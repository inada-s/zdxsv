package lobby

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"zdxsv/pkg/db"

	"github.com/golang/glog"
)

// ReplayUploadURL is offered to players in the battle info (replay_upload=), "" when the
// replay server is off.
var ReplayUploadURL string

// MaxReplaySize bounds an upload (a 2-player battle's .zdxr is ~10 MB: a ~8 MB save state + inputs).
const MaxReplaySize = 64 << 20

var battleCodeRe = regexp.MustCompile(`^[0-9]{1,20}$`)

// ReplayServer stores pcsx2 replays (.zdxr) as <Dir>/<battle code>.zdxr.
//
//	POST /replay?battle_code=C&user_id=U, body = the .zdxr: 200 stored, 409 already
//	  stored (first upload wins: one file holds every player's input), 400 bad file,
//	  403 U has no record of battle C.
//	GET /replay/C.zdxr: the stored file.
type ReplayServer struct {
	Dir string
	// Known reports whether userID played battleCode.
	Known func(battleCode, userID string) bool
	mtx   sync.Mutex
}

// ServeReplay runs a ReplayServer for dir on addr and offers publicURL + "/replay" to players.
func ServeReplay(addr, dir, publicURL string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s := &ReplayServer{Dir: dir, Known: func(battleCode, userID string) bool {
		r, err := db.DefaultDB.GetBattleRecordUser(battleCode, userID)
		return err == nil && r != nil
	}}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	ReplayUploadURL = publicURL + "/replay"
	glog.Infoln("Start replay server", addr, "dir", dir, "public", ReplayUploadURL)
	return http.Serve(ln, s)
}

func (s *ReplayServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/replay" {
		s.upload(w, r)
		return
	}
	name, ok := strings.CutPrefix(r.URL.Path, "/replay/")
	code, zdxr := strings.CutSuffix(name, ".zdxr")
	if r.Method != http.MethodGet || !ok || !zdxr || !battleCodeRe.MatchString(code) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.Dir, code+".zdxr"))
}

func (s *ReplayServer) upload(w http.ResponseWriter, r *http.Request) {
	code, user := r.URL.Query().Get("battle_code"), r.URL.Query().Get("user_id")
	if !battleCodeRe.MatchString(code) || user == "" {
		http.Error(w, "bad battle_code or user_id", http.StatusBadRequest)
		return
	}
	if !s.Known(code, user) {
		glog.Infoln("replay upload: no battle record", code, user, r.RemoteAddr)
		http.Error(w, "unknown battle", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxReplaySize+1))
	if err != nil {
		http.Error(w, "read failed", http.StatusBadRequest)
		return
	}
	if len(body) > MaxReplaySize {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	// pcsx2 Zdxsv::ReplayWrite: "ZDXSV-REPLAY 1\n" + key=value lines (battle_code among them) + "\n" + data
	head, _, ok := bytes.Cut(body, []byte("\n\n"))
	if !ok || !bytes.HasPrefix(head, []byte("ZDXSV-REPLAY 1\n")) ||
		!bytes.Contains(append(head, '\n'), []byte("\nbattle_code="+code+"\n")) {
		http.Error(w, "not a replay of this battle", http.StatusBadRequest)
		return
	}

	s.mtx.Lock()
	defer s.mtx.Unlock()
	path := filepath.Join(s.Dir, code+".zdxr")
	if _, err := os.Stat(path); err == nil {
		http.Error(w, "already uploaded", http.StatusConflict)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		glog.Errorln("replay upload:", err)
		http.Error(w, "write failed", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		glog.Errorln("replay upload:", err)
		http.Error(w, "write failed", http.StatusInternalServerError)
		return
	}
	glog.Infoln("replay uploaded", code, "by", user, len(body), "bytes")
	w.WriteHeader(http.StatusOK)
}
