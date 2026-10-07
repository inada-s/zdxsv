package lobby

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testReplay(code string) []byte {
	return []byte("ZDXSV-REPLAY 1\nbattle_code=" + code + "\nuser_id=AAAAAA\nplayers=2\nstate_size=3\n\nxyz\x00\x01")
}

func TestReplayServer(t *testing.T) {
	dir := t.TempDir()
	s := &ReplayServer{Dir: dir, Known: func(code, user string) bool {
		return code == "1696492800000" && (user == "AAAAAA" || user == "BBBBBB")
	}}
	srv := httptest.NewServer(s)
	defer srv.Close()

	post := func(query string, body []byte) int {
		t.Helper()
		resp, err := http.Post(srv.URL+"/replay?"+query, "application/x-www-form-urlencoded", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	good := testReplay("1696492800000")
	for _, c := range []struct {
		name  string
		query string
		body  []byte
		want  int
	}{
		{"not a player", "battle_code=1696492800000&user_id=CCCCCC", good, http.StatusForbidden},
		{"unknown battle", "battle_code=1696492800001&user_id=AAAAAA", testReplay("1696492800001"), http.StatusForbidden},
		{"path in code", "battle_code=../x&user_id=AAAAAA", good, http.StatusBadRequest},
		{"no user", "battle_code=1696492800000", good, http.StatusBadRequest},
		{"not a replay", "battle_code=1696492800000&user_id=AAAAAA", []byte("hello\n\n"), http.StatusBadRequest},
		{"other battle's file", "battle_code=1696492800000&user_id=AAAAAA", testReplay("16964928000001"), http.StatusBadRequest},
		{"stored", "battle_code=1696492800000&user_id=AAAAAA", good, http.StatusOK},
		{"second upload", "battle_code=1696492800000&user_id=BBBBBB", testReplay("1696492800000"), http.StatusConflict},
	} {
		if got := post(c.query, c.body); got != c.want {
			t.Errorf("%s: status %d want %d", c.name, got, c.want)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) != 1 || filepath.Base(files[0]) != "1696492800000.zdxr" {
		t.Fatalf("files %v", files)
	}
	if b, _ := os.ReadFile(files[0]); !bytes.Equal(b, good) {
		t.Fatalf("stored %q", b)
	}

	resp, err := http.Get(srv.URL + "/replay/1696492800000.zdxr")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(b, good) {
		t.Fatalf("get: %d %q", resp.StatusCode, b)
	}
	for _, p := range []string{"/replay/1696492800001.zdxr", "/replay/..%2F..%2Fx.zdxr", "/replay/1696492800000.p2s"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("get %s: %d want 404", p, resp.StatusCode)
		}
	}
}

func TestReplayServerSizeLimit(t *testing.T) {
	s := &ReplayServer{Dir: t.TempDir(), Known: func(string, string) bool { return true }}
	body := append(testReplay("1"), make([]byte, MaxReplaySize)...)
	req := httptest.NewRequest(http.MethodPost, "/replay?battle_code=1&user_id=A", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", w.Code)
	}
}

func TestBattleInfoReplayUpload(t *testing.T) {
	defer func(u string) { ReplayUploadURL = u }(ReplayUploadURL)
	infos := map[string]map[string]string{"AAAAAA": {"udp": "1", "udp_addr": "203.0.113.5:40001", "ggpo": "7001"}}
	info := func(id string) map[string]string { return infos[id] }
	emu := map[string]string{"emulator": "pcsx2", "cpu": "x86/64", "udp": "1"}

	ReplayUploadURL = ""
	if n := battleInfoNotice(newUDPTestPeer(emu, false), info); strings.Contains(string(n.Body), "replay_upload") {
		t.Fatalf("server off: %q", n.Body)
	}
	ReplayUploadURL = "http://192.168.1.8:8204/replay"
	if n := battleInfoNotice(newUDPTestPeer(emu, false), info); !strings.HasSuffix(string(n.Body), "\nreplay_upload=http://192.168.1.8:8204/replay\n") {
		t.Fatalf("server on: %q", n.Body)
	}
	// no GGPO battle (no ggpo_ line): no replay is recorded, nothing offered
	if n := battleInfoNotice(newUDPTestPeer(emu, false), nil); strings.Contains(string(n.Body), "replay_upload") {
		t.Fatalf("no ggpo: %q", n.Body)
	}
}
