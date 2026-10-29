package lobby

import (
	"fmt"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

func TestLobbyBattleSameSyncOnly(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())
	const lobbyID = 2
	enter := func(i int, sync string, side byte) *testClient {
		c := dialLobby(t, addr)
		n := NewClientNotice(0x9950)
		n.Writer().Write([]byte("emulator=pcsx2\nsync=" + sync + "\n"))
		c.send(n)
		c.login(registerAccount(t), "******", fmt.Sprintf("pilot%d", i))
		if a := c.ask(0x6305, u16(lobbyID)); a.Status != StatusSuccess {
			t.Fatalf("EnterLobby status %x", a.Status)
		}
		c.ask(0x640E, []byte{side})
		return c
	}
	started := func(c *testClient) bool {
		t.Helper()
		c.ask(0x600E, nil)
		for _, m := range c.queue {
			if m.Command == 0x6910 {
				return true
			}
		}
		return false
	}

	a1 := enter(0, "00000000000000aa", model.EntryAeug)
	b1 := enter(1, "00000000000000bb", model.EntryTitans)
	if started(a1) || started(b1) {
		t.Fatal("battle between different sync fingerprints")
	}
	a2 := enter(2, "00000000000000aa", model.EntryTitans)
	for i, c := range []*testClient{a1, a2} {
		c.expect(0x6910, CategoryNotice)
		if n := c.ask(0x6911, nil).Reader().Read8(); n != 2 {
			t.Errorf("player %d: user count %d", i, n)
		}
	}
	if started(b1) {
		t.Fatal("the other fingerprint joined the battle")
	}
}
