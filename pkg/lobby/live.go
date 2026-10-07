package lobby

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
)

// LiveURL is offered to players in the battle info (live=), "" when the replay server is off.
var LiveURL string

var liveCloseRe = regexp.MustCompile(`[^0-9A-Za-z_.-]`)

const (
	// maxLiveStreams bounds the streams kept in memory (each holds a ~8 MB save state).
	maxLiveStreams = 32
	// liveKeep is how long a stream stays after its close or its last post.
	liveKeep = 10 * time.Minute
)

// liveStream is one battle being streamed by its players: the .zdxr header and frame 0
// state from the first start, then every player's confirmed inputs as they come.
type liveStream struct {
	code    string
	start   []byte // "ZDXSV-REPLAY 1\n" header + "\n\n" + frame 0 state
	record  int    // bytes per frame: players * input_size
	inputs  []byte // frames * record
	close   string // "" = still running
	created time.Time
	updated time.Time
	users   map[string]bool // senders that passed Known
}

func (l *liveStream) frames() int { return len(l.inputs) / l.record }

// Live spectating (pcsx2 Zdxsv: the battle's players stream it, spectators poll):
//
//	POST /live/start?battle_code=C&user_id=U, body = a .zdxr without inputs (header with
//	  players= and input_size=, frame 0 state): opens the stream; a later start of C is
//	  ignored (200 either way, the first player's state is kept).
//	POST /live/inputs?battle_code=C&user_id=U&from=F[&close=why], body = the inputs of
//	  frames F..: the frames the stream lacks are appended; close= ends the stream. Reply:
//	  the stream's frame count (text), so a sender resends from there. 409: no stream yet.
//	GET /live: the streams, newest first, a line each: "C frames close" (close "-" = running).
//	GET /live/C: the start body. GET /live/C/inputs?from=F: "frames=N close=why\n" +
//	  the inputs of frames F..N-1 (close "" = running).
func (s *ReplayServer) serveLive(w http.ResponseWriter, r *http.Request) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	if s.live == nil {
		s.live = map[string]*liveStream{}
	}
	s.pruneLive(time.Now())
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodPost && path == "/live/start":
		s.liveStart(w, r)
	case r.Method == http.MethodPost && path == "/live/inputs":
		s.liveInputs(w, r)
	case r.Method == http.MethodGet && path == "/live":
		var list []*liveStream
		for _, l := range s.live {
			list = append(list, l)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].created.After(list[j].created) })
		w.Header().Set("Content-Type", "text/plain")
		for _, l := range list {
			c := l.close
			if c == "" {
				c = "-"
			}
			fmt.Fprintf(w, "%s %d %s\n", l.code, l.frames(), c)
		}
	case r.Method == http.MethodGet:
		rest, _ := strings.CutPrefix(path, "/live/")
		code, inputs := strings.CutSuffix(rest, "/inputs")
		l := s.live[code]
		if l == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if !inputs {
			w.Write(l.start)
			return
		}
		from, err := strconv.Atoi(r.URL.Query().Get("from"))
		if err != nil || from < 0 {
			http.Error(w, "bad from", http.StatusBadRequest)
			return
		}
		if from > l.frames() {
			from = l.frames()
		}
		fmt.Fprintf(w, "frames=%d close=%s\n", l.frames(), l.close)
		w.Write(l.inputs[from*l.record:])
	default:
		http.NotFound(w, r)
	}
}

func (s *ReplayServer) pruneLive(now time.Time) {
	for code, l := range s.live {
		if now.Sub(l.updated) > liveKeep {
			delete(s.live, code)
		}
	}
}

// liveSender checks the query's battle code and user; s.mtx is held.
func (s *ReplayServer) liveSender(w http.ResponseWriter, r *http.Request) (code, user string, ok bool) {
	code, user = r.URL.Query().Get("battle_code"), r.URL.Query().Get("user_id")
	if !battleCodeRe.MatchString(code) || user == "" {
		http.Error(w, "bad battle_code or user_id", http.StatusBadRequest)
		return "", "", false
	}
	if l := s.live[code]; l != nil && l.users[user] {
		return code, user, true
	}
	if !s.Known(code, user) {
		glog.Infoln("live: no battle record", code, user, r.RemoteAddr)
		http.Error(w, "unknown battle", http.StatusForbidden)
		return "", "", false
	}
	if l := s.live[code]; l != nil {
		l.users[user] = true
	}
	return code, user, true
}

func (s *ReplayServer) liveStart(w http.ResponseWriter, r *http.Request) {
	code, user, ok := s.liveSender(w, r)
	if !ok {
		return
	}
	if s.live[code] != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if len(s.live) >= maxLiveStreams {
		http.Error(w, "too many live battles", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxReplaySize+1))
	if err != nil || len(body) > MaxReplaySize {
		http.Error(w, "read failed or too large", http.StatusBadRequest)
		return
	}
	head, state, ok := bytes.Cut(body, []byte("\n\n"))
	kv := map[string]string{}
	for _, line := range strings.Split(string(head), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			kv[k] = v
		}
	}
	players, _ := strconv.Atoi(kv["players"])
	inputSize, _ := strconv.Atoi(kv["input_size"])
	stateSize, _ := strconv.Atoi(kv["state_size"])
	if !ok || !bytes.HasPrefix(head, []byte("ZDXSV-REPLAY 1\n")) || kv["battle_code"] != code ||
		players < 1 || players > 4 || inputSize < 1 || inputSize > 1024 || stateSize != len(state) {
		http.Error(w, "not a live start of this battle", http.StatusBadRequest)
		return
	}
	now := time.Now()
	s.live[code] = &liveStream{code: code, start: body, record: players * inputSize, created: now, updated: now,
		users: map[string]bool{user: true}}
	glog.Infoln("live start", code, "by", user, len(body), "bytes")
	w.WriteHeader(http.StatusOK)
}

func (s *ReplayServer) liveInputs(w http.ResponseWriter, r *http.Request) {
	code, _, ok := s.liveSender(w, r)
	if !ok {
		return
	}
	l := s.live[code]
	if l == nil {
		http.Error(w, "no live start", http.StatusConflict)
		return
	}
	from, err := strconv.Atoi(r.URL.Query().Get("from"))
	body, rerr := io.ReadAll(io.LimitReader(r.Body, MaxReplaySize+1))
	if err != nil || from < 0 || rerr != nil || len(body)%l.record != 0 ||
		len(l.inputs)+len(body) > MaxReplaySize {
		http.Error(w, "bad inputs", http.StatusBadRequest)
		return
	}
	// frames already there are dropped (every player sends the same confirmed frames),
	// a gap is not filled: the reply's count tells the sender where to resend from
	if have := l.frames(); from <= have && from*l.record+len(body) > len(l.inputs) {
		l.inputs = append(l.inputs, body[(have-from)*l.record:]...)
	}
	if c := liveCloseRe.ReplaceAllString(r.URL.Query().Get("close"), "_"); c != "" && len(c) <= 64 && l.close == "" && from*l.record+len(body) >= len(l.inputs) {
		l.close = c
		glog.Infoln("live close", code, l.frames(), "frames:", c)
	}
	l.updated = time.Now()
	fmt.Fprintf(w, "%d", l.frames())
}
