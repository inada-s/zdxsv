package lobby

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"zdxsv/pkg/db"
)

func TestAPIReplay(t *testing.T) {
	var got *db.FindReplayQuery
	var result []*db.FoundReplay
	var fail error
	h := &APIHandler{FindReplay: func(q *db.FindReplayQuery) ([]*db.FoundReplay, error) {
		got = q
		return result, fail
	}}
	get := func(query string) *httptest.ResponseRecorder {
		got = nil
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/lbs/replay?"+query, nil))
		return w
	}

	result = []*db.FoundReplay{{BattleCode: "123", ReplayURL: "https://example.com/123.pb", Users: []*db.ReplayUser{{UserID: "u1"}}}}
	w := get("battle_code=123&user_id=u1&user_id=u2&user_id=&pilot_name=g%25&players=4&aggregate=1&reverse=1&page=2")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("code %d, content type %q", w.Code, w.Header().Get("Content-Type"))
	}
	want := &db.FindReplayQuery{BattleCode: "123", UserIDs: []string{"u1", "u2"}, PilotNames: []string{"g%"}, Players: 4, Aggregate: 1, Reverse: true, Page: 2}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("query %+v, want %+v", got, want)
	}
	var body []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body) != 1 || body[0]["replay_url"] != "https://example.com/123.pb" {
		t.Fatalf("body %s (%v)", w.Body.String(), err)
	}

	if w := get(""); w.Code != 200 || got.Players != -1 || got.Aggregate != -1 || got.Reverse {
		t.Fatalf("no filter: code %d, query %+v", w.Code, got)
	}
	result = nil
	if w := get("battle_code=999"); w.Code != 204 {
		t.Fatalf("none: code %d", w.Code)
	}
	fail = errors.New("db down")
	if w := get(""); w.Code != 500 {
		t.Fatalf("db error: code %d", w.Code)
	}
	fail = nil
	for _, q := range []string{"players=x", "page=1.5", "user_name=" + strings.Repeat("a", maxReplayFilterBytes+1),
		strings.Repeat("user_id=a&", maxReplayPlayerFilters+1)} {
		if w := get(q); w.Code != 400 || got != nil {
			t.Fatalf("%.40s: code %d, searched %v", q, w.Code, got != nil)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/ops/replay_uploaded?battle_code=1&url=x", nil))
	if w.Code != 404 {
		t.Fatalf("ops path on the public api: code %d", w.Code)
	}
}

func TestAPILive(t *testing.T) {
	var result []*LiveBattle
	h := &APIHandler{LiveList: func() []*LiveBattle { return result }}
	get := func() string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/lbs/live", nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("code %d, content type %q", w.Code, w.Header().Get("Content-Type"))
		}
		return strings.TrimSpace(w.Body.String())
	}
	if got := get(); got != "[]" {
		t.Fatalf("empty: %s", got)
	}
	result = []*LiveBattle{{BattleCode: "B1", StartUnix: 1700000000, Frames: 60, Spectators: 2,
		Users: []*db.ReplayUser{{UserID: "u1", UserName: "n1", PilotName: "p1", Team: 2, Pos: 1}}}}
	want := `[{"battle_code":"B1","users":[{"user_id":"u1","user_name":"n1","pilot_name":"p1","team":2,"pos":1}],"start_unix":1700000000,"frames":60,"closed":false,"spectators":2}]`
	if got := get(); got != want {
		t.Fatalf("body %s", got)
	}
}
