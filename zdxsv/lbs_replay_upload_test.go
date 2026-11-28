package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
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

// testReplay: battle_code first, as a replay starts, then a varint, a fixed32 and rounds round_data entries.
func testReplay(code string, rounds int) []byte {
	b := append([]byte{0x1a, byte(len(code))}, code...)
	b = append(b, 0x20, 0x96, 0x01, 0x2d, 1, 2, 3, 4)
	for range rounds {
		b = append(b, 0x92, 0x01, 2, 0x08, 0x01)
	}
	return b
}

func gzipped(b []byte) []byte {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(b)
	zw.Close()
	return gz.Bytes()
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

	pb := testReplay("C1234", 1)
	huge := append(testReplay("C1234", 0), 0x3a)
	huge = binary.AppendUvarint(huge, replayUploadMaxSize)
	huge = append(huge, make([]byte, replayUploadMaxSize)...)
	for name, body := range map[string][]byte{
		"not a pb":           []byte("replay-bytes"),
		"other battle":       testReplay("C5678", 1),
		"truncated field":    append(testReplay("C1234", 1), 0x3a, 0x05, 'a'),
		"truncated gzip":     gzipped(pb)[:len(gzipped(pb))-4],
		"gzip trailing junk": append(gzipped(pb), 'x', 'x'),
		"decoded over limit": gzipped(huge),
	} {
		if w := do(replayUploadRequest(t, ok, "C1234.pb", body, "used_ms", "[[3,7]]")); w.Code != http.StatusBadRequest {
			t.Errorf("%s: code %d %s", name, w.Code, w.Body.String())
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "replays")); len(entries) != 0 || replayURL("C1234") != "" {
		t.Fatalf("an invalid replay was stored: %v %q", entries, replayURL("C1234"))
	}

	if w := do(replayUploadRequest(t, ok, "C1234.pb", pb, "used_ms", "[[3,7]]")); w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "replays", "C1234.pb")); !bytes.Equal(b, pb) {
		t.Errorf("stored %q", b)
	}
	if u := replayURL("C1234"); u != "http://127.0.0.1:9881/lbs/replays/C1234.pb" {
		t.Errorf("recorded %q", u)
	}
	if rs, _ := DefaultDB.GetBattleRecords("C1234"); rs[0].UsedMsList != "3" || rs[1].UsedMsList != "7" {
		t.Errorf("used_ms %q %q", rs[0].UsedMsList, rs[1].UsedMsList)
	}
	// the stored file is served at the recorded url's path
	if w := do(httptest.NewRequest("GET", "/lbs/replays/C1234.pb", nil)); w.Code != 200 || w.Body.String() != string(pb) {
		t.Errorf("GET stored: code %d body %q", w.Code, w.Body.String())
	}

	// the same replay again: 409, kept as first stored (the client treats it as success)
	if w := do(replayUploadRequest(t, ok, "C1234.pb", testReplay("C1234", 0))); w.Code != http.StatusConflict {
		t.Errorf("second upload code %d", w.Code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "replays", "C1234.pb")); !bytes.Equal(b, pb) {
		t.Errorf("stored after the second upload %q", b)
	}

	// a session above 2^31: as unsigned or as the signed value the client sends
	for _, s := range []string{"-268435455", "4026531841"} {
		os.RemoveAll(filepath.Join(dir, "replays"))
		DefaultDB.SetReplayURL("C5678", "")
		if w := do(replayUploadRequest(t, "battle_code=C5678&session_id="+s, "C5678.pb", testReplay("C5678", 0))); w.Code != 200 || replayURL("C5678") == "" {
			t.Errorf("session %s: code %d %s", s, w.Code, w.Body.String())
		}
	}

	// stored but its url not recorded: a retry records it (used_ms of more rounds than the replay is dropped)
	os.RemoveAll(filepath.Join(dir, "replays"))
	DefaultDB.SetReplayURL("C5678", "")
	const ok2 = "battle_code=C5678&session_id=4026531841"
	h.SetReplayURL = func(string, string) error { return errors.New("db down") }
	if w := do(replayUploadRequest(t, ok2, "C5678.pb", testReplay("C5678", 1))); w.Code != http.StatusInternalServerError {
		t.Errorf("SetReplayURL failure: code %d", w.Code)
	}
	h.SetReplayURL = nil
	if w := do(replayUploadRequest(t, ok2, "C5678.pb", testReplay("C5678", 1), "used_ms", "[[3],[4]]")); w.Code != 200 || replayURL("C5678") == "" {
		t.Errorf("retry after a SetReplayURL failure: code %d %s, url %q", w.Code, w.Body.String(), replayURL("C5678"))
	}
	if rs, _ := DefaultDB.GetBattleRecords("C5678"); rs[0].UsedMsList != "" {
		t.Errorf("used_ms of 2 rounds for a 1 round replay saved as %q", rs[0].UsedMsList)
	}
	if w := do(replayUploadRequest(t, ok2, "C5678.pb", testReplay("C5678", 1))); w.Code != http.StatusConflict {
		t.Errorf("upload after the retry: code %d", w.Code)
	}

	// used_ms of more rounds than a series can have is dropped, however many round_data the replay has
	for rounds, want := range map[int]string{10: "3,3,3,3,3,3,3,3,3,3", 11: ""} {
		os.RemoveAll(filepath.Join(dir, "replays"))
		DefaultDB.SetReplayURL("C5678", "")
		DefaultDB.SetUsedMs("C5678", "u1", 0, "")
		usedMs := "[[3]"
		for range rounds - 1 {
			usedMs += ",[3]"
		}
		if w := do(replayUploadRequest(t, ok2, "C5678.pb", testReplay("C5678", 1000), "used_ms", usedMs+"]")); w.Code != 200 {
			t.Errorf("used_ms of %d rounds: code %d %s", rounds, w.Code, w.Body.String())
		}
		if rs, _ := DefaultDB.GetBattleRecords("C5678"); rs[0].UsedMsList != want {
			t.Errorf("used_ms of %d rounds saved as %q", rounds, rs[0].UsedMsList)
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

func TestCopyGzip(t *testing.T) {
	pb := bytes.Repeat([]byte{0x2a, 0x09, 'z', 'd', 'x', 's', 'v', '-', 'p', 's', '2'}, 1000)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(pb)
	zw.Close()

	var plain bytes.Buffer
	if err := copyGzip(&plain, bytes.NewReader(pb)); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(&plain)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(zr); err != nil || !bytes.Equal(got, pb) {
		t.Errorf("a plain file stored as %d bytes that do not decode to it (%v)", len(got), err)
	}

	var kept bytes.Buffer
	if err := copyGzip(&kept, bytes.NewReader(gz.Bytes())); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept.Bytes(), gz.Bytes()) {
		t.Errorf("a gzip file stored as %d bytes, want it unchanged (%d bytes)", kept.Len(), gz.Len())
	}
}

func TestDirReplayStoreGzip(t *testing.T) {
	pb := bytes.Repeat([]byte{0x2a, 0x09, 'z', 'd', 'x', 's', 'v'}, 1000)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(pb)
	zw.Close()
	s := &DirReplayStore{Dir: t.TempDir(), URL: "http://x/"}
	for _, in := range []struct {
		name string
		data []byte
	}{{"plain.pb", pb}, {"gzip.pb", gz.Bytes()}} {
		if _, err := s.Put(context.Background(), in.name, bytes.NewReader(in.data)); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Join(s.Dir, in.name)); err != nil || !bytes.Equal(got, pb) {
			t.Errorf("%s stored as %d bytes, want the plain file (%v)", in.name, len(got), err)
		}
	}
}
