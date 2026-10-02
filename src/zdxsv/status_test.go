package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	. "zdxsv/pkg/lobby/lobbyrpc"
	"zdxsv/pkg/lobby/model"
)

func TestMakeStatusPlatforms(t *testing.T) {
	res := &StatusResponse{
		LobbyUsers: []User{
			{UserID: "AAAAAA", Platform: model.PlatformConsole},
			{UserID: "BBBBBB", Platform: model.PlatformEmuX8664, UDP: true},
			{UserID: "CCCCCC"}, // an old lobby sends no platform
			{UserID: "DDDDDD", Platform: model.PlatformEmuX8664},
		},
		Battles: []Battle{{Platform: model.PlatformEmuX8664, Users: []User{
			{UserID: "DDDDDD", Platform: model.PlatformEmuX8664}, // also in the lobby list: counted once
			{UserID: "EEEEEE", Platform: model.PlatformEmuX8664},
		}}},
	}
	s := makeStatus(res, time.Now())
	if s.LobbyUserCount != 4 || s.BattleUserCount != 1 {
		t.Errorf("counts %d %d", s.LobbyUserCount, s.BattleUserCount)
	}
	if c := s.Platforms[model.PlatformConsole]; c.LobbyUserCount != 2 || c.BattleUserCount != 0 {
		t.Errorf("console %+v", *c)
	}
	if c := s.Platforms[model.PlatformEmuX8664]; c.LobbyUserCount != 2 || c.BattleUserCount != 1 {
		t.Errorf("emulator %+v", *c)
	}
	if s.LobbyUsers[1].UDP != "○" || s.LobbyUsers[2].Platform != model.PlatformConsole {
		t.Errorf("users %+v", s.LobbyUsers)
	}
}

func TestApiStatEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	getApiStat(w, httptest.NewRequest("GET", "/api/stat", nil))
	var body struct {
		LobbyUsers []interface{}
		Platforms  map[string]platformCount
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if body.LobbyUsers == nil || len(body.Platforms) != 2 {
		t.Errorf("body %s", w.Body.String())
	}
}
