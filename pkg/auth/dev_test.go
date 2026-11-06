package auth

import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"

	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
)

var (
	devFormRe  = regexp.MustCompile(`action="([^"]+)"`)
	devStateRe = regexp.MustCompile(`name="state" value="([^"]+)"`)
	devDenyRe  = regexp.MustCompile(`<a href="([^"]+)">Deny`)
)

func newDevSite(t *testing.T) (*httptest.Server, *http.Client) {
	dbtest.Use(t)
	s := &Server{Provider: DevProvider(), Store: db.DefaultDB}
	site := httptest.NewServer(s)
	t.Cleanup(site.Close)
	s.PublicURL = site.URL
	return site, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func get(t *testing.T, c *http.Client, u string) (*http.Response, string) {
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

// devAuthorizePage follows the start page's button and returns the dev page.
func devAuthorizePage(t *testing.T, site *httptest.Server, c *http.Client) string {
	q := url.Values{"redirect_port": {"50123"}, "state": {"st"}, "code_challenge": {challenge(verifier)}, "lang": {"en"}}
	_, body := get(t, c, site.URL+"/auth/start?"+q.Encode())
	m := buttonRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("start page", body)
	}
	resp, body := get(t, c, site.URL+html.UnescapeString(m[1]))
	if resp.StatusCode != 200 {
		t.Fatal("dev authorize", resp.StatusCode, body)
	}
	return body
}

func TestDevSignIn(t *testing.T) {
	site, c := newDevSite(t)
	body := devAuthorizePage(t, site, c)
	form, state := devFormRe.FindStringSubmatch(body), devStateRe.FindStringSubmatch(body)
	if form == nil || state == nil || html.UnescapeString(form[1]) != site.URL+"/auth/dev/callback" {
		t.Fatal("dev form", body)
	}
	resp, _ := get(t, c, form[1]+"?"+url.Values{"code": {"alice"}, "state": {html.UnescapeString(state[1])}}.Encode())
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || loc.Host != "127.0.0.1:50123" || loc.Query().Get("state") != "st" {
		t.Fatal("emulator redirect", resp.StatusCode, loc)
	}
	if key, err := db.DefaultDB.IdentityLoginKey("dev", "alice"); err != nil || key == "" {
		t.Fatal("dev identity", key, err)
	}
	if key, _ := db.DefaultDB.IdentityLoginKey("discord", "alice"); key != "" {
		t.Fatal("dev sign-in made a discord identity")
	}
}

func TestDevSignInDenyAndBadID(t *testing.T) {
	site, c := newDevSite(t)
	deny := devDenyRe.FindStringSubmatch(devAuthorizePage(t, site, c))
	if deny == nil {
		t.Fatal("no deny link")
	}
	if resp, _ := get(t, c, html.UnescapeString(deny[1])); resp.StatusCode != http.StatusForbidden {
		t.Fatal("deny", resp.StatusCode)
	}
	state := devStateRe.FindStringSubmatch(devAuthorizePage(t, site, c))
	bad := url.Values{"code": {"a b"}, "state": {html.UnescapeString(state[1])}}
	if resp, _ := get(t, c, site.URL+"/auth/dev/callback?"+bad.Encode()); resp.StatusCode != http.StatusForbidden {
		t.Fatal("bad user id", resp.StatusCode)
	}
}

func TestDevAuthorizeOffForDiscord(t *testing.T) {
	r := newRig(t)
	if resp, _ := get(t, http.DefaultClient, r.site.URL+"/auth/dev/authorize?state=x"); resp.StatusCode != http.StatusNotFound {
		t.Fatal("dev page served by the discord provider", resp.StatusCode)
	}
}
