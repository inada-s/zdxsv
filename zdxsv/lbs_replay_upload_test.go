package main

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fields: form field name, value pairs written before the file.
func replayUploadRequest(t *testing.T, query, filename string, content []byte, fields ...string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i := 0; i+1 < len(fields); i += 2 {
		mw.WriteField(fields[i], fields[i+1])
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	req := httptest.NewRequest("POST", "/lbs/replay_upload?"+query, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestAPIReplayUpload(t *testing.T) {
	UseTestDB(t)
	for _, r := range []*BattleRecord{
		{BattleCode: "C1234", UserID: "u1", Players: 2, Pos: 1, GgpoSession: 0x12345678},
		{BattleCode: "C1234", UserID: "u2", Players: 2, Pos: 2, GgpoSession: 0x12345678},
		{BattleCode: "C5678", UserID: "u1", Players: 2, Pos: 1, GgpoSession: 0xF0000001},
		{BattleCode: "C0", UserID: "u1", Players: 2, Pos: 1},
	} {
		if err := DefaultDB.AddBattleRecord(r); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	h := &APIHandler{ReplayStore: &DirReplayStore{Dir: dir, URL: "http://127.0.0.1:9881/lbs/"}}
	do := func(req *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	replayURL := func(code string) string {
		rs, err := DefaultDB.GetBattleRecords(code)
		if err != nil || len(rs) == 0 {
			t.Fatalf("records of %s: %v", code, err)
		}
		return rs[len(rs)-1].ReplayURL
	}
	const ok = "battle_code=C1234&session_id=305419896"

	for _, c := range []struct {
		query, name string
		code        int
	}{
		{"battle_code=C1234", "C1234.pb", http.StatusForbidden},
		{"battle_code=C1234&session_id=305419897", "C1234.pb", http.StatusForbidden},
		{"battle_code=C1234&session_id=x", "C1234.pb", http.StatusForbidden},
		{"battle_code=C9999&session_id=305419896", "C9999.pb", http.StatusForbidden},
		{"battle_code=C0&session_id=0", "C0.pb", http.StatusForbidden},
		{"session_id=305419896", "C1234.pb", http.StatusBadRequest},
		{"battle_code=../C1234&session_id=305419896", "C1234.pb", http.StatusBadRequest},
		{ok, "C5678.pb", http.StatusBadRequest},
		{ok, "C1234.txt", http.StatusBadRequest},
	} {
		if w := do(replayUploadRequest(t, c.query, c.name, []byte("bad"))); w.Code != c.code {
			t.Errorf("%s %s: code %d, want %d", c.query, c.name, w.Code, c.code)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 || replayURL("C1234") != "" {
		t.Fatalf("a refused upload was stored: %v %q", entries, replayURL("C1234"))
	}

	if w := do(replayUploadRequest(t, ok, "C1234.pb", []byte("replay-bytes"), "used_ms", "[[3,7]]")); w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "replays", "C1234.pb")); string(b) != "replay-bytes" {
		t.Errorf("stored %q", b)
	}
	if u := replayURL("C1234"); u != "http://127.0.0.1:9881/lbs/replays/C1234.pb" {
		t.Errorf("recorded %q", u)
	}
	if rs, _ := DefaultDB.GetBattleRecords("C1234"); rs[0].UsedMsList != "3" || rs[1].UsedMsList != "7" {
		t.Errorf("used_ms %q %q", rs[0].UsedMsList, rs[1].UsedMsList)
	}
	// the stored file is served at the recorded url's path
	if w := do(httptest.NewRequest("GET", "/lbs/replays/C1234.pb", nil)); w.Code != 200 || w.Body.String() != "replay-bytes" {
		t.Errorf("GET stored: code %d body %q", w.Code, w.Body.String())
	}

	// the same replay again: 409, kept as first stored (the client treats it as success)
	if w := do(replayUploadRequest(t, ok, "C1234.pb", []byte("other"))); w.Code != http.StatusConflict {
		t.Errorf("second upload code %d", w.Code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "replays", "C1234.pb")); string(b) != "replay-bytes" {
		t.Errorf("stored after the second upload %q", b)
	}

	// a session above 2^31: as unsigned or as the signed value the client sends
	for _, s := range []string{"-268435455", "4026531841"} {
		os.RemoveAll(filepath.Join(dir, "replays"))
		DefaultDB.SetReplayURL("C5678", "")
		if w := do(replayUploadRequest(t, "battle_code=C5678&session_id="+s, "C5678.pb", []byte("y"))); w.Code != 200 || replayURL("C5678") == "" {
			t.Errorf("session %s: code %d %s", s, w.Code, w.Body.String())
		}
	}

	if w := do(httptest.NewRequest("GET", "/lbs/replay_upload", nil)); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET code %d", w.Code)
	}
	DefaultDB.SetReplayURL("C1234", "")
	if w := do(httptest.NewRequest("POST", "/lbs/replay_upload?"+ok, bytes.NewReader(nil))); w.Code != http.StatusBadRequest {
		t.Errorf("no form code %d", w.Code)
	}

	off := &APIHandler{}
	if w := httptest.NewRecorder(); true {
		off.ServeHTTP(w, replayUploadRequest(t, ok, "C1234.pb", []byte("x")))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("disabled code %d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	off.ServeHTTP(w, httptest.NewRequest("GET", "/lbs/replays/C1234.pb", nil))
	if b, _ := io.ReadAll(w.Body); w.Code != http.StatusNotFound {
		t.Errorf("no dir store: code %d body %q", w.Code, b)
	}
}
