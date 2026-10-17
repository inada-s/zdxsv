package lobby

import (
	"errors"
	"io"
	"testing"
	"time"

	"zdxsv/pkg/battle/battlerpc"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

// waitPeers waits until the lobby serves n peers.
func waitPeers(t *testing.T, app *App, n int) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		got := 0
		app.Locked(func(a *App) { got = len(a.peers) })
		if got == n {
			return
		}
	}
	t.Fatalf("lobby did not reach %d peers", n)
}

// expectClosed reads until the lobby closes the connection.
func expectClosed(t *testing.T, c *testClient) {
	t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	for {
		_, err := c.conn.Read(buf)
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			t.Fatalf("want the lobby to close the connection, got %v", err)
		}
		return
	}
}

func TestAliveCheckIntervalLineCheck(t *testing.T) {
	addr, _ := startLobbyAppWith(t, battlerpc.NewBattleRpc(), func(a *App) {
		a.aliveCheckInterval = 50 * time.Millisecond
	})
	c := dialLobby(t, addr)
	c.expect(0x6001, CategoryQuestion)
	c.expect(0x6001, CategoryQuestion)
}

func TestAliveCheckRecvTimeout(t *testing.T) {
	addr, app := startLobbyApp(t, battlerpc.NewBattleRpc())
	c := dialLobby(t, addr)
	waitPeers(t, app, 1)

	var start time.Time
	app.Locked(func(a *App) {
		for _, p := range a.peers {
			start = p.lastRecvTime
		}
		a.checkAlive(start.Add(peerRecvTimeout - time.Second))
	})
	c.expect(0x6001, CategoryQuestion)

	app.Locked(func(a *App) { a.checkAlive(start.Add(peerRecvTimeout)) })
	expectClosed(t, c)
	waitPeers(t, app, 0)
}

func TestAliveCheckBattleTimeout(t *testing.T) {
	_, app := startLobbyApp(t, battlerpc.NewBattleRpc())
	now := time.Now()
	app.Locked(func(a *App) {
		for sid, age := range map[string]time.Duration{"old": battleTimeout, "new": battleTimeout - time.Second} {
			b := model.NewBattle(model.PlatformConsole, 0)
			b.StartTime = now.Add(-age)
			a.battles[sid] = b
		}
		a.checkAlive(now)
	})
	app.Locked(func(a *App) {
		if _, ok := a.battles["old"]; ok {
			t.Error("battle of battleTimeout age kept")
		}
		if _, ok := a.battles["new"]; !ok {
			t.Error("younger battle dropped")
		}
	})
}

func TestAliveCheckBattleServerReconnect(t *testing.T) {
	_, app := startLobbyApp(t, battlerpc.NewBattleRpc())
	ping := func(a *App) error {
		var reply string
		return a.battleServer.Call("Logic.PingPong", "Ping", &reply)
	}
	app.Locked(func(a *App) {
		if a.battleServer == nil {
			t.Fatal("no battle server connection at start")
		}
		a.battleServer.Close()
		a.checkAlive(time.Now())
		if a.battleServer == nil || ping(a) != nil {
			t.Error("broken battle server connection not replaced")
		}

		a.battleServer = nil
		a.checkAlive(time.Now())
		if a.battleServer == nil || ping(a) != nil {
			t.Error("missing battle server connection not made")
		}
	})
}
