package lobby

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOpsReplayUploaded(t *testing.T) {
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zdxsv/replays/123.pb" {
			http.NotFound(w, r)
		}
	}))
	defer files.Close()

	stored := map[string]string{}
	h := &OpsHandler{
		ReplayURLPrefix: files.URL + "/zdxsv/",
		SetReplayURL:    func(c, u string) error { stored[c] = u; return nil },
	}
	get := func(code, u string) int {
		r := httptest.NewRequest("GET", "/ops/replay_uploaded?"+url.Values{"battle_code": {code}, "url": {u}}.Encode(), nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	good := files.URL + "/zdxsv/replays/123.pb"
	for _, c := range []struct {
		code, url string
		want      int
	}{
		{"", good, 400},
		{"123", "", 400},
		{"123", "https://example.com/zdxsv/replays/123.pb", 400}, // outside the prefix
		{"123", files.URL + "/zdxsv/replays/456.pb", 400},        // not stored (HEAD 404)
		{"123", good, 200},
	} {
		if got := get(c.code, c.url); got != c.want {
			t.Errorf("%q %q: status %d, want %d", c.code, c.url, got, c.want)
		}
	}
	if len(stored) != 1 || stored["123"] != good {
		t.Errorf("stored %v", stored)
	}

	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/ops/replay_uploaded?battle_code=123&url=" + url.QueryEscape(good))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || strings.TrimSpace(string(body)) != "OK" {
		t.Errorf("served: %d %q", resp.StatusCode, body)
	}
}
