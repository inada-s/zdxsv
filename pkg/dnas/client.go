package dnas

// Client side of the PS2's TLS dialect, for tests and `zdxsv dnascheck`:
// the same 100-byte SSLv2-format ClientHello the PS2 sends (docker/router
// routes on its first bytes), TLS 1.0, TLS_RSA_WITH_RC4_128_MD5.

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

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

// Query sends one HTTP/1.0 request over conn as the PS2 does and returns
// the response status and body.
func Query(conn net.Conn, method, urlPath string, body []byte) (int, []byte, error) {
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
