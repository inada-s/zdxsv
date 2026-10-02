package lobby

import (
	"testing"
	"zdxsv/pkg/db"
	"zdxsv/pkg/lobby/model"
)

func TestStatusPlatform(t *testing.T) {
	app := NewApp()
	ps2 := &AppPeer{Platform: model.PlatformConsole}
	ps2.UserID = "AAAAAA"
	emu := &AppPeer{Platform: model.PlatformEmuX8664}
	emu.UserID = "BBBBBB"
	app.users["SAAAAAA"] = ps2
	app.users["SBBBBBB"] = emu
	b := model.NewBattle(model.PlatformEmuX8664, 0)
	b.Users = append(b.Users, model.User{User: db.User{UserID: "CCCCCC", SessionID: "SCCCCCC"}})
	app.battles["SCCCCCC"] = b

	res := app.status()
	got := map[string]string{}
	for _, u := range res.LobbyUsers {
		got[u.UserID] = u.Platform
	}
	if got["AAAAAA"] != model.PlatformConsole || got["BBBBBB"] != model.PlatformEmuX8664 {
		t.Errorf("lobby platforms %v", got)
	}
	if len(res.Battles) != 1 || res.Battles[0].Platform != model.PlatformEmuX8664 ||
		len(res.Battles[0].Users) != 1 || res.Battles[0].Users[0].Platform != model.PlatformEmuX8664 {
		t.Errorf("battles %+v", res.Battles)
	}
}
