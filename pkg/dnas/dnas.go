// Package dnas is the DNAS server of zdxsv, replacing the Apache/PHP DNASrep
// (github.com/FogNo23/DNASrep, AGPL-3.0 like zdxsv) that docker/legacyweb ran.
//
// The PS2 opens with an SSLv2-format ClientHello and TLS 1.0 RC4 (tls10.go).
// DNAS queries are answered as DNASrep's connect.php / others.php do: canned
// packets (data/www/<gateway>/packets/<gameid>_<qrytype>); i-connect answers
// get two 3DES-CBC layers. /00000020/* is proxied to the zdxsv login server
// (the game's browser opens the login page through the same host).
package dnas

import (
	"bufio"
	"bytes"
	"crypto/cipher"
	"crypto/des"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/golang/glog"
)

// data: DNASrep (8e8dad3) etc/dnas (certificates) and www/dnas/<gateway>/{error.raw,packets}.
// testdata/golden.txt: the PHP DNASrep's answers (testdata/golden.py).
//
//go:embed data
var data embed.FS

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

// encrypt3 is DNASrep's encrypt3: 3DES-EDE in CBC mode, in place.
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

// Answer returns the answer to query q at gateway gw (gai-gw, us-gw, eu-gw):
// connect = i-connect, else others. note names the packet used, or why error.raw.
func Answer(gw string, connect bool, q []byte) (ans []byte, note string) {
	errRaw, err := data.ReadFile(path.Join("data/www", gw, "error.raw"))
	if err != nil {
		return nil, "no gateway " + gw
	}
	idOff := 0x1b
	if connect {
		idOff = 0x2c
	}
	fname := hex.EncodeToString(substr(q, idOff, 8)) + "_" + hex.EncodeToString(substr(q, 0, 4))
	p, err := data.ReadFile(path.Join("data/www", gw, "packets", fname))
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
	b, err := data.ReadFile(name)
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

func loadTLS(region string) (*tlsConfig, error) {
	certs, err := pemBlocks("data/etc/cert-" + region + ".pem")
	if err != nil {
		return nil, err
	}
	ca, err := pemBlocks("data/etc/ca-cert.pem")
	if err != nil {
		return nil, err
	}
	certs = append(certs, ca...)
	keys, err := pemBlocks("data/etc/cert-" + region + "-key.pem")
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

// Server answers DNAS on the PS2's TLS dialect.
type Server struct {
	tls *tlsConfig
	mux *http.ServeMux
}

// NewServer: region selects the certificate (jp, us, eu); loginAddr (host:port)
// is the zdxsv login server behind /00000020/.
func NewServer(region, loginAddr string) (*Server, error) {
	cfg, err := loadTLS(region)
	if err != nil {
		return nil, err
	}
	s := &Server{tls: cfg, mux: http.NewServeMux()}
	s.mux.HandleFunc("/", handleDNAS)
	u, err := url.Parse("http://" + loginAddr + "/")
	if err != nil {
		return nil, err
	}
	s.mux.Handle("/00000020/", http.StripPrefix("/00000020", httputil.NewSingleHostReverseProxy(u)))
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
		ans, note = Answer(gw, true, q)
	case strings.HasSuffix(base, "others"):
		ans, note = Answer(gw, false, q)
	}
	if ans == nil {
		glog.Infof("dnas 404 %s %s", r.Method, r.URL)
		http.NotFound(w, r)
		return
	}
	glog.Infof("dnas %s %s q=%d -> %s (%d B)", r.Method, r.URL.Path, len(q), note, len(ans))
	w.Header().Set("Content-Type", "image/gif")
	w.Write(ans)
}

// ListenAndServe listens on addr (e.g. ":443") and serves until an accept error.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

func (s *Server) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.serveConn(c)
	}
}

func (s *Server) serveConn(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	tc := &tlsConn{Conn: c, cfg: s.tls}
	if err := tc.handshake(); err != nil {
		glog.Infof("dnas %s handshake: %v", c.RemoteAddr(), err)
		return
	}
	c.SetDeadline(time.Time{})
	s.serveHTTP(tc)
	tc.Close()
}

// serveHTTP answers HTTP/1.x requests on c until it closes. Responses are
// HTTP/1.0 with Content-Length (as Apache answered with force-response-1.0).
func (s *Server) serveHTTP(c net.Conn) {
	br := bufio.NewReader(c)
	for {
		c.SetReadDeadline(time.Now().Add(60 * time.Second))
		req, err := http.ReadRequest(br)
		if err != nil {
			if err != io.EOF {
				glog.Infof("dnas %s read request: %v", c.RemoteAddr(), err)
			}
			return
		}
		glog.Infof("dnas %s %s %s ua=%q", c.RemoteAddr(), req.Method, req.URL, req.UserAgent())
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
