package main

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	maxReplayFilterBytes   = 64
	maxReplayPlayerFilters = 8
	// statusMaxAge: /lbs/status asks the lobby at most this often; the website polls every 30 s.
	statusMaxAge = time.Second
)

// APIHandler serves the lobby's public API.
//
//	GET /lbs/replay?battle_code=C&user_id=U&user_name=N&pilot_name=P&players=4&aggregate=1&used_ms=M&reverse=1&page=0:
//	  the battles with an uploaded replay, newest first, 100 per page, as JSON (db.FoundReplay);
//	  204 when none. user_id, user_name and pilot_name may repeat; names are LIKE patterns.
//	  used_ms: an MS id + 1 that a player matching the player filters (any player without them) used.
//	GET /lbs/live: the battles streamed to live spectators (LiveBattle, running first), [] when none;
//	/lbs/live_stream: a battle's live stream, POST from its uplink, GET to watch (spectator.go).
//	GET /lbs/status: the lobby and battle users (Status), for the website's online panel.
//	POST /lbs/replay_upload: a battle's replay from an emulator.
//	POST /lbs/login (form token): logs in like the login server's /login, as JSON
//	  (login.HandleAPILogin) and answers a temporary key of the new login session.
//	/auth/*: emulator sign-in (Auth).
//	POST /discord/interactions: Discord slash commands (Discord).
//	GET or POST /lbs/replay_played?battle_code=C: an emulator started C's replay; /lbs/replay's
//	  play_count counts these. 200 even when the count fails, 400 without battle_code.
//	POST /lbs/zproxy/register, /lbs/zproxy/battle_info: zproxy's calls, JSON in and out
//	  (lobbyapi).
type APIHandler struct {
	// FindReplay searches (db.DefaultDB.FindReplay when nil).
	FindReplay func(q *FindReplayQuery) ([]*FoundReplay, error)
	// Live serves /lbs/live_stream (Spectators when nil).
	Live *SpectatorRegistry
	// LiveList lists the live battles (Live.List when nil).
	LiveList func() []*LiveBattle
	// Status reports the lobby's users and battles (an empty lobby when nil).
	Status func() *StatusResponse
	// ReplayStore keeps /lbs/replay_upload's replays (upload off when nil); a DirReplayStore's
	// files are served at /lbs/replays/.
	ReplayStore ReplayStore
	// SetReplayURL records an uploaded replay (db.DefaultDB.SetReplayURL when nil).
	SetReplayURL func(battleCode, url string) error
	// ReplayPlayed counts a play (db.DefaultDB.IncrementReplayPlayCount when nil).
	ReplayPlayed func(battleCode string) error
	// Auth serves /auth/ (emulator sign-in, auth.Server), off when nil.
	Auth http.Handler
	// Discord serves POST /discord/interactions (chatops.Discord), off when nil.
	Discord http.Handler
	// RegisterProxy and BattleInfo answer zproxy (Lbs methods), off when nil. RegisterProxy
	// gets the client's IP.
	RegisterProxy func(ip string, req *RegisterProxyRequest) *RegisterProxyResponse
	BattleInfo    func(req *BattleInfoRequest) *BattleInfoResponse

	statusMu   sync.Mutex
	statusAt   time.Time
	statusBody []byte
}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Auth != nil && strings.HasPrefix(r.URL.Path, "/auth/") {
		h.Auth.ServeHTTP(w, r)
		return
	}
	if h.Discord != nil && r.URL.Path == "/discord/interactions" {
		h.Discord.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/lbs/live_stream" {
		h.serveLiveStream(w, r)
		return
	}
	if h.RegisterProxy != nil && r.URL.Path == RegisterProxyPath {
		serveJSON(w, r, func(req *RegisterProxyRequest) any { return h.RegisterProxy(requestIP(r), req) })
		return
	}
	if h.BattleInfo != nil && r.URL.Path == BattleInfoPath {
		serveJSON(w, r, func(req *BattleInfoRequest) any { return h.BattleInfo(req) })
		return
	}
	if r.URL.Path == "/lbs/live" {
		h.serveLive(w)
		return
	}
	if r.URL.Path == "/lbs/status" {
		h.serveStatus(w)
		return
	}
	if r.URL.Path == "/lbs/replay_upload" {
		h.serveReplayUpload(w, r)
		return
	}
	if r.URL.Path == "/lbs/login" {
		HandleAPILogin(w, r)
		return
	}
	if r.URL.Path == "/lbs/replay_played" {
		h.serveReplayPlayed(w, r)
		return
	}
	if s, ok := h.ReplayStore.(*DirReplayStore); ok && strings.HasPrefix(r.URL.Path, "/lbs/"+replayUploadPrefix) {
		http.StripPrefix("/lbs/", http.FileServer(http.Dir(s.Dir))).ServeHTTP(w, r)
		return
	}
	if r.URL.Path != "/lbs/replay" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	q := NewFindReplayQuery()
	q.BattleCode = r.FormValue("battle_code")
	for _, f := range []struct {
		name string
		dst  *[]string
	}{
		{"user_id", &q.UserIDs},
		{"user_name", &q.UserNames},
		{"pilot_name", &q.PilotNames},
	} {
		for _, v := range r.Form[f.name] {
			if v == "" {
				continue
			}
			if len(v) > maxReplayFilterBytes || len(*f.dst) >= maxReplayPlayerFilters {
				http.Error(w, "invalid "+f.name+" filters", http.StatusBadRequest)
				return
			}
			*f.dst = append(*f.dst, v)
		}
	}
	for _, f := range []struct {
		name string
		dst  *int
	}{
		{"players", &q.Players},
		{"aggregate", &q.Aggregate},
		{"used_ms", &q.UsedMs},
		{"page", &q.Page},
	} {
		if v := r.FormValue(f.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				http.Error(w, "invalid query", http.StatusBadRequest)
				return
			}
			*f.dst = n
		}
	}
	q.Reverse = r.FormValue("reverse") == "1"

	find := h.FindReplay
	if find == nil {
		find = DefaultDB.FindReplay
	}
	replays, err := find(q)
	if err != nil {
		logger.Warn("lbs/replay: FindReplay failure", zap.Error(err))
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if len(replays) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(replays); err != nil {
		logger.Warn("lbs/replay: JSON encode failure", zap.Error(err))
	}
}

func (h *APIHandler) serveReplayPlayed(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	battleCode := r.FormValue("battle_code")
	if battleCode == "" || len(battleCode) > maxReplayFilterBytes {
		http.Error(w, "invalid battle_code", http.StatusBadRequest)
		return
	}
	played := h.ReplayPlayed
	if played == nil {
		played = DefaultDB.IncrementReplayPlayCount
	}
	if err := played(battleCode); err != nil {
		logger.Warn("lbs/replay_played: IncrementReplayPlayCount failure", zap.Any("battle_code", battleCode), zap.Error(err))
	}
	w.WriteHeader(http.StatusOK)
}

// serveStatus answers from the lobby's state, reused for statusMaxAge.
func (h *APIHandler) serveStatus(w http.ResponseWriter) {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	now := time.Now()
	if h.statusBody == nil || now.Sub(h.statusAt) >= statusMaxAge {
		res := &StatusResponse{}
		if h.Status != nil {
			res = h.Status()
		}
		body, err := json.Marshal(MakeStatus(res, now))
		if err != nil {
			logger.Warn("lbs/status: JSON encode failure", zap.Error(err))
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		h.statusBody, h.statusAt = body, now
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(h.statusBody)
}

func (h *APIHandler) serveLive(w http.ResponseWriter) {
	list := h.LiveList
	if list == nil {
		list = h.live().List
	}
	battles := list()
	if battles == nil {
		battles = []*LiveBattle{} // [], not null
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(battles); err != nil {
		logger.Warn("lbs/live: JSON encode failure", zap.Error(err))
	}
}

var jst = time.FixedZone("Asia/Tokyo", 9*60*60)

const statusTimeFormat = "2006年 01月02日 15:04"

// StatusUser is a player on the status page.
type StatusUser struct {
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Team     string `json:"team"`
	UDP      string `json:"udp"`
	Platform string `json:"platform"`
}

// PlatformCount counts the users of one platform.
type PlatformCount struct {
	LobbyUserCount  int `json:"lobby_user_count"`
	BattleUserCount int `json:"battle_user_count"`
}

// Status is the /lbs/status body: who is in the lobby and who is in a battle.
type Status struct {
	NowDate        string       `json:"now_date"`
	LobbyUserCount int          `json:"lobby_user_count"`
	LobbyUsers     []StatusUser `json:"lobby_users"`

	BattleUserCount int          `json:"battle_user_count"`
	BattleUsers     []StatusUser `json:"battle_users"`

	// Platforms counts users per platform; console and emulator players never meet.
	Platforms map[string]*PlatformCount `json:"platforms"`
}

func newStatusUser(u ZproxyUser) StatusUser {
	user := StatusUser{
		UserID:   u.UserID,
		Name:     u.Name,
		Team:     u.Team,
		Platform: u.Platform,
	}
	if user.Platform == "" {
		user.Platform = PlatformConsole
	}
	if u.UDP {
		user.UDP = "○"
	}
	return user
}

// MakeStatus turns a lobby status into the /lbs/status body; a user counts once.
func MakeStatus(res *StatusResponse, now time.Time) Status {
	s := Status{
		NowDate:     now.In(jst).Format(statusTimeFormat),
		LobbyUsers:  []StatusUser{},
		BattleUsers: []StatusUser{},
		Platforms: map[string]*PlatformCount{
			PlatformConsole: {},
			PlatformEmu:     {},
		},
	}
	count := func(platform string) *PlatformCount {
		c, ok := s.Platforms[platform]
		if !ok {
			c = &PlatformCount{}
			s.Platforms[platform] = c
		}
		return c
	}
	checked := map[string]bool{}
	for _, u := range res.LobbyUsers {
		if checked[u.UserID] {
			continue
		}
		checked[u.UserID] = true
		user := newStatusUser(u)
		s.LobbyUsers = append(s.LobbyUsers, user)
		count(user.Platform).LobbyUserCount++
	}
	for _, b := range res.Battles {
		for _, u := range b.Users {
			if checked[u.UserID] {
				continue
			}
			checked[u.UserID] = true
			user := newStatusUser(u)
			s.BattleUsers = append(s.BattleUsers, user)
			count(user.Platform).BattleUserCount++
		}
	}
	s.LobbyUserCount = len(s.LobbyUsers)
	s.BattleUserCount = len(s.BattleUsers)
	return s
}

const maxZproxyRequestBytes = 64 << 10

func serveJSON[T any](w http.ResponseWriter, r *http.Request, answer func(req *T) any) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	req := new(T)
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxZproxyRequestBytes)).Decode(req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(answer(req)); err != nil {
		logger.Warn("JSON encode failure", zap.String("path", r.URL.Path), zap.Error(err))
	}
}

// requestIP is the client's IP. The API listens behind a reverse proxy on this host:
// a request from loopback with X-Forwarded-For comes from its last hop, the one the
// proxy appended.
func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return host
	}
	fwd := r.Header.Values("X-Forwarded-For")
	if len(fwd) == 0 {
		return host
	}
	hops := strings.Split(fwd[len(fwd)-1], ",")
	return strings.TrimSpace(hops[len(hops)-1])
}
