package lobby

import (
	"testing"
	"time"
	"zdxsv/pkg/config"
	"zdxsv/pkg/lobby/message"
)

type callcenter chan interface{}

func newCallcenter() callcenter {
	return make(chan interface{}, 1)
}

func (c callcenter) Call(v interface{}) {
	c <- v
}

func (c callcenter) WaitCall(t *testing.T) {
	select {
	case <-c:
	case <-time.After(10 * time.Millisecond):
		t.Fail()
	}
}

func TestDispatchMessage(t *testing.T) {
	c := newCallcenter()

	config.Conf.Lobby.Addr = ":18200"
	config.Conf.Lobby.PublicAddr = ":18200"
	config.Conf.Lobby.RPCAddr = ":18201"
	config.Conf.Battle.Addr = ":18210"
	config.Conf.Battle.PublicAddr = ":18210"
	config.Conf.Battle.RPCAddr = ":13080"

	app := NewApp()
	go app.Serve()
	defer app.Quit()

	p := &AppPeer{app: app}
	app.AddHandler(0x0123, "testHandler", func(peer *AppPeer, msg *message.Message) {
		t.Logf("%+v %+v\n", peer, msg)
		c.Call(msg)
	})
	app.chEvent <- eventPeerMessage{
		peer: p,
		msg:  message.NewClientQuestion(0x0123),
	}

	c.WaitCall(t)
}
