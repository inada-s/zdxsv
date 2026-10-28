package lobby

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"zdxsv/pkg/lobby/lobbyrpc"
	"zdxsv/pkg/lobby/model"
)

func TestMakeStatusPlatforms(t *testing.T) {
	res := &lobbyrpc.StatusResponse{
		LobbyUsers: []lobbyrpc.User{
			{UserID: "AAAAAA", Platform: model.PlatformConsole},
			{UserID: "BBBBBB", Platform: model.PlatformEmu, UDP: true},
			{UserID: "CCCCCC"}, // no platform: a console
			{UserID: "DDDDDD", Platform: model.PlatformEmu},
		},
		Battles: []lobbyrpc.Battle{{Platform: model.PlatformEmu, Users: []lobbyrpc.User{
			{UserID: "DDDDDD", Platform: model.PlatformEmu}, // also in the lobby list: counted once
			{UserID: "EEEEEE", Platform: model.PlatformEmu},
		}}},
	}
	s := MakeStatus(res, time.Now())
	if s.LobbyUserCount != 4 || s.BattleUserCount != 1 {
		t.Errorf("counts %d %d", s.LobbyUserCount, s.BattleUserCount)
	}
	if c := s.Platforms[model.PlatformConsole]; c.LobbyUserCount != 2 || c.BattleUserCount != 0 {
		t.Errorf("console %+v", *c)
	}
	if c := s.Platforms[model.PlatformEmu]; c.LobbyUserCount != 2 || c.BattleUserCount != 1 {
		t.Errorf("emulator %+v", *c)
	}
	if s.LobbyUsers[1].UDP != "○" || s.LobbyUsers[2].Platform != model.PlatformConsole {
		t.Errorf("users %+v", s.LobbyUsers)
	}
}

func TestAPIStatus(t *testing.T) {
	calls := 0
	h := &APIHandler{Status: func() *lobbyrpc.StatusResponse {
		calls++
		return &lobbyrpc.StatusResponse{LobbyUsers: []lobbyrpc.User{{UserID: "AAAAAA"}, {UserID: "BBBBBB"}}}
	}}
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/lbs/status", nil))
		return w
	}
	var body struct {
		NowDate    string
		LobbyUsers []StatusUser
		Platforms  map[string]PlatformCount
	}
	w := get()
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("code %d, content type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if len(body.LobbyUsers) != 2 || body.Platforms[model.PlatformConsole].LobbyUserCount != 2 || body.NowDate == "" {
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
