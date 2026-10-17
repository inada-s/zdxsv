package battlerpc

import "testing"

func TestBattleRpc(t *testing.T) {
	m := NewBattleRpc()
	var pong string
	if m.PingPong(nil, &pong); pong != "Pong" {
		t.Errorf("PingPong = %q", pong)
	}
	users := []User{{UserID: "U1", SessionID: "S1"}, {UserID: "U2", SessionID: "S2"}}
	var reply int
	for i := 0; i < MaxRoom+1; i++ {
		if err := m.NotifyBattleUsers(&BattleInfoArgs{Users: users}, &reply); err != nil || reply != 1 {
			t.Fatalf("NotifyBattleUsers = %v, %d", err, reply)
		}
	}
	// room ids wrap at MaxRoom; the newest notification wins per session
	if info := m.GetBattleInfo("S2"); info == nil || info.RoomID != 0 || len(info.Users) != 2 {
		t.Fatalf("GetBattleInfo = %+v", info)
	}
	m.ClearBattleInfo("S2")
	if m.GetBattleInfo("S2") != nil || m.GetBattleInfo("S1") == nil {
		t.Errorf("ClearBattleInfo cleared the wrong session")
	}
}
