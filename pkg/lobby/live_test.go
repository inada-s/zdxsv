package lobby

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLive(t *testing.T) {
	s := &ReplayServer{Dir: t.TempDir(), Known: func(code, user string) bool {
		return code == "1696492800000" && (user == "AAAAAA" || user == "BBBBBB")
	}}
	srv := httptest.NewServer(s)
	defer srv.Close()

	req := func(method, path string, body []byte) (int, string) {
		t.Helper()
		r, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	check := func(method, path string, body []byte, wantCode int, wantBody string) {
		t.Helper()
		code, got := req(method, path, body)
		if code != wantCode || (wantBody != "" && got != wantBody) {
			t.Errorf("%s %s: %d %q, want %d %q", method, path, code, got, wantCode, wantBody)
		}
	}
	const q = "?battle_code=1696492800000&user_id="
	// 2 players, 2-byte inputs: 4 bytes a frame
	start := []byte("ZDXSV-REPLAY 1\nbattle_code=1696492800000\nplayers=2\ninput_size=2\nstate_size=3\n\nxyz")

	check("POST", "/live/inputs"+q+"AAAAAA&from=0", []byte("0000"), http.StatusConflict, "")
	check("POST", "/live/start"+q+"CCCCCC", start, http.StatusForbidden, "")
	check("POST", "/live/start?battle_code=1&user_id=AAAAAA", start, http.StatusForbidden, "")
	check("POST", "/live/start"+q+"AAAAAA", bytes.Replace(start, []byte("state_size=3"), []byte("state_size=4"), 1), http.StatusBadRequest, "")
	check("POST", "/live/start"+q+"AAAAAA", bytes.Replace(start, []byte("input_size=2\n"), nil, 1), http.StatusBadRequest, "")
	check("GET", "/live", nil, http.StatusOK, "")
	check("POST", "/live/start"+q+"AAAAAA", start, http.StatusOK, "")
	// a later start keeps the first one's state
	check("POST", "/live/start"+q+"BBBBBB", bytes.Replace(start, []byte("xyz"), []byte("XYZ"), 1), http.StatusOK, "")
	check("GET", "/live/1696492800000", nil, http.StatusOK, string(start))
	check("GET", "/live", nil, http.StatusOK, "1696492800000 0 -\n")

	check("POST", "/live/inputs"+q+"AAAAAA&from=0", []byte("000"), http.StatusBadRequest, "")
	check("POST", "/live/inputs"+q+"AAAAAA&from=0", []byte("0000"+"1111"), http.StatusOK, "2")
	// the other player's copy of frames 1..2: frame 1 dropped, frame 2 appended
	check("POST", "/live/inputs"+q+"BBBBBB&from=1", []byte("1111"+"2222"), http.StatusOK, "3")
	// a gap is not filled: the reply says where to resend from
	check("POST", "/live/inputs"+q+"AAAAAA&from=5", []byte("5555"), http.StatusOK, "3")
	check("GET", "/live/1696492800000/inputs?from=1", nil, http.StatusOK, "frames=3 close=\n"+"1111"+"2222")
	check("GET", "/live/1696492800000/inputs?from=9", nil, http.StatusOK, "frames=3 close=\n")
	// a close from a sender that lacks the newest frames does not end the stream
	check("POST", "/live/inputs"+q+"BBBBBB&from=0&close=end", []byte("0000"), http.StatusOK, "3")
	check("GET", "/live", nil, http.StatusOK, "1696492800000 3 -\n")
	check("POST", "/live/inputs"+q+"AAAAAA&from=3&close=disconnect+2", []byte("3333"), http.StatusOK, "4")
	check("GET", "/live/1696492800000/inputs?from=3", nil, http.StatusOK, "frames=4 close=disconnect_2\n"+"3333")
	check("GET", "/live", nil, http.StatusOK, "1696492800000 4 disconnect_2\n")
	check("GET", "/live/123/inputs?from=0", nil, http.StatusNotFound, "")
	if code, _ := req("GET", "/replay/1696492800000.zdxr", nil); code != http.StatusNotFound {
		t.Errorf("live stream stored as a replay: %d", code)
	}
}
