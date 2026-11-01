package lobby

import (
	"fmt"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"

	pb "github.com/golang/protobuf/proto"
)

// A player asking for the battle server address after the others left the
// lobby for the battle still gets their battle info.
func TestBattleInfoAfterPeersLeft(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())
	const lobbyID = 4
	sides := []byte{model.EntryAeug, model.EntryTitans, model.EntryAeug, model.EntryTitans}
	var cs []*testClient
	var ids []string
	for i := range sides {
		c := dialLobby(t, addr)
		n := NewClientNotice(0x9950)
		n.Writer().Write([]byte(fmt.Sprintf("emulator=pcsx2\nudp=1\nggpo=%d\nudp_addr=192.0.2.%d:%d\nsync=0123456789abcdef\n",
			20000+i, 10+i, 30000+i)))
		c.send(n)
		id, _, _ := c.login(registerAccount(t), "******", fmt.Sprintf("pilot%d", i))
		if a := c.ask(0x6305, u16(lobbyID)); a.Status != StatusSuccess {
			t.Fatalf("EnterLobby status %x", a.Status)
		}
		cs = append(cs, c)
		ids = append(ids, id)
	}
	for i, c := range cs {
		c.ask(0x640E, []byte{sides[i]})
	}
	for _, c := range cs {
		c.expect(0x6910, CategoryNotice)
	}

	for i, c := range cs {
		c.ask(0x6916, nil)
		m := battleInfo(t, c.expect(0x9951, CategoryCustom))
		peers := map[string]bool{}
		for _, a := range m.Candidates {
			peers[a.GetUserId()] = true
		}
		for _, u := range m.Users {
			if u.GetSync() != "0123456789abcdef" {
				t.Errorf("player %d: %s sync %q", i, u.GetUserId(), u.GetSync())
			}
			if u.GetUserId() == ids[i] {
				continue
			}
			if u.GgpoPort == nil || !peers[u.GetUserId()] {
				t.Errorf("player %d: %s ggpo port %v, candidates %v", i, u.GetUserId(), u.GgpoPort, peers[u.GetUserId()])
			}
		}
		if m.SessionId == nil || len(m.Users) != len(cs) {
			t.Errorf("player %d: battle info %v", i, pb.CompactTextString(m))
		}
		c.ask(0x6002, nil)
		c.conn.Close()
	}
}
