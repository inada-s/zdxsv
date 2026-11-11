// The DNAS server of zdxsv, ported from DNASrep
// (github.com/FogNo23/DNASrep, AGPL-3.0 like zdxsv).
//
// The PS2 opens with an SSLv2-format ClientHello and TLS 1.0 RC4 (dnas_tls10.go).
// DNAS queries are answered with canned
// packets (dnas/www/<gateway>/packets/<gameid>_<qrytype>); i-connect answers
// get two 3DES-CBC layers. /00000020/* goes to the zdxsv login pages
// (the game's browser opens the login page through the same host).
package main

import (
	"bufio"
	"bytes"
	"crypto/cipher"
	"crypto/des"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"go.uber.org/zap"
)

// dnas/: DNASrep (8e8dad3) etc/dnas (certificates) and www/dnas/<gateway>/{error.raw,packets}.
// testdata/golden.txt: the PHP DNASrep's answers (testdata/golden.py).
//
//go:embed dnas
var dnasFS embed.FS

// substr is PHP substr for in-range starts: clamped to the end of b.
func substr(b []byte, off, n int) []byte {
	if off >= len(b) {
		return nil
	}
	if off+n > len(b) {
		n = len(b) - off
	}
	return b[off : off+n]
}

// encrypt3 is 3DES-EDE in CBC mode, in place.
func encrypt3(b []byte, off, n int, key, iv []byte) {
	c, err := des.NewTripleDESCipher(key)
	if err != nil {
		panic(err)
	}
	cipher.NewCBCEncrypter(c, iv).CryptBlocks(b[off:off+n], b[off:off+n])
}

var (
	envelopeKey = mustHex("eb711416cb0ab016ae190174b5ce63397b01b91880145e34")
	envelopeIV  = mustHex("c510a6400a9b022f")
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// DnasAnswer returns the answer to query q at gateway gw (gai-gw, us-gw, eu-gw):
// connect = i-connect, else others. note names the packet used, or why error.raw.
func DnasAnswer(gw string, connect bool, q []byte) (ans []byte, note string) {
	errRaw, err := dnasFS.ReadFile(path.Join("dnas/www", gw, "error.raw"))
	if err != nil {
		return nil, "no gateway " + gw
	}
	idOff := 0x1b
	if connect {
		idOff = 0x2c
	}
	fname := hex.EncodeToString(substr(q, idOff, 8)) + "_" + hex.EncodeToString(substr(q, 0, 4))
	p, err := dnasFS.ReadFile(path.Join("dnas/www", gw, "packets", fname))
	if err != nil {
		return errRaw, fname + " missing"
	}
	if connect {
		if len(p) < 0x148 {
			return errRaw, fname + " too short for i-connect"
		}
		c1 := sha1.Sum(substr(q, 0x34, 0x100))
		c2 := sha1.Sum(substr(q, 0x48, 0xec))
		full := append(c2[:], c1[:0xc]...) // k1 k2 k3 iv
		encrypt3(p, 0xc8, 0x20, full[:24], full[24:32])
		encrypt3(p, 0x28, 0x120, envelopeKey, envelopeIV)
	}
	return p, fname
}

func pemBlocks(name string) ([][]byte, error) {
	b, err := dnasFS.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return out, nil
		}
		out = append(out, blk.Bytes)
	}
}

func loadDnasTLS(region string) (*tlsConfig, error) {
	certs, err := pemBlocks("dnas/etc/cert-" + region + ".pem")
	if err != nil {
		return nil, err
	}
	ca, err := pemBlocks("dnas/etc/ca-cert.pem")
	if err != nil {
		return nil, err
	}
	certs = append(certs, ca...)
	keys, err := pemBlocks("dnas/etc/cert-" + region + "-key.pem")
	if err != nil {
		return nil, err
	}
	if len(certs) < 2 || len(keys) != 1 {
		return nil, fmt.Errorf("region %s: %d certs %d keys", region, len(certs), len(keys))
	}
	if key, err := x509.ParsePKCS1PrivateKey(keys[0]); err == nil {
		return &tlsConfig{certs: certs, key: key}, nil
	}
	k8, err := x509.ParsePKCS8PrivateKey(keys[0])
	if err != nil {
		return nil, err
	}
	key, ok := k8.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("region %s: not an RSA key", region)
	}
	return &tlsConfig{certs: certs, key: key}, nil
}

// DnasServer answers DNAS on the PS2's TLS dialect.
type DnasServer struct {
	tls *tlsConfig
	mux *http.ServeMux
}

// NewDnasServer: region selects the certificate (jp, us, eu); login serves the zdxsv login
// pages behind /00000020/.
func NewDnasServer(region string, login http.Handler) (*DnasServer, error) {
	cfg, err := loadDnasTLS(region)
	if err != nil {
		return nil, err
	}
	s := &DnasServer{tls: cfg, mux: http.NewServeMux()}
	s.mux.HandleFunc("/", handleDNAS)
	s.mux.Handle("/00000020/", http.StripPrefix("/00000020", login))
	return s, nil
}

func handleDNAS(w http.ResponseWriter, r *http.Request) {
	q, _ := io.ReadAll(r.Body)
	dir, base := path.Split(r.URL.Path)
	gw := path.Base(dir)
	var ans []byte
	var note string
	switch {
	case strings.HasSuffix(base, "i-connect"):
		ans, note = DnasAnswer(gw, true, q)
	case strings.HasSuffix(base, "others"):
		ans, note = DnasAnswer(gw, false, q)
	}
	if ans == nil {
		logger.Info("dnas 404", zap.String("method", r.Method), zap.Stringer("url", r.URL))
		http.NotFound(w, r)
		return
	}
	logger.Info("dnas request", zap.String("method", r.Method), zap.String("path", r.URL.Path), zap.Int("q", len(q)), zap.Any("note", note), zap.Int("bytes", len(ans)))
	w.Header().Set("Content-Type", "image/gif")
	w.Write(ans)
}

// ListenAndServe listens on addr (e.g. ":443") and serves until an accept error.
func (s *DnasServer) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

func (s *DnasServer) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.serveConn(c)
	}
}

// ServeConn serves one accepted connection (for the router's Console).
func (s *DnasServer) ServeConn(c net.Conn) { s.serveConn(c) }

func (s *DnasServer) serveConn(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	tc := &tlsConn{Conn: c, cfg: s.tls}
	if err := tc.handshake(); err != nil {
		logger.Info("dnas handshake failed", zap.Stringer("addr", c.RemoteAddr()), zap.Error(err))
		return
	}
	c.SetDeadline(time.Time{})
	s.serveHTTP(tc)
	tc.Close()
}

// serveHTTP answers HTTP/1.x requests on c until it closes. Responses are
// HTTP/1.0 with Content-Length.
func (s *DnasServer) serveHTTP(c net.Conn) {
	br := bufio.NewReader(c)
	for {
		c.SetReadDeadline(time.Now().Add(60 * time.Second))
		req, err := http.ReadRequest(br)
		if err != nil {
			if err != io.EOF {
				logger.Info("dnas read request failed", zap.Stringer("addr", c.RemoteAddr()), zap.Error(err))
			}
			return
		}
		logger.Info("dnas https request", zap.Stringer("addr", c.RemoteAddr()), zap.String("method", req.Method), zap.Stringer("url", req.URL), zap.String("ua", req.UserAgent()))
		rw := &respWriter{h: http.Header{}}
		s.mux.ServeHTTP(rw, req)
		if _, err := c.Write(rw.bytes()); err != nil || req.Close || req.ProtoMinor == 0 {
			return
		}
	}
}

// respWriter buffers one response.
type respWriter struct {
	h      http.Header
	status int
	body   bytes.Buffer
}

func (w *respWriter) Header() http.Header { return w.h }
func (w *respWriter) WriteHeader(s int) {
	if w.status == 0 {
		w.status = s
	}
}
func (w *respWriter) Write(p []byte) (int, error) { w.WriteHeader(200); return w.body.Write(p) }

func (w *respWriter) bytes() []byte {
	w.WriteHeader(200)
	w.h.Set("Content-Length", fmt.Sprint(w.body.Len()))
	w.h.Del("Transfer-Encoding")
	var out bytes.Buffer
	fmt.Fprintf(&out, "HTTP/1.0 %d %s\r\n", w.status, http.StatusText(w.status))
	w.h.Write(&out)
	out.WriteString("\r\n")
	out.Write(w.body.Bytes())
	return out.Bytes()
}

// Client side of the PS2's TLS dialect, for tests and `zdxsv dnascheck`:
// the same 100-byte SSLv2-format ClientHello the PS2 sends (docker/router
// routes on its first bytes), TLS 1.0, TLS_RSA_WITH_RC4_128_MD5.
func (c *tlsConn) clientHandshake() error {
	// v2 hello body: 01 ver(2) cslen(2)=75 sidlen(2)=0 chlen(2)=16, 25 suites, challenge
	challenge := make([]byte, 16)
	rand.Read(challenge)
	body := []byte{1, 3, 1, 0, 75, 0, 0, 0, 16}
	body = append(body, 0, 0, suiteRC4MD5)
	for i := 0; i < 24; i++ {
		body = append(body, 0x01, 0, byte(i+1)) // SSLv2-only suites, never chosen
	}
	body = append(body, challenge...)
	c.hs.Write(body)
	if _, err := c.Conn.Write(append([]byte{0x80, byte(len(body))}, body...)); err != nil {
		return err
	}
	clientRandom := append(make([]byte, 16), challenge...)

	var buf []byte
	typ, sh, err := c.readHandshake(&buf)
	if err != nil {
		return err
	}
	if typ != 2 || len(sh) < 38 || int(sh[34])+37 > len(sh) {
		return fmt.Errorf("bad ServerHello %d %x", typ, sh)
	}
	serverRandom := sh[2:34]
	if suite := uint16(sh[35+int(sh[34])])<<8 | uint16(sh[36+int(sh[34])]); suite != suiteRC4MD5 {
		return fmt.Errorf("server chose suite %04x", suite)
	}
	typ, certs, err := c.readHandshake(&buf)
	if err != nil {
		return err
	}
	if typ != 11 || len(certs) < 6 {
		return fmt.Errorf("bad Certificate %d", typ)
	}
	n := int(certs[3])<<16 | int(certs[4])<<8 | int(certs[5])
	if 6+n > len(certs) {
		return errors.New("bad Certificate length")
	}
	leaf, err := x509.ParseCertificate(certs[6 : 6+n])
	if err != nil {
		return err
	}
	pub, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("certificate key is not RSA")
	}
	if typ, _, err := c.readHandshake(&buf); err != nil || typ != 14 {
		return fmt.Errorf("want ServerHelloDone: %d %v", typ, err)
	}

	pre := make([]byte, 48)
	rand.Read(pre[2:])
	pre[0], pre[1] = 3, 1
	enc, err := rsa.EncryptPKCS1v15(rand.Reader, pub, pre)
	if err != nil {
		return err
	}
	cke := hsMsg(16, append([]byte{byte(len(enc) >> 8), byte(len(enc))}, enc...))
	c.hs.Write(cke)
	if err := c.writeRecord(recHandshake, cke); err != nil {
		return err
	}
	master := prf10(pre, "master secret", append(append([]byte{}, clientRandom...), serverRandom...), 48)
	kb := prf10(master, "key expansion", append(append([]byte{}, serverRandom...), clientRandom...), 2*16+32)
	cMac, sMac, cKey, sKey := kb[:16], kb[16:32], kb[32:48], kb[48:64]

	if err := c.writeRecord(recCCS, []byte{1}); err != nil {
		return err
	}
	co, _ := rc4.NewCipher(cKey)
	c.out = &halfConn{rc4: co, macKey: cMac, newMac: md5.New}
	cfin := hsMsg(20, c.finished(master, "client finished"))
	c.hs.Write(cfin)
	if err := c.writeRecord(recHandshake, cfin); err != nil {
		return err
	}
	if t, b, err := c.readRecord(); err != nil || t != recCCS {
		return fmt.Errorf("want CCS: %d %x %v", t, b, err)
	}
	ci, _ := rc4.NewCipher(sKey)
	c.in = &halfConn{rc4: ci, macKey: sMac, newMac: md5.New}
	want := c.finished(master, "server finished")
	typ, fin, err := c.readHandshake(&buf)
	if err != nil {
		return err
	}
	if typ != 20 || !bytes.Equal(fin, want) {
		return fmt.Errorf("bad server Finished %d %x want %x", typ, fin, want)
	}
	return nil
}

// DnasQuery sends one HTTP/1.0 request over conn as the PS2 does and returns
// the response status and body.
func DnasQuery(conn net.Conn, method, urlPath string, body []byte) (int, []byte, error) {
	c := &tlsConn{Conn: conn}
	defer c.Close()
	if err := c.clientHandshake(); err != nil {
		return 0, nil, fmt.Errorf("handshake: %w", err)
	}
	req := fmt.Sprintf("%s %s HTTP/1.0\r\nHost: gate1.jp.dnas.playstation.org\r\nContent-Length: %d\r\n\r\n", method, urlPath, len(body))
	if _, err := c.Write(append([]byte(req), body...)); err != nil {
		return 0, nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

// newDNASRouter is the 443 front end: game consoles get the DNAS server (with the login
// pages behind /00000020/), other TLS goes to ZDXSV_DNAS_HTTPS_BACKEND.
func newDNASRouter(login http.Handler) (*Router, error) {
	s, err := NewDnasServer(Conf.DNAS.Region, login)
	if err != nil {
		return nil, err
	}
	return &Router{Console: s.ServeConn, HTTPSBackend: Conf.DNAS.HTTPSBackend}, nil
}

// mainDNASCheck queries a DNAS server at addr as the game's PS2 does
// (i-connect, then others), then the login page proxy (/00000020/health), and
// exits 1 unless all three answer as expected. With pins it goes through the TLS port addr.
func mainDNASCheck(addr string, pins []string) {
	ok := true
	for _, q := range []struct {
		kind  string
		query []byte
	}{
		{"i-connect", dnasCheckQuery(0x2c, "01180000", "c9acca0f30eecbf7", 308)},
		{"others", dnasCheckQuery(0x1b, "01188001", "bdfe36f1209991ec", 184)},
	} {
		want, note := DnasAnswer("gai-gw", q.kind == "i-connect", q.query)
		c, err := dialCheck(addr, pins)
		if err == nil {
			c.SetDeadline(time.Now().Add(20 * time.Second))
			var status int
			var got []byte
			status, got, err = DnasQuery(c, "POST", "/gai-gw/v2.5_"+q.kind, q.query)
			if err == nil && (status != 200 || !bytes.Equal(got, want)) {
				err = fmt.Errorf("status %d, %d B, want 200 and %s (%d B)", status, len(got), note, len(want))
			}
		}
		if err != nil {
			ok = false
			fmt.Println("FAIL", q.kind, err)
		} else {
			fmt.Println("OK", q.kind, note, len(want), "B")
		}
	}
	c, err := dialCheck(addr, pins)
	if err == nil {
		c.SetDeadline(time.Now().Add(20 * time.Second))
		var status int
		status, _, err = DnasQuery(c, "GET", "/00000020/health", nil)
		if err == nil && status != 200 {
			err = fmt.Errorf("status %d", status)
		}
	}
	if err != nil {
		ok = false
		fmt.Println("FAIL login proxy /00000020/health", err)
	} else {
		fmt.Println("OK login proxy /00000020/health")
	}
	if !ok {
		os.Exit(1)
	}
}

// dnasCheckQuery: a query of n bytes for game id gid and query type qt (hex).
func dnasCheckQuery(idOff int, qt, gid string, n int) []byte {
	q := make([]byte, n)
	for i := range q {
		q[i] = byte(i * 7)
	}
	t, _ := hex.DecodeString(qt)
	id, _ := hex.DecodeString(gid)
	copy(q[0:4], t)
	copy(q[idOff:], id)
	return q
}

func dialCheck(addr string, pins []string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if len(pins) == 0 {
		return d.Dial("tcp", addr)
	}
	return tls.DialWithDialer(d, "tcp", addr, TLSClientConfig(pins...))
}
