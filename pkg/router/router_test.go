package router

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// fakeBackend hands every accepted connection to the test.
func fakeBackend(t *testing.T) (string, chan net.Conn) {
	t.Helper()
	l := listenLocal(t)
	ch := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
			ch <- c
		}
	}()
	return l.Addr().String(), ch
}

// startRouter routes to fresh fake backends and returns a connected client.
func startRouter(t *testing.T) (client *net.TCPConn, dnas, https chan net.Conn) {
	r := &Router{HelloTimeout: 300 * time.Millisecond}
	r.DNASBackend, dnas = fakeBackend(t)
	r.HTTPSBackend, https = fakeBackend(t)
	return startRouterWith(t, r), dnas, https
}

func startRouterWith(t *testing.T, r *Router) *net.TCPConn {
	t.Helper()
	l := listenLocal(t)
	go r.Serve(l)
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c.(*net.TCPConn)
}

func accepted(t *testing.T, ch chan net.Conn) net.Conn {
	t.Helper()
	select {
	case c := <-ch:
		c.SetDeadline(time.Now().Add(2 * time.Second))
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("backend got no connection")
		return nil
	}
}

func noConn(t *testing.T, ch chan net.Conn, name string) {
	t.Helper()
	select {
	case <-ch:
		t.Errorf("%s backend got a connection", name)
	default:
	}
}

func readFull(t *testing.T, r io.Reader, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return b
}

// proxyHeader is the PROXY v1 line the https backend (caddy) gets for client c.
func proxyHeader(c net.Conn) string {
	cl, sv := c.LocalAddr().(*net.TCPAddr), c.RemoteAddr().(*net.TCPAddr)
	return fmt.Sprintf("PROXY TCP4 %s %s %d %d\r\n", cl.IP, sv.IP, cl.Port, sv.Port)
}

var consoleHello = []byte{128, 100, 1, 0x03, 0x01, 'd', 'n', 'a', 's'}

func TestRouteGameConsoleToDNAS(t *testing.T) {
	c, dnas, https := startRouter(t)
	c.Write(consoleHello)
	b := accepted(t, dnas)
	if got := readFull(t, b, len(consoleHello)); !bytes.Equal(got, consoleHello) {
		t.Fatalf("dnas got % x, want the hello unchanged", got)
	}
	// Both directions; the client closing its write side ends the backend conn.
	b.Write([]byte("pong"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if got := readFull(t, c, 4); string(got) != "pong" {
		t.Errorf("client got %q", got)
	}
	c.Write([]byte("more"))
	c.CloseWrite()
	if rest, err := io.ReadAll(b); err != nil || string(rest) != "more" {
		t.Errorf("dnas rest = %q, %v", rest, err)
	}
	noConn(t, https, "https")
}

func TestRouteOthersToHTTPSWithProxyHeader(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hello []byte
	}{
		{"tls", []byte{22, 3, 1, 0x02, 0x00, 1, 2, 3}},
		{"console-like length", []byte{128, 100, 1, 0x03, 0x02, 1}},
		{"short hello timeout", []byte{128, 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, dnas, https := startRouter(t)
			c.Write(tc.hello)
			b := accepted(t, https)
			r := bufio.NewReader(b)
			line, err := r.ReadString('\n')
			if err != nil || line != proxyHeader(c) {
				t.Fatalf("header = %q, %v; want %q", line, err, proxyHeader(c))
			}
			if got := readFull(t, r, len(tc.hello)); !bytes.Equal(got, tc.hello) {
				t.Errorf("https got % x, want % x", got, tc.hello)
			}
			b.Write([]byte("ok"))
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			if got := readFull(t, c, 2); string(got) != "ok" {
				t.Errorf("client got %q", got)
			}
			noConn(t, dnas, "dnas")
		})
	}
}

func expectAlert(t *testing.T, c *net.TCPConn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{21, 3, 0, 0, 2, 2, 80}; !bytes.Equal(got, want) {
		t.Errorf("client got % x, want internal_error alert % x", got, want)
	}
}

func TestBackendDownSendsAlert(t *testing.T) {
	l := listenLocal(t)
	l.Close()
	c := startRouterWith(t, &Router{DNASBackend: l.Addr().String(), HelloTimeout: 300 * time.Millisecond})

	// Only the record header: unread bytes at close would reset the conn before the alert is read.
	c.Write(consoleHello[:5])
	expectAlert(t, c)
}

func TestNoHTTPSBackendSendsAlert(t *testing.T) {
	c := startRouterWith(t, &Router{HelloTimeout: 300 * time.Millisecond})
	c.Write([]byte{22, 3, 1, 0x02})
	expectAlert(t, c)
}

// TestConsoleInProcess: with Console set, a game console connection is served in
// process and reads the hello unchanged (the sniffed bytes put back in front).
func TestConsoleInProcess(t *testing.T) {
	got := make(chan []byte, 1)
	r := &Router{HelloTimeout: 300 * time.Millisecond, Console: func(c net.Conn) {
		c.SetDeadline(time.Now().Add(2 * time.Second))
		b, _ := io.ReadAll(c)
		c.Write([]byte("pong"))
		got <- b
	}}
	r.HTTPSBackend, _ = fakeBackend(t)
	c := startRouterWith(t, r)
	c.Write(consoleHello)
	c.CloseWrite()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if b := readFull(t, c, 4); string(b) != "pong" {
		t.Errorf("client got %q", b)
	}
	if b := <-got; !bytes.Equal(b, consoleHello) {
		t.Errorf("console handler got % x, want the hello unchanged", b)
	}
}
