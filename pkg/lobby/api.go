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

	"github.com/golang/glog"
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
//	  a spectator watches one at udp://<lobby host>:<STUN port>/<battle_code>.
//	GET /lbs/status: the lobby and battle users (Status), for the website's online panel.
//	POST /lbs/replay_upload: a battle's replay from an emulator.
type APIHandler struct {
	// FindReplay searches (db.DefaultDB.FindReplay when nil).
	FindReplay func(q *db.FindReplayQuery) ([]*db.FoundReplay, error)
	// LiveList lists the live battles (Spectators.List when nil).
	LiveList func() []*LiveBattle
	// Status reports the lobby's users and battles (an empty lobby when nil).
	Status func() *lobbyrpc.StatusResponse
	// ReplayStore keeps /lbs/replay_upload's replays (upload off when nil); a DirReplayStore's
	// files are served at /lbs/replays/.
	ReplayStore ReplayStore
	// SetReplayURL records an uploaded replay (db.DefaultDB.SetReplayURL when nil).
	SetReplayURL func(battleCode, url string) error

	statusMu   sync.Mutex
	statusAt   time.Time
	statusBody []byte
}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		glog.Warningln("lbs/replay: FindReplay failure", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if len(replays) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(replays); err != nil {
		glog.Warningln("lbs/replay: JSON encode failure", err)
	}
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
			glog.Warningln("lbs/status: JSON encode failure", err)
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
		list = Spectators.List
	}
	battles := list()
	if battles == nil {
		battles = []*LiveBattle{} // [], not null
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(battles); err != nil {
		glog.Warningln("lbs/live: JSON encode failure", err)
	}
}
