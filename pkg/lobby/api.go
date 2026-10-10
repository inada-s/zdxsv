package lobby

import (
	"encoding/json"
	"net/http"
	"strconv"

	"zdxsv/pkg/db"

	"github.com/golang/glog"
)

const (
	maxReplayFilterBytes   = 64
	maxReplayPlayerFilters = 8
)

// APIHandler serves the lobby's public API (as gdxsv lbs /lbs/*).
//
//	GET /lbs/replay?battle_code=C&user_id=U&user_name=N&pilot_name=P&players=4&aggregate=1&reverse=1&page=0:
//	  the battles with an uploaded replay, newest first, 100 per page, as JSON (db.FoundReplay);
//	  204 when none. user_id, user_name and pilot_name may repeat; names are LIKE patterns.
//	GET /lbs/live: the battles streamed to live spectators (LiveBattle, running first), [] when none;
//	  a spectator watches one at udp://<lobby host>:<STUN port>/<battle_code>.
type APIHandler struct {
	// FindReplay searches (db.DefaultDB.FindReplay when nil).
	FindReplay func(q *db.FindReplayQuery) ([]*db.FoundReplay, error)
	// LiveList lists the live battles (Spectators.List when nil).
	LiveList func() []*LiveBattle
}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/lbs/live" {
		h.serveLive(w)
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
