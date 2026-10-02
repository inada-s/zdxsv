package dnas

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

// query is golden.py's query().
func query(gw, kind, packet string, n int) []byte {
	var q []byte
	for i := 0; len(q) < n; i++ {
		h := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%s/%d", gw, kind, packet, i)))
		q = append(q, h[:]...)
	}
	q = q[:n]
	parts := strings.Split(packet, "_")
	off := 0x1b
	if kind == "i-connect" {
		off = 0x2c
	}
	copy(q[0:4], mustHex(parts[1]))
	copy(q[off:], mustHex(parts[0]))
	return q
}

type golden struct {
	gw, kind, packet string
	qlen, alen       int
	sha              string
}

func readGolden(t *testing.T) []golden {
	f, err := os.Open("testdata/golden.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []golden
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		w := strings.Fields(sc.Text())
		if len(w) != 6 {
			t.Fatalf("bad golden line %q", sc.Text())
		}
		g := golden{gw: w[0], kind: w[1], packet: w[2], sha: w[5]}
		g.qlen, _ = strconv.Atoi(w[3])
		g.alen, _ = strconv.Atoi(w[4])
		out = append(out, g)
	}
	return out
}

// TestAnswerGolden: Answer gives DNASrep's (PHP) answers byte for byte.
func TestAnswerGolden(t *testing.T) {
	gs := readGolden(t)
	if len(gs) < 400 {
		t.Fatalf("only %d golden cases", len(gs))
	}
	for _, g := range gs {
		a, note := Answer(g.gw, g.kind == "i-connect", query(g.gw, g.kind, g.packet, g.qlen))
		sum := sha256.Sum256(a)
		if len(a) != g.alen || hex.EncodeToString(sum[:]) != g.sha {
			t.Errorf("%s %s %s q=%d: got %d B (%s), want %d B", g.gw, g.kind, g.packet, g.qlen, len(a), note, g.alen)
		}
	}
}

func startServer(t *testing.T, loginAddr string) string {
	s, err := NewServer("jp", loginAddr)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go s.Serve(ln)
	return ln.Addr().String()
}

func dialQuery(t *testing.T, addr, method, urlPath string, body []byte) (int, []byte) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	status, b, err := Query(c, method, urlPath, body)
	if err != nil {
		t.Fatal(err)
	}
	return status, b
}

// TestServer: the PS2 dialect end to end (hello, TLS 1.0 RC4-MD5, HTTP/1.0) for
// both query kinds, a 404, and the login page proxy.
func TestServer(t *testing.T) {
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "login %s", r.URL.Path)
	}))
	defer login.Close()
	addr := startServer(t, strings.TrimPrefix(login.URL, "http://"))

	for _, kind := range []string{"i-connect", "others"} {
		q := query("gai-gw", kind, "c9acca0f30eecbf7_01180000", 308)
		want, note := Answer("gai-gw", kind == "i-connect", q)
		if strings.HasSuffix(note, "missing") {
			t.Fatal(note)
		}
		status, got := dialQuery(t, addr, "POST", "/gai-gw/v2.5_"+kind, q)
		if status != 200 || string(got) != string(want) {
			t.Errorf("%s: status %d, %d B, want %d B", kind, status, len(got), len(want))
		}
	}
	if status, _ := dialQuery(t, addr, "GET", "/gai-gw/nothing", nil); status != 404 {
		t.Errorf("unknown path: status %d", status)
	}
	status, got := dialQuery(t, addr, "GET", "/00000020/CRS-top.jsp", nil)
	if status != 200 || string(got) != "login /CRS-top.jsp" {
		t.Errorf("login proxy: status %d %q", status, got)
	}
}
