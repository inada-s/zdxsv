package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLoginPageGameCode(t *testing.T) {
	UseTestDB(t)
	PrepareLoginPages()
	a, err := DefaultDB.RegisterAccount("192.0.2.1:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{gameCodeZGVSZ, gameCodeAEUG} {
		mux := http.NewServeMux()
		mux.Handle("/"+code+"/", http.StripPrefix("/"+code, withGameCode(code, newLoginHandler())))
		ts := httptest.NewServer(mux)
		_, _, body := fetch(t)(http.PostForm(ts.URL+"/"+code+"/login", url.Values{"login_key": {a.LoginKey}}))
		ts.Close()
		for _, want := range []string{"https://www01.kddi-mmbb.jp/" + code + "/login", "https://www01.kddi-mmbb.jp/" + code + "/CRS-top.jsp"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: login page lacks %s: %.300q", code, want, body)
			}
		}
	}
}
