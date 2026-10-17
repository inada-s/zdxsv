package model

import (
	"testing"

	"zdxsv/pkg/db"
)

func TestBattleGetUserByPosOutOfRange(t *testing.T) {
	b := NewBattle("ps2", 2)
	b.Add(&User{User: db.User{UserID: "a"}})
	b.Add(&User{User: db.User{UserID: "b"}})
	for _, pos := range []byte{0, 3, 255} {
		if u := b.GetUserByPos(pos); u != nil {
			t.Errorf("GetUserByPos(%d) = %v, want nil", pos, u.UserID)
		}
	}
	if u := b.GetUserByPos(2); u == nil || u.UserID != "b" {
		t.Errorf("GetUserByPos(2) = %v", u)
	}
}
