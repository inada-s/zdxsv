package auth

import (
	"html/template"
	"net/http"
	"net/url"
)

// DevProvider signs in without an outside provider: /auth/dev/authorize asks the browser for a user
// ID and passes it back as the code. For local development only; its identities are stored under the
// provider name "dev", so they never stand for Discord users.
func DevProvider() Provider {
	return Provider{Name: "dev", AuthURL: "/auth/dev/authorize", dev: true}
}

func validDevUserID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

var tplDevAuthorize = template.Must(template.New("dev").Parse(`<!DOCTYPE html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>zdxsv dev sign-in</title>
<style>body{font-family:sans-serif;max-width:36em;margin:3em auto;padding:0 1em;line-height:1.6}</style>
</head><body><h1>zdxsv dev sign-in</h1>
<p>Local development provider. A user ID stands for one provider user; the first sign-in of an ID creates its account.</p>
<form method="get" action="{{.Callback}}"><input type="hidden" name="state" value="{{.State}}">
<p><label>User ID <input name="code" value="1" pattern="[0-9A-Za-z_\-]{1,32}" required autofocus></label>
<button type="submit">Sign in</button></p></form>
<p><a href="{{.Deny}}">Deny</a></p></body></html>`))

func (s *Server) serveDevAuthorize(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" || len(state) > 128 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	tplDevAuthorize.Execute(w, struct{ Callback, State, Deny string }{
		Callback: s.redirectURI(),
		State:    state,
		Deny:     s.redirectURI() + "?" + url.Values{"error": {"access_denied"}, "state": {state}}.Encode(),
	})
}
