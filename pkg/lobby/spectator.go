package lobby

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"zdxsv/pkg/db"

	"zdxsv/pkg/zlog"
)

// Live spectating over the lobby HTTP API. The battle's uplink (spectator_uplink in
// its P2PMatching) opens one POST /lbs/live_stream?battle_code=&session_id= (session_id
// = the battle's ggpo_session) at the battle start and streams the body until the
// battle ends. A spectator opens one GET /lbs/live_stream?battle_code= and receives the
// whole upload from its first byte, as it grows, until the upload ended; the lobby then
// closes the connection. The bytes are in the emulator's format; the
// lobby stores and forwards them without parsing.
//
// GET without battle_code: the newest battle with an upload (running ones first); with
// skip=<code>,<code> the newest running one not in skip (auto-next). The answer names the
// battle in X-Battle-Code. 404: none.
//
// One goroutine (Run) writes to every spectator in turn, liveTick apart, each write
// bounded by liveWriteWait: a spectator whose socket is full is skipped until its next
// turn and dropped after liveStall without progress.
const (
	liveTick              = time.Second / 3
	liveWriteWait         = 5 * time.Millisecond
	liveStall             = 30 * time.Second
	liveUploadIdle        = 10 * time.Second // the uplink sends at least every liveTick
	liveMaxBytes          = 32 << 20
	liveRetention         = 10 * time.Minute // after the upload ended
	liveIdle              = 10 * time.Minute // opened, no upload
	liveMaxSpectators     = 4096
	liveMaxBattleWatchers = 512
	liveReadBuf           = 32 << 10
)

// Spectators is the lobby's registry; battleInfoNotice opens a battle per GGPO battle.
var Spectators = NewSpectatorRegistry()

type SpectatorRegistry struct {
	mu        sync.Mutex
	battles   map[string]*liveBattle
	nwatchers int
	join      chan *liveWatcher
}

type liveBattle struct {
	code      string
	session   int32
	opened    time.Time
	uploading bool
	uploaded  bool      // an upload started
	done      time.Time // the upload ended; zero before
	buf       []byte    // append-only: a slice of it stays valid without the lock
	watchers  int
	users     []*db.ReplayUser // for the /lbs/live list
}

// liveWatcher is owned by Run once joined.
type liveWatcher struct {
	b        *liveBattle
	conn     net.Conn
	pre      []byte // the response header not written yet
	pos      int
	progress time.Time
}

func NewSpectatorRegistry() *SpectatorRegistry {
	return &SpectatorRegistry{battles: map[string]*liveBattle{}, join: make(chan *liveWatcher, 64)}
}

// Open starts accepting the battle's upload (session = its ggpo_session; users for List). Idempotent.
func (r *SpectatorRegistry) Open(code string, session uint32, users []*db.ReplayUser, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.battles[code]; ok {
		return
	}
	r.battles[code] = &liveBattle{code: code, session: int32(session), opened: now, users: users}
	zlog.Infoln("live open", code)
}

// Run writes to the spectators until stop is closed (nil: forever).
func (r *SpectatorRegistry) Run(stop <-chan struct{}) {
	t := time.NewTicker(liveTick)
	defer t.Stop()
	var ws []*liveWatcher
	turn := 0
	for {
		select {
		case <-stop:
			for _, w := range ws {
				r.drop(w)
			}
			return
		case w := <-r.join:
			ws = append(ws, w)
		case now := <-t.C:
			ws = r.deliver(ws, turn, now)
			turn++
			r.Tick(now)
		}
	}
}

func (r *SpectatorRegistry) deliver(ws []*liveWatcher, turn int, now time.Time) []*liveWatcher {
	if len(ws) == 0 {
		return ws
	}
	type view struct {
		buf  []byte
		done bool
	}
	views := map[*liveBattle]view{}
	r.mu.Lock()
	for _, w := range ws {
		views[w.b] = view{w.b.buf, !w.b.done.IsZero()}
	}
	r.mu.Unlock()
	end := now.Add(liveTick / 2)
	keep := ws[:0]
	n := len(ws)
	order := make([]*liveWatcher, n)
	for i := range ws {
		order[i] = ws[(i+turn)%n]
	}
	for _, w := range order {
		v := views[w.b]
		if !r.write(w, v.buf, end) || (v.done && len(w.pre) == 0 && w.pos == len(v.buf)) || now.Sub(w.progress) > liveStall {
			r.drop(w)
			continue
		}
		keep = append(keep, w)
	}
	for i := len(keep); i < n; i++ {
		ws[i] = nil
	}
	return keep
}

// write sends what w has not got; false: the connection failed.
func (r *SpectatorRegistry) write(w *liveWatcher, buf []byte, end time.Time) bool {
	for len(w.pre) > 0 || w.pos < len(buf) {
		dl := time.Now().Add(liveWriteWait)
		if dl.After(end) {
			dl = end
		}
		w.conn.SetWriteDeadline(dl)
		data := w.pre
		if len(data) == 0 {
			data = buf[w.pos:]
		}
		n, err := w.conn.Write(data)
		if n > 0 {
			w.progress = time.Now()
			if len(w.pre) > 0 {
				w.pre = w.pre[n:]
			} else {
				w.pos += n
			}
		}
		if err != nil {
			return errors.Is(err, os.ErrDeadlineExceeded)
		}
	}
	return true
}

func (r *SpectatorRegistry) drop(w *liveWatcher) {
	w.conn.Close()
	r.mu.Lock()
	w.b.watchers--
	r.nwatchers--
	r.mu.Unlock()
}

// Tick drops battles ended longer than liveRetention ago, or opened liveIdle ago without an upload.
func (r *SpectatorRegistry) Tick(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for code, b := range r.battles {
		if (!b.done.IsZero() && now.Sub(b.done) > liveRetention) || (!b.uploaded && now.Sub(b.opened) > liveIdle) {
			delete(r.battles, code)
			zlog.Infoln("live drop", code)
		}
	}
}

func (r *SpectatorRegistry) startUpload(code string, session int32) (*liveBattle, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.battles[code]
	if b == nil || b.session != session {
		return nil, http.StatusNotFound
	}
	if b.uploaded {
		return nil, http.StatusConflict
	}
	b.uploaded, b.uploading = true, true
	zlog.Infoln("live upload", code)
	return b, 0
}

// add appends to b's stream; false past liveMaxBytes.
func (r *SpectatorRegistry) add(b *liveBattle, data []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(b.buf)+len(data) > liveMaxBytes {
		return false
	}
	b.buf = append(b.buf, data...)
	return true
}

func (r *SpectatorRegistry) endUpload(b *liveBattle, why string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b.uploading = false
	b.done = now
	zlog.Infoln("live end", b.code, why, "bytes", len(b.buf))
}

// pick returns the battle a GET watches and counts the watcher; 0 or an HTTP status.
func (r *SpectatorRegistry) pick(code string, skip []string) (*liveBattle, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b *liveBattle
	if code != "" {
		b = r.battles[code]
	} else {
		b = r.newest(skip)
	}
	if b == nil || !b.uploaded {
		return nil, http.StatusNotFound
	}
	if r.nwatchers >= liveMaxSpectators || b.watchers >= liveMaxBattleWatchers {
		return nil, http.StatusServiceUnavailable
	}
	b.watchers++
	r.nwatchers++
	return b, 0
}

func (r *SpectatorRegistry) unpick(b *liveBattle) {
	r.mu.Lock()
	b.watchers--
	r.nwatchers--
	r.mu.Unlock()
}

// newest: with skip nil the latest opened battle with an upload, running ones first; else
// the latest opened running one not in skip.
func (r *SpectatorRegistry) newest(skip []string) *liveBattle {
	var best *liveBattle
	for _, b := range r.battles {
		if !b.uploaded || (skip != nil && (!b.done.IsZero() || containsCode(skip, b.code))) {
			continue
		}
		if best == nil || (best.done.IsZero() == b.done.IsZero() && best.opened.Before(b.opened)) || (!best.done.IsZero() && b.done.IsZero()) {
			best = b
		}
	}
	return best
}

// LiveBattle is one /lbs/live entry: a battle with an upload.
type LiveBattle struct {
	BattleCode string           `json:"battle_code"`
	Users      []*db.ReplayUser `json:"users,omitempty"`
	StartUnix  int64            `json:"start_unix"`
	Bytes      int              `json:"bytes"`
	Closed     bool             `json:"closed"`
	Spectators int              `json:"spectators"`
}

// List returns the battles with an upload, running first, then newest opened first.
func (r *SpectatorRegistry) List() []*LiveBattle {
	r.mu.Lock()
	defer r.mu.Unlock()
	var bs []*liveBattle
	for _, b := range r.battles {
		if b.uploaded {
			bs = append(bs, b)
		}
	}
	sort.Slice(bs, func(i, j int) bool {
		if bs[i].done.IsZero() != bs[j].done.IsZero() {
			return bs[i].done.IsZero()
		}
		return bs[i].opened.After(bs[j].opened)
	})
	list := make([]*LiveBattle, 0, len(bs))
	for _, b := range bs {
		list = append(list, &LiveBattle{
			BattleCode: b.code,
			Users:      b.users,
			StartUnix:  b.opened.Unix(),
			Bytes:      len(b.buf),
			Closed:     !b.done.IsZero(),
			Spectators: b.watchers,
		})
	}
	return list
}

func containsCode(codes []string, code string) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

func (h *APIHandler) serveLiveStream(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.serveLiveUpload(w, r)
	case http.MethodGet:
		h.serveLiveWatch(w, r)
	default:
		http.Error(w, "", http.StatusMethodNotAllowed)
	}
}

func (h *APIHandler) serveLiveUpload(w http.ResponseWriter, r *http.Request) {
	session, err := strconv.ParseInt(r.URL.Query().Get("session_id"), 10, 64)
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	reg := h.live()
	b, status := reg.startUpload(r.URL.Query().Get("battle_code"), int32(session))
	if b == nil {
		http.Error(w, "", status)
		return
	}
	rc := http.NewResponseController(w)
	buf := make([]byte, liveReadBuf)
	why := "eof"
	for {
		rc.SetReadDeadline(time.Now().Add(liveUploadIdle))
		n, err := r.Body.Read(buf)
		if n > 0 && !reg.add(b, buf[:n]) {
			why = "too large"
			break
		}
		if err != nil {
			if err != io.EOF {
				why = err.Error()
			}
			break
		}
	}
	reg.endUpload(b, why, time.Now())
	if why == "eof" {
		w.WriteHeader(http.StatusNoContent)
	} else {
		http.Error(w, "", http.StatusBadRequest)
	}
}

func (h *APIHandler) serveLiveWatch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var skip []string
	if _, ok := q["skip"]; ok {
		skip = []string{}
		if s := q.Get("skip"); s != "" {
			skip = strings.Split(s, ",")
		}
	}
	reg := h.live()
	b, status := reg.pick(q.Get("battle_code"), skip)
	if b == nil {
		http.Error(w, "", status)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		reg.unpick(b)
		http.Error(w, "", http.StatusHTTPVersionNotSupported)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		reg.unpick(b)
		return
	}
	pre := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nCache-Control: no-store\r\nX-Battle-Code: %s\r\nConnection: close\r\n\r\n", b.code)
	reg.join <- &liveWatcher{b: b, conn: conn, pre: []byte(pre), progress: time.Now()}
}

func (h *APIHandler) live() *SpectatorRegistry {
	if h.Live != nil {
		return h.Live
	}
	return Spectators
}
