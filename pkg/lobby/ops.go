package lobby

import (
	"net/http"
	"strings"

	"zdxsv/pkg/db"

	"github.com/golang/glog"
)

// OpsHandler serves the lobby's private API (as gdxsv lbs /ops/*).
//
//	GET /ops/replay_uploaded?battle_code=C&url=U: called by the replay uploader
//	  (infra/uploader) after it stored C's replay at U. U must start with
//	  ReplayURLPrefix and answer a HEAD with 200; then every record of C gets U.
type OpsHandler struct {
	ReplayURLPrefix string
	// Head checks an uploaded url (http.Head when nil).
	Head func(url string) (*http.Response, error)
	// SetReplayURL stores the url (db.DefaultDB.SetReplayURL when nil).
	SetReplayURL func(battleCode, url string) error
}

func (h *OpsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ops/replay_uploaded" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
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
