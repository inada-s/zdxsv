package lobby

import (
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestTestRelaySession(t *testing.T) {
	now := time.Now()
	r, maintain := newTestRelay(9, 0xABC)
	r.now = func() time.Time { return now }
	a := netip.MustParseAddrPort("1.2.3.4:5000")
	if dst, _ := r.handle(relayPing(0, 9, 0xABC, 1), a); dst != a {
		t.Fatal("ping with the test token not answered")
	}
	if dst, _ := r.handle(relayPing(0, 9, 0xABD, 1), a); dst.IsValid() {
		t.Error("ping with another token answered")
	}

	now = now.Add(relayIdleTimeout + time.Second)
	maintain()
	s, ok := r.sessions[9]
	if !ok || !s.created.Equal(now) {
		t.Fatalf("stale test session not registered again: %+v", s)
	}
	if dst, _ := r.handle(relayPing(1, 9, 0xABC, 2), a); dst != a {
		t.Error("ping not answered after the session was registered again")
	}
}

func TestRelayServeMaintained(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	returned := make(chan struct{})
	go func() {
		NewRelay().serveMaintained(conn, 5*time.Millisecond, func() { calls.Add(1) })
		close(returned)
	}()
	for end := time.Now().Add(5 * time.Second); calls.Load() < 2; time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("maintain called %d times in 5 s", calls.Load())
		}
	}

	conn.Close()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("serveMaintained did not return after the conn closed")
	}
	n := calls.Load()
	time.Sleep(50 * time.Millisecond)
	if m := calls.Load(); m != n {
		t.Errorf("maintain called %d times after serveMaintained returned", m-n)
	}
}
