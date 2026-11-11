package lobby

import (
	"bytes"
	"fmt"
	"sort"
	"testing"

	"zdxsv/pkg/db"
	"zdxsv/pkg/db/dbtest"
	. "zdxsv/pkg/lobby/message"
	"zdxsv/pkg/lobby/model"
)

// newRobustPeer returns a logged in peer of a new App; sent messages stay in its Conn's outbuf.
func newRobustPeer(t *testing.T) *AppPeer {
	t.Helper()
	c, _ := newPipe(t)
	app := NewApp()
	p := &AppPeer{app: app, conn: NewConn(c), Platform: model.PlatformConsole}
	p.User = model.User{User: db.User{UserID: "ROBUST", Name: "robust"}}
	app.users[p.UserID] = p
	return p
}

// Every client message handler must survive any body in any peer state: a
// panic in a handler stops the whole lobby.
func TestHandlersSurviveBadInput(t *testing.T) {
	dbtest.Use(t)
	states := []struct {
		name  string
		setup func(*AppPeer)
	}{
		{"no lobby", func(*AppPeer) {}},
		{"in lobby", func(p *AppPeer) { p.app.OnEnterLobby(p, 4) }},
	}
	bodies := [][]byte{
		nil,
		{0, 1, 0xFF, 0xFF, 0xFF, 0xFF},
		bytes.Repeat([]byte{0xFF}, 16),
	}
	var cmds []int
	for cmd, name := range defaultHandlers.handlerNames {
		if name == "testHandler" { // added by TestDispatchMessage
			continue
		}
		cmds = append(cmds, int(cmd))
	}
	sort.Ints(cmds)

	for _, s := range states {
		for _, cmd := range cmds {
			for i, body := range bodies {
				name := fmt.Sprintf("%s/0x%04x %s/%d", s.name, cmd, defaultHandlers.handlerNames[uint16(cmd)], i)
				func() {
					p := newRobustPeer(t)
					s.setup(p)
					m := NewClientQuestion(uint16(cmd))
					m.Writer().Write(body)
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("%s: panic: %v", name, r)
						}
					}()
					defaultHandlers.handlers[uint16(cmd)](p, m)
				}()
			}
		}
	}
}
