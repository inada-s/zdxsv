package lobby

import (
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"zdxsv/pkg/db"
	"zdxsv/pkg/proto"

	"github.com/golang/glog"
	pb "github.com/golang/protobuf/proto"
)

// Live spectating over the lobby HTTP API: one GGPO participant of a lobby battle
// (live_uplink=1 in the battle info) POSTs its replay to /lbs/live_push as it records
// it, and spectators long-poll /lbs/live_pull.
//
// No start state: a spectator plays the battle start from the hosted common state
// and the header's lobby answers.
//
// /lbs/live_push: body a SpectatorInputPush (battle_code, session_id = the battle's
// ggpo_session, and any of header, round_data, start_frame + frame_bytes + input_data,
// close_reason with start_frame = the last frame); answer a SpectatorInputAck with
// what the lobby holds. Inputs past the held frames are dropped: the uplink resends
// from ack_frame.
//
// /lbs/live_pull?battle_code=&skip=&frame=&header=&round=&id=: battle_code empty picks
// the newest battle, or with skip (comma-separated codes) the newest running one not in
// skip. frame: the frames the spectator holds; header=1: it holds the header; round: the
// round_data bytes it holds; id: a random spectator id (counts spectators). Answer a
// SpectatorInputPush with battle_code and what is new: header, round_data (whole),
// input_data from frame, close_reason once every frame was sent. Without news it waits
// up to liveWait. 404: no such battle.
const (
	liveMaxPushBytes        = 256 << 10
	liveMaxPullFrames       = 600
	liveWait                = time.Second
	liveMaxHeaderBytes      = 16 << 10
	liveMaxRoundBytes       = 4 << 10
	liveMaxFrameBytes       = 4 * 64
	liveMaxFrames           = 60 * 60 * 60 // an hour at 60 fps: a battle is shorter, the memory per session bounded
	liveSubscriberTimeout   = 10 * time.Second
	liveRetention           = 10 * time.Minute // after close
	liveIdle                = 10 * time.Minute // without a push
	liveEndGrace            = 5 * time.Second  // after a player's battle end, without a push: the lobby closes
	liveMaxSubscribers      = 4096
	liveMaxBattleSubscriber = 512
	liveMaxIDBytes          = 64
)

// Spectators is the lobby's registry; battleInfoNotice opens a session per GGPO battle.
var Spectators = NewSpectatorRegistry()

type SpectatorRegistry struct {
	mu       sync.Mutex
	sessions map[string]*liveSession
	nsubs    int
}

type liveSession struct {
	code        string
	session     int32
	opened      time.Time
	lastPush    time.Time
	closed      time.Time // zero while running
	ended       time.Time // first player back in the lobby or reporting the battle; zero before
	pushed      bool
	header      []byte
	rounds      []byte
	frameBytes  int
	inputs      []byte
	closeReason string
	subs        map[string]time.Time // spectator id -> last pull
	changed     chan struct{}
	users       []*db.ReplayUser // for the /lbs/live list
}

func NewSpectatorRegistry() *SpectatorRegistry {
	return &SpectatorRegistry{sessions: map[string]*liveSession{}}
}

// Open starts accepting the battle's uplink (session = its ggpo_session; users for List). Idempotent.
func (r *SpectatorRegistry) Open(code string, session uint32, users []*db.ReplayUser, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[code]; ok {
		return
	}
	r.sessions[code] = &liveSession{code: code, session: int32(session), opened: now, lastPush: now, subs: map[string]time.Time{}, changed: make(chan struct{}), users: users}
	glog.Infoln("live open", code)
}

// End marks userID's battle as over. A still-pushing uplink ends with its own close; a silent one
// is closed by Tick after liveEndGrace.
func (r *SpectatorRegistry) End(code, userID string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[code]
	if s == nil || !s.closed.IsZero() || !s.ended.IsZero() {
		return
	}
	for _, u := range s.users {
		if u.UserID == userID {
			s.ended = now
			glog.Infoln("live end", code, userID)
			return
		}
	}
}

func (r *SpectatorRegistry) Run() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for now := range t.C {
		r.Tick(now)
	}
}

func (s *liveSession) notify() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *liveSession) frames() int {
	if s.frameBytes == 0 {
		return 0
	}
	return len(s.inputs) / s.frameBytes
}

// Push takes an uplink push and returns the ack (nil: no such battle or session).
func (r *SpectatorRegistry) Push(m *proto.SpectatorInputPush, now time.Time) *proto.SpectatorInputAck {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[m.GetBattleCode()]
	if s == nil || m.GetSessionId() != s.session {
		return nil
	}
	if !s.pushed {
		s.pushed = true
		glog.Infoln("live uplink", s.code)
	}
	s.lastPush = now
	changed := false
	if h := m.GetHeader(); len(h) > 0 && s.header == nil && len(h) <= liveMaxHeaderBytes {
		s.header = append([]byte(nil), h...)
		changed = true
	}
	if rd := m.GetRoundData(); len(s.rounds) < len(rd) && len(rd) <= liveMaxRoundBytes {
		s.rounds = append([]byte(nil), rd...)
		changed = true
	}
	if fb := int(m.GetFrameBytes()); 0 < fb && fb <= liveMaxFrameBytes && (s.frameBytes == 0 || s.frameBytes == fb) {
		s.frameBytes = fb
		in := m.GetInputData()
		start := int(m.GetStartFrame()) * fb
		if s.closed.IsZero() && len(in)%fb == 0 && 0 <= start && start <= len(s.inputs) && len(s.inputs) < start+len(in) && start+len(in) <= liveMaxFrames*fb {
			s.inputs = append(s.inputs, in[len(s.inputs)-start:]...)
			changed = true
		}
	}
	if m.CloseReason != nil && s.closed.IsZero() && s.frameBytes > 0 && int(m.GetStartFrame())*s.frameBytes == len(s.inputs) {
		s.closed = now
		s.closeReason = m.GetCloseReason()
		changed = true
		glog.Infoln("live close", s.code, s.closeReason, "frames", s.frames())
	}
	if changed {
		s.notify()
	}
	return &proto.SpectatorInputAck{
		BattleCode: pb.String(s.code),
		AckFrame:   pb.Int32(int32(s.frames())),
		HeaderAck:  pb.Bool(s.header != nil),
		RoundAck:   pb.Int32(int32(len(s.rounds))),
		CloseAck:   pb.Bool(!s.closed.IsZero()),
	}
}

// LivePull is what a spectator holds of a battle (Code empty: pick one, see Skip).
type LivePull struct {
	Code   string
	Skip   []string // with Code empty: the newest running battle not in Skip
	ID     string
	Frame  int
	Header bool
	Round  int
}

// Pull returns what is new for q (nil: no such battle, or the subscriber limit),
// waiting up to wait for it. done aborts the wait.
func (r *SpectatorRegistry) Pull(q *LivePull, wait time.Duration, done <-chan struct{}) *proto.SpectatorInputPush {
	r.mu.Lock()
	code := q.Code
	if code == "" && len(q.Skip) > 0 {
		code = r.next(q.Skip)
	} else if code == "" {
		code = r.newest()
	}
	s := r.sessions[code]
	if s == nil || !r.subscribe(s, q.ID, time.Now()) {
		r.mu.Unlock()
		return nil
	}
	m := s.news(q)
	if m == nil && wait > 0 {
		ch := s.changed
		r.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-ch:
		case <-t.C:
		case <-done:
		}
		t.Stop()
		r.mu.Lock()
		m = s.news(q)
	}
	r.mu.Unlock()
	if m == nil {
		m = &proto.SpectatorInputPush{}
	}
	m.BattleCode = pb.String(code)
	return m
}

func (r *SpectatorRegistry) subscribe(s *liveSession, id string, now time.Time) bool {
	if _, ok := s.subs[id]; !ok {
		if r.nsubs >= liveMaxSubscribers || len(s.subs) >= liveMaxBattleSubscriber {
			return false
		}
		r.nsubs++
		glog.Infoln("live subscribe", s.code, id)
	}
	s.subs[id] = now
	return true
}

// news returns what q lacks (nil: nothing). Header first: nothing else before the spectator has it.
func (s *liveSession) news(q *LivePull) *proto.SpectatorInputPush {
	if !q.Header {
		if s.header == nil {
			return nil
		}
		return &proto.SpectatorInputPush{Header: s.header}
	}
	m := &proto.SpectatorInputPush{}
	any := false
	if q.Round < len(s.rounds) {
		m.RoundData = s.rounds
		any = true
	}
	have := s.frames()
	if fb := s.frameBytes; fb > 0 && 0 <= q.Frame && q.Frame < have {
		n := have - q.Frame
		if n > liveMaxPullFrames {
			n = liveMaxPullFrames
		}
		m.StartFrame = pb.Int32(int32(q.Frame))
		m.FrameBytes = pb.Int32(int32(fb))
		m.InputData = s.inputs[q.Frame*fb : (q.Frame+n)*fb]
		any = true
	} else if !s.closed.IsZero() && q.Frame == have {
		m.StartFrame = pb.Int32(int32(have))
		m.CloseReason = pb.String(s.closeReason)
		any = true
	}
	if !any {
		return nil
	}
	return m
}

// newest returns the latest opened battle with an uplink, preferring running ones.
func (r *SpectatorRegistry) newest() string {
	var best *liveSession
	for _, s := range r.sessions {
		if !s.pushed {
			continue
		}
		if best == nil || (best.closed.IsZero() == s.closed.IsZero() && best.opened.Before(s.opened)) || (!best.closed.IsZero() && s.closed.IsZero()) {
			best = s
		}
	}
	if best == nil {
		return ""
	}
	return best.code
}

// LiveBattle is one /lbs/live entry: a battle whose uplink has pushed.
type LiveBattle struct {
	BattleCode string           `json:"battle_code"`
	Users      []*db.ReplayUser `json:"users,omitempty"`
	StartUnix  int64            `json:"start_unix"`
	Frames     int              `json:"frames"`
	Closed     bool             `json:"closed"`
	Spectators int              `json:"spectators"`
}

// List returns the battles with an uplink, running first, then newest opened first.
func (r *SpectatorRegistry) List() []*LiveBattle {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ss []*liveSession
	for _, s := range r.sessions {
		if s.pushed {
			ss = append(ss, s)
		}
	}
	sort.Slice(ss, func(i, j int) bool {
		if ss[i].closed.IsZero() != ss[j].closed.IsZero() {
			return ss[i].closed.IsZero()
		}
		return ss[i].opened.After(ss[j].opened)
	})
	list := make([]*LiveBattle, 0, len(ss))
	for _, s := range ss {
		list = append(list, &LiveBattle{
			BattleCode: s.code,
			Users:      s.users,
			StartUnix:  s.opened.Unix(),
			Frames:     s.frames(),
			Closed:     !s.closed.IsZero(),
			Spectators: len(s.subs),
		})
	}
	return list
}

// next is auto-next's pick: the latest opened running
// battle with an uplink that the spectator has not watched, so it joins near the start.
func (r *SpectatorRegistry) next(skip []string) string {
	var best *liveSession
	for _, s := range r.sessions {
		if !s.pushed || !s.closed.IsZero() || containsCode(skip, s.code) {
			continue
		}
		if best == nil || best.opened.Before(s.opened) {
			best = s
		}
	}
	if best == nil {
		return ""
	}
	return best.code
}

// Tick closes battles whose uplink went silent after the battle end, drops spectators
// silent for liveSubscriberTimeout, and sessions closed longer than liveRetention or
// without a push for liveIdle.
func (r *SpectatorRegistry) Tick(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for code, s := range r.sessions {
		if (!s.closed.IsZero() && now.Sub(s.closed) > liveRetention) || (s.closed.IsZero() && now.Sub(s.lastPush) > liveIdle) {
			r.nsubs -= len(s.subs)
			delete(r.sessions, code)
			s.notify()
			glog.Infoln("live drop", code)
			continue
		}
		if s.closed.IsZero() && !s.ended.IsZero() && now.Sub(s.ended) >= liveEndGrace && now.Sub(s.lastPush) >= liveEndGrace {
			s.closed = now
			s.closeReason = "battle end"
			s.notify()
			glog.Infoln("live close", s.code, s.closeReason, "frames", s.frames(), "uplink silent")
		}
		for id, seen := range s.subs {
			if now.Sub(seen) > liveSubscriberTimeout {
				delete(s.subs, id)
				r.nsubs--
			}
		}
	}
}

func containsCode(codes []string, code string) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

func (h *APIHandler) serveLivePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, liveMaxPushBytes+1))
	if err != nil || len(body) > liveMaxPushBytes {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	m := new(proto.SpectatorInputPush)
	if err := pb.Unmarshal(body, m); err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	ack := h.live().Push(m, time.Now())
	if ack == nil {
		http.NotFound(w, r)
		return
	}
	writeProto(w, ack)
}

func (h *APIHandler) serveLivePull(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	q := &LivePull{Code: r.FormValue("battle_code"), ID: r.FormValue("id"), Header: r.FormValue("header") == "1"}
	if s := r.FormValue("skip"); s != "" {
		q.Skip = strings.Split(s, ",")
	}
	q.Frame, _ = strconv.Atoi(r.FormValue("frame"))
	q.Round, _ = strconv.Atoi(r.FormValue("round"))
	if q.ID == "" || len(q.ID) > liveMaxIDBytes {
		q.ID = r.RemoteAddr
	}
	m := h.live().Pull(q, liveWait, r.Context().Done())
	if m == nil {
		http.NotFound(w, r)
		return
	}
	writeProto(w, m)
}

func (h *APIHandler) live() *SpectatorRegistry {
	if h.Live != nil {
		return h.Live
	}
	return Spectators
}

func writeProto(w http.ResponseWriter, m pb.Message) {
	data, err := pb.Marshal(m)
	if err != nil {
		glog.Errorln(err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}
