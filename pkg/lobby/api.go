package lobby

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"zdxsv/pkg/db"
	"zdxsv/pkg/lobby/lobbyrpc"
	"zdxsv/pkg/login"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
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
type APIHandler struct {
	// FindReplay searches (db.DefaultDB.FindReplay when nil).
	FindReplay func(q *db.FindReplayQuery) ([]*db.FoundReplay, error)
	// Live serves /lbs/live_stream (Spectators when nil).
	Live *SpectatorRegistry
	// LiveList lists the live battles (Live.List when nil).
	LiveList func() []*LiveBattle
	// Status reports the lobby's users and battles (an empty lobby when nil).
	Status func() *lobbyrpc.StatusResponse
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
		login.HandleAPILogin(w, r)
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
	q := db.NewFindReplayQuery()
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
		find = db.DefaultDB.FindReplay
	}
	replays, err := find(q)
	if err != nil {
		zlog.Warn("lbs/replay: FindReplay failure", zap.Error(err))
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if len(replays) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(replays); err != nil {
		zlog.Warn("lbs/replay: JSON encode failure", zap.Error(err))
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
		played = db.DefaultDB.IncrementReplayPlayCount
	}
	if err := played(battleCode); err != nil {
		zlog.Warn("lbs/replay_played: IncrementReplayPlayCount failure", zap.Any("battle_code", battleCode), zap.Error(err))
	}
	w.WriteHeader(http.StatusOK)
}

// serveStatus answers from the lobby's state, reused for statusMaxAge.
func (h *APIHandler) serveStatus(w http.ResponseWriter) {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	now := time.Now()
	if h.statusBody == nil || now.Sub(h.statusAt) >= statusMaxAge {
		res := &lobbyrpc.StatusResponse{}
		if h.Status != nil {
			res = h.Status()
		}
		body, err := json.Marshal(MakeStatus(res, now))
		if err != nil {
			zlog.Warn("lbs/status: JSON encode failure", zap.Error(err))
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
		zlog.Warn("lbs/live: JSON encode failure", zap.Error(err))
	}
}
