package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zdxsv/pkg/assets"
)

func TestLoginHandlerAssetsAndHealth(t *testing.T) {
	srv := httptest.NewServer(newLoginHandler())
	t.Cleanup(srv.Close)

	get := func(path string, header map[string]string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest("GET", srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res, body
	}

	res, body := get("/health", nil)
	if res.StatusCode != 200 || string(body) != "works\n" {
		t.Errorf("/health: %d %q", res.StatusCode, body)
	}

	for _, c := range []struct{ name, contentType string }{
		{"assets/css/bootstrap.min.css", "text/css; charset=utf-8"},
		{"assets/login.png", "image/png"},
		{"assets/checkfile", ""},
	} {
		want, err := assets.Asset(c.name)
		if err != nil {
			t.Fatal(err)
		}
		res, body := get("/"+c.name, nil)
		if res.StatusCode != 200 || !bytes.Equal(body, want) {
			t.Errorf("%s: %d, %d bytes, want %d", c.name, res.StatusCode, len(body), len(want))
		}
		if c.contentType != "" && res.Header.Get("Content-Type") != c.contentType {
			t.Errorf("%s: Content-Type %q", c.name, res.Header.Get("Content-Type"))
		}
		modified, err := time.Parse(http.TimeFormat, res.Header.Get("Last-Modified"))
		if err != nil || modified.After(time.Now()) {
			t.Errorf("%s: Last-Modified %q", c.name, res.Header.Get("Last-Modified"))
		}
		if _, err := time.Parse(http.TimeFormat, res.Header.Get("Expires")); err != nil {
			t.Errorf("%s: Expires %q", c.name, res.Header.Get("Expires"))
		}

		// Any valid If-Modified-Since is answered from the client's cache.
		res, body = get("/"+c.name, map[string]string{"If-Modified-Since": modified.Format(http.TimeFormat)})
		if res.StatusCode != http.StatusNotModified || len(body) != 0 {
			t.Errorf("%s revalidated: %d, %d bytes", c.name, res.StatusCode, len(body))
		}
	}

	if res, _ := get("/assets/missing.png", nil); res.StatusCode != 404 {
		t.Errorf("missing asset: %d", res.StatusCode)
	}
	if res, _ := get("/unknown", nil); res.StatusCode != 404 {
		t.Errorf("unknown path: %d", res.StatusCode)
	}
}
