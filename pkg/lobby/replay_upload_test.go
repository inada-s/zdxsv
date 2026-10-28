package lobby

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

func replayUploadRequest(t *testing.T, filename string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	req := httptest.NewRequest("POST", "/lbs/replay_upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestAPIReplayUpload(t *testing.T) {
	dir := t.TempDir()
	recorded := map[string]string{}
	h := &APIHandler{
		ReplayStore:  &DirReplayStore{Dir: dir, URL: "http://127.0.0.1:9881/lbs/"},
		SetReplayURL: func(c, u string) error { recorded[c] = u; return nil },
	}
	do := func(req *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	if w := do(replayUploadRequest(t, "C1234.pb", []byte("replay-bytes"))); w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "replays", "C1234.pb")); string(b) != "replay-bytes" {
		t.Errorf("stored %q", b)
	}
	if recorded["C1234"] != "http://127.0.0.1:9881/lbs/replays/C1234.pb" {
		t.Errorf("recorded %v", recorded)
	}
	// the stored file is served at the recorded url's path
	if w := do(httptest.NewRequest("GET", "/lbs/replays/C1234.pb", nil)); w.Code != 200 || w.Body.String() != "replay-bytes" {
		t.Errorf("GET stored: code %d body %q", w.Code, w.Body.String())
	}

	// the same replay again: 409, kept as first stored (the client treats it as success)
	if w := do(replayUploadRequest(t, "C1234.pb", []byte("other"))); w.Code != http.StatusConflict {
		t.Errorf("second upload code %d", w.Code)
	}
	for _, name := range []string{"C1234.txt", ".pb"} {
		if w := do(replayUploadRequest(t, name, []byte("x"))); w.Code != http.StatusBadRequest {
			t.Errorf("%q: code %d", name, w.Code)
		}
	}
	// a path in the file name: Go keeps only its base name
	if w := do(replayUploadRequest(t, "../a/b.pb", []byte("y"))); w.Code != 200 || recorded["b"] == "" {
		t.Errorf("path name: code %d recorded %v", w.Code, recorded)
	}
	if w := do(httptest.NewRequest("GET", "/lbs/replay_upload", nil)); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET code %d", w.Code)
	}
	if w := do(httptest.NewRequest("POST", "/lbs/replay_upload", bytes.NewReader(nil))); w.Code != http.StatusBadRequest {
		t.Errorf("no form code %d", w.Code)
	}

	off := &APIHandler{}
	if w := httptest.NewRecorder(); true {
		off.ServeHTTP(w, replayUploadRequest(t, "C1.pb", []byte("x")))
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
