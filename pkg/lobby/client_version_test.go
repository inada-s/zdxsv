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
	set := func(key, v string) {
		t.Helper()
		if _, err := conn.Exec(`INSERT OR REPLACE INTO m_server_setting (key, value) VALUES (?, ?)`, key, v); err != nil {
			t.Fatal(err)
		}
	}
	dial := func(info string) *testClient {
		c := dialLobby(t, addr)
		if info != "" {
			n := NewClientNotice(0x9950)
			n.Writer().Write([]byte(info))
			c.send(n)
		}
		return c
	}
	refusedAs := func(ac *testLogin, info string) {
		t.Helper()
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
	refused := func(info string) { t.Helper(); refusedAs(registerAccount(t), info) }
	allowedAs := func(ac *testLogin, info string) {
		t.Helper()
		dial(info).login(ac, "******", "p")
	}
	allowed := func(info string) { t.Helper(); allowedAs(registerAccount(t), info) }
	const emu = "emulator=pcsx2\n"

	allowed(emu + "pcsx2=zdxsv-0.0.1\n")
	allowed(emu + "pcsx2=zdxsv-1.2.1-dev.1\n")
	allowed(emu + "version=abc\n")
	set("min_pcsx2_version", "zdxsv-1.2.0")
	refused(emu + "pcsx2=zdxsv-1.1.9\n")
	refused(emu + "pcsx2=zdxsv-1.2.0-dev.3\n")
	refused(emu + "pcsx2=garbage\n")
	refused(emu + "pcsx2=zdxsv-1.2.1-dev.1\n")
	refused(emu + "version=abc\n")
	refused("emulator=other\n")
	allowed(emu + "pcsx2=zdxsv-1.2.0\n")
	allowed(emu + "pcsx2=zdxsv-1.10.0\n")
	allowed("")
	allowed("os=windows\n")

	dev, other := registerAccount(t), registerAccount(t)
	refusedAs(dev, emu+"version=abc\n")
	set("pcsx2_allow", "x, "+dev.LoginKey+",mach-1")
	allowedAs(dev, emu+"version=abc\n")
	allowedAs(dev, emu+"pcsx2=zdxsv-1.1.9\n")
	refusedAs(other, emu+"version=abc\n")
	allowed(emu + "machine_id=mach-1\nversion=abc\n")
	refused(emu + "machine_id=mach-2\nversion=abc\n")

	set("min_pcsx2_version", "1.3")
	allowed(emu + "pcsx2=zdxsv-1.1.9\n")
}
