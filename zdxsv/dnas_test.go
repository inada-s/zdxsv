package main

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

// realDnasData is the DNASrep checkout in ZDXSV_DNAS_DATA_DIR (scripts/dnasrep.sh fetches it).
func realDnasData(t *testing.T) *dnasData {
	dir := os.Getenv("ZDXSV_DNAS_DATA_DIR")
	if dir == "" {
		t.Skip("ZDXSV_DNAS_DATA_DIR not set: run scripts/dnasrep.sh")
	}
	d, err := loadDnasData(dir)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// fakeDnasData is a DNASrep-shaped directory with a generated certificate, a dummy
// envelope key and one packet (c9acca0f30eecbf7_01180000).
func fakeDnasData(t *testing.T) string {
	dir := t.TempDir()
	write := func(name string, b []byte) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gate1.jp.dnas.playstation.org"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	write("etc/dnas/cert-jp.pem", cert)
	write("etc/dnas/ca-cert.pem", cert)
	write("etc/dnas/cert-jp-key.pem", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	write("www/dnas/gai-gw/connect.php", []byte(`    $packet = encrypt3($packet, 0x28, 0x120, pack("H*", "0123456789abcdef"), pack("H*", "fedcba9876543210"), pack("H*", "89abcdef01234567"), pack("H*", "0011223344556677"));`))
	write("www/dnas/gai-gw/error.raw", []byte("error"))
	packet := make([]byte, 0x148)
	for i := range packet {
		packet[i] = byte(i)
	}
	write("www/dnas/gai-gw/packets/c9acca0f30eecbf7_01180000", packet)
	return dir
}

func TestDnasData(t *testing.T) {
	if _, err := loadDnasData(""); err == nil {
		t.Error("empty dir: no error")
	}
	if _, err := loadDnasData(t.TempDir()); err == nil {
		t.Error("dir without connect.php: no error")
	}
	d, err := loadDnasData(fakeDnasData(t))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(d.envelopeKey) != "0123456789abcdeffedcba987654321089abcdef01234567" || hex.EncodeToString(d.envelopeIV) != "0011223344556677" {
		t.Errorf("envelope key %x iv %x", d.envelopeKey, d.envelopeIV)
	}
	q := query("gai-gw", "others", "c9acca0f30eecbf7_01180000", 184)
	if a, note := d.Answer("gai-gw", false, q); len(a) != 0x148 || note != "c9acca0f30eecbf7_01180000" {
		t.Errorf("others: %d B %q", len(a), note)
	}
	if a, note := d.Answer("gai-gw", false, query("gai-gw", "others", "0000000000000000_01188001", 184)); string(a) != "error" {
		t.Errorf("unknown game: %q %q", a, note)
	}
	for _, gw := range []string{"..", "us-gw", "etc"} {
		if a, _ := d.Answer(gw, false, q); a != nil {
			t.Errorf("gateway %q: %d B", gw, len(a))
		}
	}
}

// TestAnswerGolden: Answer gives DNASrep's (PHP) answers byte for byte.
func TestAnswerGolden(t *testing.T) {
	d := realDnasData(t)
	gs := readGolden(t)
	if len(gs) < 400 {
		t.Fatalf("only %d golden cases", len(gs))
	}
	for _, g := range gs {
		a, note := d.Answer(g.gw, g.kind == "i-connect", query(g.gw, g.kind, g.packet, g.qlen))
		sum := sha256.Sum256(a)
		if len(a) != g.alen || hex.EncodeToString(sum[:]) != g.sha {
			t.Errorf("%s %s %s q=%d: got %d B (%s), want %d B", g.gw, g.kind, g.packet, g.qlen, len(a), note, g.alen)
		}
	}
}

func startServer(t *testing.T, d *dnasData, login http.Handler) string {
	s, err := NewDnasServer(d, "jp", login)
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
	status, b, err := DnasQuery(c, method, urlPath, body)
	if err != nil {
		t.Fatal(err)
	}
	return status, b
}

// TestServer: the PS2 dialect end to end (hello, TLS 1.0 RC4-MD5, HTTP/1.0) for
// both query kinds, a 404, and the login pages.
func TestServer(t *testing.T) {
	d, err := loadDnasData(fakeDnasData(t))
	if err != nil {
		t.Fatal(err)
	}
	addr := startServer(t, d, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "login %s", r.URL.Path)
	}))

	for _, kind := range []string{"i-connect", "others"} {
		q := query("gai-gw", kind, "c9acca0f30eecbf7_01180000", 308)
		want, note := d.Answer("gai-gw", kind == "i-connect", q)
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
	for _, code := range []string{gameCodeZGVSZ, gameCodeAEUG} {
		status, got := dialQuery(t, addr, "GET", "/"+code+"/CRS-top.jsp", nil)
		if status != 200 || string(got) != "login /CRS-top.jsp" {
			t.Errorf("login pages %s: status %d %q", code, status, got)
		}
	}
}
