package function

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func post(t *testing.T, name string, data []byte) int {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data)
	mw.Close()
	r := httptest.NewRequest("POST", "/", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	uploadHandler(w, r)
	return w.Code
}

func TestUploadLocal(t *testing.T) {
	var notices []string
	lobby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notices = append(notices, r.URL.Query().Get("battle_code")+" "+r.URL.Query().Get("url"))
	}))
	defer lobby.Close()
	localDir, localURL, lobbyURL = t.TempDir(), "http://files", lobby.URL+"/ops/replay_uploaded"
	defer func() { localDir, localURL, lobbyURL = "", "", "" }()

	// Larger than maxMemory: the form keeps the rest in a temporary file.
	big := bytes.Repeat([]byte{1, 2, 3, 4}, 3<<20)
	for _, c := range []struct {
		name string
		data []byte
		want int
	}{
		{"123.pb", big, 200},
		{"123.pb", []byte("second"), 409},
		{"123.zdxr", big, 400},
		{".pb", big, 400},
		{"1.2.pb", big, 400},
	} {
		if got := post(t, c.name, c.data); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
	stored, err := os.ReadFile(filepath.Join(localDir, "replays", "123.pb"))
	if err != nil || !bytes.Equal(stored, big) {
		t.Errorf("stored %d bytes (%v), want %d", len(stored), err, len(big))
	}
	want := "123 http://files/replays/123.pb"
	if len(notices) != 2 || notices[0] != want || notices[1] != want {
		t.Errorf("lobby notices %q", notices)
	}
}
