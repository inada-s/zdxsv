package main

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAPIReplay(t *testing.T) {
	var got *FindReplayQuery
	var result []*FoundReplay
	var fail error
	h := &APIHandler{FindReplay: func(q *FindReplayQuery) ([]*FoundReplay, error) {
		got = q
		return result, fail
	}}
	get := func(query string) *httptest.ResponseRecorder {
		got = nil
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/lbs/replay?"+query, nil))
		return w
	}

	result = []*FoundReplay{{BattleCode: "123", ReplayURL: "https://example.com/123.pb", Users: []*ReplayUser{{UserID: "u1"}}}}
	w := get("battle_code=123&user_id=u1&user_id=u2&user_id=&pilot_name=g%25&players=4&aggregate=1&used_ms=13&reverse=1&page=2")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("code %d, content type %q", w.Code, w.Header().Get("Content-Type"))
	}
	want := &FindReplayQuery{BattleCode: "123", UserIDs: []string{"u1", "u2"}, PilotNames: []string{"g%"}, Players: 4, Aggregate: 1, UsedMs: 13, Reverse: true, Page: 2}
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
	result = []*LiveBattle{{BattleCode: "B1", StartUnix: 1700000000, Bytes: 60, Spectators: 2,
		Users: []*ReplayUser{{UserID: "u1", UserName: "n1", PilotName: "p1", Team: 2, Pos: 1}}}}
	want := `[{"battle_code":"B1","users":[{"user_id":"u1","user_name":"n1","pilot_name":"p1","team":2,"pos":1}],"start_unix":1700000000,"bytes":60,"closed":false,"spectators":2}]`
	if got := get(); got != want {
		t.Fatalf("body %s", got)
	}
}

func TestMakeStatusPlatforms(t *testing.T) {
	res := &StatusResponse{
		LobbyUsers: []ZproxyUser{
			{UserID: "AAAAAA", Platform: PlatformConsole},
			{UserID: "BBBBBB", Platform: PlatformEmu, UDP: true},
			{UserID: "CCCCCC"}, // no platform: a console
			{UserID: "DDDDDD", Platform: PlatformEmu},
		},
		Battles: []ZproxyBattle{{Platform: PlatformEmu, Users: []ZproxyUser{
			{UserID: "DDDDDD", Platform: PlatformEmu}, // also in the lobby list: counted once
			{UserID: "EEEEEE", Platform: PlatformEmu},
		}}},
	}
	s := MakeStatus(res, time.Now())
	if s.LobbyUserCount != 4 || s.BattleUserCount != 1 {
		t.Errorf("counts %d %d", s.LobbyUserCount, s.BattleUserCount)
	}
	if c := s.Platforms[PlatformConsole]; c.LobbyUserCount != 2 || c.BattleUserCount != 0 {
		t.Errorf("console %+v", *c)
	}
	if c := s.Platforms[PlatformEmu]; c.LobbyUserCount != 2 || c.BattleUserCount != 1 {
		t.Errorf("emulator %+v", *c)
	}
	if s.LobbyUsers[1].UDP != "○" || s.LobbyUsers[2].Platform != PlatformConsole {
		t.Errorf("users %+v", s.LobbyUsers)
	}
}

func TestAPIStatus(t *testing.T) {
	calls := 0
	h := &APIHandler{Status: func() *StatusResponse {
		calls++
		return &StatusResponse{LobbyUsers: []ZproxyUser{{UserID: "AAAAAA"}, {UserID: "BBBBBB"}}}
	}}
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/lbs/status", nil))
		return w
	}
	var body struct {
		NowDate    string                   `json:"now_date"`
		LobbyUsers []StatusUser             `json:"lobby_users"`
		Platforms  map[string]PlatformCount `json:"platforms"`
	}
	w := get()
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("code %d, content type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if len(body.LobbyUsers) != 2 || body.Platforms[PlatformConsole].LobbyUserCount != 2 || body.NowDate == "" {
		t.Errorf("body %s", w.Body.String())
	}
	// a second request within statusMaxAge reuses the answer
	if get(); calls != 1 {
		t.Errorf("lobby asked %d times for 2 requests in a row", calls)
	}
	h.statusAt = time.Now().Add(-2 * statusMaxAge)
	if get(); calls != 2 {
		t.Errorf("lobby asked %d times after the answer expired", calls)
	}

	// no lobby: an empty status, not an error
	w = httptest.NewRecorder()
	(&APIHandler{}).ServeHTTP(w, httptest.NewRequest("GET", "/lbs/status", nil))
	body.LobbyUsers = nil
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 {
		t.Fatal(w.Code, err, w.Body.String())
	}
	if body.LobbyUsers == nil || len(body.Platforms) != 2 {
		t.Errorf("empty body %s", w.Body.String())
	}
}
