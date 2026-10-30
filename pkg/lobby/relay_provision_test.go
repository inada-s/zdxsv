package lobby

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// relayStarter starts a real regional relay loop for each StartRelay.
type relayStarter struct {
	t         *testing.T
	ctx       context.Context
	lobbyAddr string
	mtx       sync.Mutex
	started   []string
	addrs     map[string]string
	done      map[string]chan error
}

func (s *relayStarter) StartRelay(region string) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		s.t.Error(err)
		return
	}
	s.t.Cleanup(func() { conn.Close() })
	done := make(chan error, 1)
	s.mtx.Lock()
	s.started = append(s.started, region)
	s.addrs[region] = conn.LocalAddr().String()
	s.done[region] = done
	s.mtx.Unlock()
	go func() {
		done <- RunRegionalRelay(s.ctx, conn, s.lobbyAddr, RelayStatus{Region: region, PublicAddr: conn.LocalAddr().String(), Secret: "s3cret"})
	}()
}

func (s *relayStarter) startedRegions() []string {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	return append([]string(nil), s.started...)
}

func addRelayUser(a *App, id string, info map[string]string) {
	p := &AppPeer{PlatformInfo: info}
	p.UserID = id
	a.users[id] = p
}

func TestRelayProvisioning(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go ServeRelayControl(l, "s3cret")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	starter := &relayStarter{t: t, ctx: ctx, lobbyAddr: l.Addr().String(), addrs: map[string]string{}, done: map[string]chan error{}}

	a := NewApp()
	a.SetRelayProvisioning([]string{"asia-northeast1", "us-west2"}, starter)
	t0 := time.Now()

	tokyo := map[string]string{"relay_server": "1", "asia-northeast1": "10", "us-west2": "120"}
	osaka := map[string]string{"relay_server": "1", "asia-northeast2": "9", "us-west2": "130"}
	la := map[string]string{"relay_server": "1", "asia-northeast1": "110", "us-west2": "15"}
	laNoRelay := map[string]string{"asia-northeast1": "110", "us-west2": "15"}
	addRelayUser(a, "AAAAAA", tokyo)
	addRelayUser(a, "BBBBBB", osaka)
	addRelayUser(a, "CCCCCC", tokyo)
	addRelayUser(a, "DDDDDD", laNoRelay)
	a.updateRelays(t0)
	if s := starter.startedRegions(); len(s) != 0 {
		t.Fatalf("3 relay users: started %v", s)
	}
	addRelayUser(a, "DDDDDD", tokyo)
	a.updateRelays(t0)
	if s := starter.startedRegions(); len(s) != 0 {
		t.Fatalf("4 relay users of one group: started %v", s)
	}

	addRelayUser(a, "DDDDDD", la)
	a.updateRelays(t0)
	if s := starter.startedRegions(); len(s) != 2 || s[0] != "asia-northeast1" || s[1] != "us-west2" {
		t.Fatalf("4 relay users of two groups: started %v, want both regions", s)
	}
	tokyoAddr, laAddr := starter.addrs["asia-northeast1"], starter.addrs["us-west2"]
	t.Cleanup(func() { UnregisterRelay(tokyoAddr); UnregisterRelay(laAddr) })
	waitFor(t, "both relays registered", func() bool { return registeredRelay(tokyoAddr) != nil && registeredRelay(laAddr) != nil })

	a.updateRelays(t0.Add(relayStartRetry))
	if s := starter.startedRegions(); len(s) != 2 {
		t.Fatalf("registered relays started again: %v", s)
	}
	relays := battleRelays([]string{"AAAAAA", "CCCCCC"}, func(id string) map[string]string { return a.users[id].PlatformInfo })
	if len(relays) != 2 || relays[0].PublicAddr != tokyoAddr {
		t.Fatalf("battle relays of two Tokyo players: %+v, want Tokyo first", relays)
	}

	delete(a.users, "DDDDDD")
	lastNeeded := t0.Add(relayStartRetry)
	a.updateRelays(lastNeeded.Add(relayIdleShutdown - time.Second))
	regionalRelays.Lock()
	idle := regionalRelays.byAddr[laAddr]
	regionalRelays.byAddr[tokyoAddr].activeSessions = 1
	regionalRelays.Unlock()
	if idle == nil || idle.shutdown == nil {
		t.Fatal("relay shut down before relayIdleShutdown")
	}
	a.updateRelays(lastNeeded.Add(relayIdleShutdown))
	select {
	case err := <-starter.done["us-west2"]:
		if err != nil {
			t.Fatalf("idle relay stopped with %v, want a shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle relay still running after relayIdleShutdown")
	}
	select {
	case err := <-starter.done["asia-northeast1"]:
		t.Fatalf("relay with a session stopped: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	waitFor(t, "idle relay unregistered", func() bool { return registeredRelay(laAddr) == nil })
}
