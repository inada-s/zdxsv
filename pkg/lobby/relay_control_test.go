package lobby

import (
	"context"
	"net"
	"testing"
	"time"
)

func registeredRelay(addr string) *RegionalRelay {
	regionalRelays.Lock()
	defer regionalRelays.Unlock()
	return regionalRelays.byAddr[addr]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func startRegionalRelay(t *testing.T, ctx context.Context, lobbyAddr, secret string) (*net.UDPAddr, chan error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	addr := conn.LocalAddr().(*net.UDPAddr)
	done := make(chan error, 1)
	go func() {
		done <- RunRegionalRelay(ctx, conn, lobbyAddr, RelayStatus{Region: "asia-northeast1", PublicAddr: addr.String(), Secret: secret})
	}()
	return addr, done
}

func TestRegionalRelayRegistersAndRelays(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go ServeRelayControl(l, "s3cret")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, wrongDone := startRegionalRelay(t, ctx, l.Addr().String(), "wrong")
	relay, done := startRegionalRelay(t, ctx, l.Addr().String(), "s3cret")
	waitFor(t, "registration", func() bool { return registeredRelay(relay.String()) != nil })

	infoA := map[string]string{"asia-northeast1": "20"}
	infoB := map[string]string{"asia-northeast1": "30"}
	info := map[string]map[string]string{"AAAAAA": infoA, "BBBBBB": infoB}
	relays := battleRelays([]string{"AAAAAA", "BBBBBB"}, func(id string) map[string]string { return info[id] })
	if len(relays) != 1 || relays[0].PublicAddr != relay.String() || relays[0].Region != "asia-northeast1" {
		t.Fatalf("battle relays %+v, want only %s", relays, relay)
	}
	token := relays[0].Sessions.SessionToken(9)
	if relays[0].Sessions.SessionToken(9) != token {
		t.Fatalf("second ask for session 9 got another token")
	}

	var players [2]*net.UDPConn
	for i := range players {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		players[i] = c
		buf := make([]byte, 64)
		waitFor(t, "pong with the pushed token", func() bool {
			c.WriteToUDP(relayPing(uint8(i), 9, token, 42), relay)
			c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, err := c.ReadFromUDP(buf)
			return err == nil && n == relayPingSize && buf[4] == relayTypePong
		})
	}
	players[0].WriteToUDP(ggpoRelayPacket(0, 1, 5), relay)
	buf := make([]byte, 64)
	players[1].SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := players[1].ReadFromUDP(buf)
	if err != nil || n != ggpoHeaderSize+4 || buf[7] != 5 || buf[10] != 0 {
		t.Fatalf("relayed: %x %v", buf[:n], err)
	}

	cancel()
	<-done
	<-wrongDone
	waitFor(t, "unregistration", func() bool { return registeredRelay(relay.String()) == nil })
}

func TestRegionalRelayShutdown(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, done := startRegionalRelay(t, ctx, l.Addr().String(), "s3cret")
	conn, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte(`{"shutdown":true}` + "\n"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("relay stopped with %v, want a shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay still running after the shutdown")
	}
}
