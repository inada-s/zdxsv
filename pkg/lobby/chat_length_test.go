package lobby

import (
	"strings"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db/dbtest"
)

func TestLobbyChatDropsTextLongerThanGameInput(t *testing.T) {
	dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())
	var cs []*testClient
	var ids []string
	for _, name := range []string{"pilot0", "pilot1"} {
		c := dialLobby(t, addr)
		id, _, _ := c.login(registerAccount(t), "******", name)
		c.ask(0x6305, u16(4))
		c.heard()
		cs = append(cs, c)
		ids = append(ids, id)
	}
	for _, tc := range []struct {
		line  string
		heard bool
	}{
		{strings.Repeat("あ", 20), true},
		{strings.Repeat("あ", 21), false},
		{strings.Repeat("あ", 26), false},
		{strings.Repeat("a", 40), true},
		{strings.Repeat("a", 41), false},
	} {
		cs[0].say(sjis(t, tc.line))
		for i, c := range cs {
			got := c.heard()
			want := 0
			if tc.heard {
				want = 1
			}
			if len(got) != want || (want == 1 && got[0] != ids[0]+":"+sjis(t, tc.line)) {
				t.Errorf("%d chars, client %d heard %q", len([]rune(tc.line)), i, got)
			}
		}
	}
}
