package main

import (
	"testing"
	"time"

	"github.com/valyala/gorpc"

	. "zdxsv/pkg/lobby/lobbyrpc"
)

func lobbyUserCount() int {
	current.RLock()
	defer current.RUnlock()
	return current.statusData.LobbyUserCount
}

func TestUpdateStatus(t *testing.T) {
	saved := current.statusData
	t.Cleanup(func() { current.statusData = saved })

	var answer interface{}
	s := gorpc.NewTCPServer("127.0.0.1:0", func(_ string, req interface{}) interface{} {
		if _, ok := req.(*StatusRequest); !ok {
			t.Errorf("request %T", req)
		}
		return answer
	})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			s.Stop()
		}
	})
	c := gorpc.NewTCPClient(s.Listener.ListenAddr().String())
	c.Start()
	t.Cleanup(c.Stop)

	answer = &StatusResponse{LobbyUsers: []User{{UserID: "AAAAAA"}, {UserID: "BBBBBB"}}}
	updateStatus(c)
	if n := lobbyUserCount(); n != 2 {
		t.Fatalf("lobby users %d after a status answer, want 2", n)
	}

	answer = &RegisterProxyResponse{}
	updateStatus(c)
	if n := lobbyUserCount(); n != 2 {
		t.Errorf("lobby users %d after a wrong answer, want the last status", n)
	}

	s.Stop()
	stopped = true
	start := time.Now()
	updateStatus(c)
	if n := lobbyUserCount(); n != 2 {
		t.Errorf("lobby users %d with the lobby down, want the last status", n)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("call with the lobby down took %v", d)
	}
}
