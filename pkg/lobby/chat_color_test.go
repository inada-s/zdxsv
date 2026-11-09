package lobby

import (
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
)

// Each chat notice ends with 4 zero bytes after the text: type and the ID, name
// and text colors, which mean the default colors.
func TestChatNoticeColorBytes(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())

	c := dialLobby(t, addr)
	c.login(registerAccount(t), "******", "pilot")
	c.ask(0x6305, u16(4))
	c.say("11111111")
	c.say("a")
	c.ask(0x600E, nil)

	n := 0
	for _, m := range c.queue {
		if m.Command != 0x6702 || m.Category != CategoryNotice {
			continue
		}
		n++
		r := m.Reader()
		r.ReadString()
		r.ReadString()
		text := r.ReadString()
		if r.Remaining() != 4 {
			t.Fatalf("%q: %d bytes after the text, want 4", text, r.Remaining())
		}
		for i := 0; i < 4; i++ {
			if b := r.Read8(); b != 0 {
				t.Errorf("%q: byte %d after the text is %#x, want 0", text, i, b)
			}
		}
	}
	if n != 2 {
		t.Fatalf("got %d chat notices, want 2", n)
	}
}
