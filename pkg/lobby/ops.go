package lobby

import (
	"encoding/json"
	"net/http"
	"strings"

	"zdxsv/pkg/db"

	"github.com/golang/glog"
)

// OpsHandler serves the lobby's private API.
//
//	GET /ops/replay_uploaded?battle_code=C&url=U: called by the replay uploader
//	  after it stored C's replay at U. U must start with
//	  ReplayURLPrefix and answer a HEAD with 200; then every record of C gets U.
//	GET /ops/ban?kind=K&value=V&reason=R: refuses the lobby login of the user V
//	  (K=user, every account holding it), the machine id V (K=machine) or the ip V (K=ip).
//	GET /ops/unban?kind=K&value=V: removes that ban, 404 if none.
//	GET /ops/bans: the bans as JSON.
//	GET /ops/accounts?machine_id=M: the accounts last logged in from M and their users as JSON.
type OpsHandler struct {
	ReplayURLPrefix string
	// Head checks an uploaded url (http.Head when nil).
	Head func(url string) (*http.Response, error)
	// SetReplayURL stores the url (db.DefaultDB.SetReplayURL when nil).
	SetReplayURL func(battleCode, url string) error
}

func (h *OpsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	switch r.URL.Path {
	case "/ops/replay_uploaded":
		h.serveReplayUploaded(w, r)
	case "/ops/ban":
		serveBan(w, r)
	case "/ops/unban":
		serveUnban(w, r)
	case "/ops/bans":
		serveBans(w)
	case "/ops/accounts":
		serveAccounts(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *OpsHandler) serveReplayUploaded(w http.ResponseWriter, r *http.Request) {
	battleCode, url := r.FormValue("battle_code"), r.FormValue("url")
	if battleCode == "" || url == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	if h.ReplayURLPrefix == "" || !strings.HasPrefix(url, h.ReplayURLPrefix) {
		glog.Warningln("replay_uploaded: invalid url", url)
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	head, set := h.Head, h.SetReplayURL
	if head == nil {
		head = http.Head
	}
	if set == nil {
		set = db.DefaultDB.SetReplayURL
	}
	resp, err := head(url)
	if err != nil {
		glog.Warningln("replay_uploaded: head failure", err)
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		glog.Warningln("replay_uploaded: head status", resp.StatusCode, url)
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	if err := set(battleCode, url); err != nil {
		glog.Warningln("replay_uploaded: SetReplayURL failure", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	glog.Infoln("replay uploaded", battleCode, url)
	w.Write([]byte("OK"))
}

func banKind(kind string) bool {
	return kind == db.BanUser || kind == db.BanMachine || kind == db.BanIP
}

func serveBan(w http.ResponseWriter, r *http.Request) {
	ban := &db.Ban{Kind: r.FormValue("kind"), Value: r.FormValue("value"), Reason: r.FormValue("reason")}
	if !banKind(ban.Kind) || ban.Value == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	if err := db.DefaultDB.AddBan(ban); err != nil {
		glog.Warningln("ban: AddBan failure", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	glog.Infoln("ban added", ban.Kind, ban.Value, ban.Reason)
	w.Write([]byte("OK"))
}

func serveUnban(w http.ResponseWriter, r *http.Request) {
	kind, value := r.FormValue("kind"), r.FormValue("value")
	if !banKind(kind) || value == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	ok, err := db.DefaultDB.DeleteBan(kind, value)
	if err != nil {
		glog.Warningln("unban: DeleteBan failure", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	glog.Infoln("ban removed", kind, value)
	w.Write([]byte("OK"))
}

func serveBans(w http.ResponseWriter) {
	bans, err := db.DefaultDB.GetBanList()
	if err != nil {
		glog.Warningln("bans: GetBanList failure", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	writeJSON(w, bans)
}

type opsAccount struct {
	*db.Account
	Users []*db.User `json:"users"`
}

func serveAccounts(w http.ResponseWriter, r *http.Request) {
	machineID := r.FormValue("machine_id")
	if machineID == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	accounts, err := db.DefaultDB.GetAccountsByMachineID(machineID)
	if err != nil {
		glog.Warningln("accounts: GetAccountsByMachineID failure", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	ret := []*opsAccount{}
	for _, ac := range accounts {
		users, err := db.DefaultDB.GetUserList(ac.LoginKey)
		if err != nil {
			glog.Warningln("accounts: GetUserList failure", err)
			http.Error(w, "", http.StatusInternalServerError)
			return
		}
		ac.SessionID = ""
		ret = append(ret, &opsAccount{Account: ac, Users: users})
	}
	writeJSON(w, ret)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		glog.Warningln("ops: encode failure", err)
	}
}
