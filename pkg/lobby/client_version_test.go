package lobby

import (
	"strings"
	"testing"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
)

func TestLobbyOutdatedClient(t *testing.T) {
	conn := dbtest.Use(t)
	addr := startLobby(t, battlerpc.NewBattleRpc())
	setMin := func(v string) {
		t.Helper()
		if _, err := conn.Exec(`INSERT OR REPLACE INTO m_server_setting (key, value) VALUES ('min_pcsx2_version', ?)`, v); err != nil {
			t.Fatal(err)
		}
	}
	dial := func(info string) *testClient {
		c := dialLobby(t, addr)
		n := NewClientNotice(0x9950)
		n.Writer().Write([]byte("emulator=pcsx2\n" + info))
		c.send(n)
		return c
	}
	refused := func(info string) {
		t.Helper()
		ac := registerAccount(t)
		c := dial(info)
		kp := c.expect(0x6101, CategoryQuestion)
		a := NewClientAnswer(kp)
		a.Writer().WriteString(keyPairKey1(ac.SessionID)).Write(encryptString(a.Seq, ac.LoginKey))
		c.send(a)
		if msg := c.expect(0x6003, CategoryNotice).Reader().ReadString(); !strings.Contains(msg, "PLEASE UPDATE PCSX2") {
			t.Fatalf("%q: refusal %q", info, msg)
		}
		c.ask(0x600E, nil)
		for _, m := range c.queue {
			if m.Command == 0x6103 {
				t.Fatalf("%q: first data asked after the refusal", info)
			}
		}
	}
	allowed := func(info string) {
		t.Helper()
		dial(info).login(registerAccount(t), "******", "p")
	}

	allowed("pcsx2=zdxsv-0.0.1\n")
	setMin("zdxsv-1.2.0")
	refused("pcsx2=zdxsv-1.1.9\n")
	refused("pcsx2=zdxsv-1.2.0-dev.3\n")
	refused("pcsx2=garbage\n")
	allowed("pcsx2=zdxsv-1.2.0\n")
	allowed("pcsx2=zdxsv-1.2.1-dev.1\n")
	allowed("pcsx2=zdxsv-1.10.0\n")
	allowed("version=abc\n")
	setMin("1.3")
	allowed("pcsx2=zdxsv-1.1.9\n")
}
