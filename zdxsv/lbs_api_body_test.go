package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPIBodyLimit(t *testing.T) {
	srv := httptest.NewServer(&APIHandler{Auth: &AuthServer{Provider: DevProvider()}})
	defer srv.Close()
	big := "token=" + strings.Repeat("a", apiFormBodyMax)
	post := func(path string, body io.Reader) int {
		t.Helper()
		res, err := http.Post(srv.URL+path, "application/x-www-form-urlencoded", body)
		if err != nil {
			t.Fatal(path, err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for _, path := range []string{"/auth/token", "/auth/revoke", "/auth/accounts", "/auth/accounts/carry", "/lbs/login", "/lbs/replay_played"} {
		if code := post(path, strings.NewReader(big)); code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: %d with Content-Length, want 413", path, code)
		}
		if code := post(path, io.MultiReader(strings.NewReader(big))); code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: %d chunked, want 413", path, code)
		}
	}
	if code := post("/lbs/login", strings.NewReader("account=1")); code != http.StatusBadRequest {
		t.Errorf("/lbs/login without token: %d, want 400", code)
	}
	if code := post("/lbs/replay_upload", strings.NewReader(big)); code != http.StatusServiceUnavailable {
		t.Errorf("/lbs/replay_upload: %d, want its own 503", code)
	}
}

func TestAPIBodyReadTimeout(t *testing.T) {
	defer func(d time.Duration) { apiReadTimeout = d }(apiReadTimeout)
	apiReadTimeout = 100 * time.Millisecond
	srv := httptest.NewServer(&APIHandler{})
	defer srv.Close()
	c, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "POST /lbs/login HTTP/1.1\r\nHost: x\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 100\r\n\r\ntoken=")
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("connection not closed by the server: %v", err)
	}
	if !strings.HasPrefix(string(b), "HTTP/1.1 400") {
		t.Fatalf("answer %q, want 400", b)
	}
}
