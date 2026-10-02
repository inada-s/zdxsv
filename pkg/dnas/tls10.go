package dnas

// Minimal TLS 1.0 server for the PS2 DNAS client, which opens with an
// SSLv2-format ClientHello (crypto/tls cannot parse it). RSA key exchange,
// TLS_RSA_WITH_RC4_128_MD5 / TLS_RSA_WITH_RC4_128_SHA only.

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/rsa"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
)

const (
	recCCS       = 20
	recAlert     = 21
	recHandshake = 22
	recAppData   = 23

	suiteRC4MD5 = 0x0004
	suiteRC4SHA = 0x0005
)

type tlsConfig struct {
	certs [][]byte // DER, leaf first
	key   *rsa.PrivateKey
}

type halfConn struct {
	rc4    *rc4.Cipher
	macKey []byte
	newMac func() hash.Hash
	seq    uint64
}

func (h *halfConn) mac(typ byte, data []byte) []byte {
	m := hmac.New(h.newMac, h.macKey)
	var hdr [13]byte
	binary.BigEndian.PutUint64(hdr[:8], h.seq)
	hdr[8] = typ
	hdr[9], hdr[10] = 3, 1
	binary.BigEndian.PutUint16(hdr[11:], uint16(len(data)))
	m.Write(hdr[:])
	m.Write(data)
	return m.Sum(nil)
}

type tlsConn struct {
	net.Conn
	cfg     *tlsConfig
	in, out *halfConn
	hs      bytes.Buffer // handshake transcript
	rbuf    []byte       // decrypted app data not yet read
	suite   uint16
}

func prfHash(newH func() hash.Hash, secret, seed []byte, out []byte) {
	a := seed
	for n := 0; n < len(out); {
		m := hmac.New(newH, secret)
		m.Write(a)
		a = m.Sum(nil)
		m = hmac.New(newH, secret)
		m.Write(a)
		m.Write(seed)
		n += copy(out[n:], m.Sum(nil))
	}
}

// prf10 is the TLS 1.0 PRF: P_MD5(S1) xor P_SHA1(S2).
func prf10(secret []byte, label string, seed []byte, n int) []byte {
	ls := append([]byte(label), seed...)
	h := (len(secret) + 1) / 2
	a, b := make([]byte, n), make([]byte, n)
	prfHash(md5.New, secret[:h], ls, a)
	prfHash(sha1.New, secret[len(secret)-h:], ls, b)
	for i := range a {
		a[i] ^= b[i]
	}
	return a
}

func (c *tlsConn) readRecord() (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(c.Conn, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[3:]))
	body := make([]byte, n)
	if _, err := io.ReadFull(c.Conn, body); err != nil {
		return 0, nil, err
	}
	if c.in != nil {
		c.in.rc4.XORKeyStream(body, body)
		ms := c.in.newMac().Size()
		if len(body) < ms {
			return 0, nil, errors.New("short record")
		}
		data, got := body[:len(body)-ms], body[len(body)-ms:]
		if !hmac.Equal(got, c.in.mac(hdr[0], data)) {
			return 0, nil, errors.New("bad record mac")
		}
		c.in.seq++
		body = data
	}
	return hdr[0], body, nil
}

func (c *tlsConn) writeRecord(typ byte, data []byte) error {
	for len(data) > 0 || typ != recAppData {
		n := len(data)
		if n > 16384 {
			n = 16384
		}
		frag := append([]byte(nil), data[:n]...)
		if c.out != nil {
			frag = append(frag, c.out.mac(typ, frag)...)
			c.out.rc4.XORKeyStream(frag, frag)
			c.out.seq++
		}
		rec := []byte{typ, 3, 1, byte(len(frag) >> 8), byte(len(frag))}
		if _, err := c.Conn.Write(append(rec, frag...)); err != nil {
			return err
		}
		data = data[n:]
		if typ != recAppData {
			break
		}
	}
	return nil
}

func hsMsg(typ byte, body []byte) []byte {
	n := len(body)
	return append([]byte{typ, byte(n >> 16), byte(n >> 8), byte(n)}, body...)
}

// readHandshake returns one handshake message (type, body), appending it to the transcript.
func (c *tlsConn) readHandshake(buf *[]byte) (byte, []byte, error) {
	msgLen := func(b []byte) int { return 4 + (int(b[1])<<16 | int(b[2])<<8 | int(b[3])) }
	for len(*buf) < 4 || len(*buf) < msgLen(*buf) {
		typ, body, err := c.readRecord()
		if err != nil {
			return 0, nil, err
		}
		if typ != recHandshake {
			return 0, nil, fmt.Errorf("want handshake record, got %d %x", typ, body)
		}
		*buf = append(*buf, body...)
	}
	b := *buf
	n := int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	msg := b[:4+n]
	*buf = b[4+n:]
	c.hs.Write(msg)
	return msg[0], msg[4:], nil
}

func (c *tlsConn) finished(master []byte, label string) []byte {
	m, s := md5.Sum(c.hs.Bytes()), sha1.Sum(c.hs.Bytes())
	return prf10(master, label, append(m[:], s[:]...), 12)
}

func (c *tlsConn) handshake() error {
	// SSLv2-format ClientHello: [0x80|len_hi len_lo] 01 ver(2) cslen(2) sidlen(2) chlen(2) cs sid challenge
	var h2 [2]byte
	if _, err := io.ReadFull(c.Conn, h2[:]); err != nil {
		return err
	}
	if h2[0]&0x80 == 0 {
		return fmt.Errorf("not an SSLv2 hello: %x", h2)
	}
	body := make([]byte, int(h2[0]&0x7f)<<8|int(h2[1]))
	if _, err := io.ReadFull(c.Conn, body); err != nil {
		return err
	}
	if len(body) < 9 || body[0] != 1 {
		return fmt.Errorf("bad v2 hello %x", body)
	}
	csl := int(binary.BigEndian.Uint16(body[3:]))
	sidl := int(binary.BigEndian.Uint16(body[5:]))
	chl := int(binary.BigEndian.Uint16(body[7:]))
	if 9+csl+sidl+chl > len(body) || chl > 32 {
		return errors.New("bad v2 hello lengths")
	}
	cs := body[9 : 9+csl]
	challenge := body[9+csl+sidl : 9+csl+sidl+chl]
	c.hs.Write(body)
	offered := map[uint16]bool{}
	for i := 0; i+3 <= len(cs); i += 3 {
		if cs[i] == 0 {
			offered[uint16(cs[i+1])<<8|uint16(cs[i+2])] = true
		}
	}
	switch {
	case offered[suiteRC4MD5]:
		c.suite = suiteRC4MD5
	case offered[suiteRC4SHA]:
		c.suite = suiteRC4SHA
	default:
		return fmt.Errorf("no common suite in %x", cs)
	}
	clientRandom := make([]byte, 32)
	copy(clientRandom[32-chl:], challenge)
	serverRandom := make([]byte, 32)
	rand.Read(serverRandom)

	sh := []byte{3, 1}
	sh = append(sh, serverRandom...)
	sh = append(sh, 0, byte(c.suite>>8), byte(c.suite), 0)
	var certs []byte
	for _, der := range c.cfg.certs {
		certs = append(certs, byte(len(der)>>16), byte(len(der)>>8), byte(len(der)))
		certs = append(certs, der...)
	}
	certs = append([]byte{byte(len(certs) >> 16), byte(len(certs) >> 8), byte(len(certs))}, certs...)
	var flight []byte
	for _, m := range [][]byte{hsMsg(2, sh), hsMsg(11, certs), hsMsg(14, nil)} {
		c.hs.Write(m)
		flight = append(flight, m...)
	}
	if err := c.writeRecord(recHandshake, flight); err != nil {
		return err
	}

	var buf []byte
	typ, cke, err := c.readHandshake(&buf)
	if err != nil {
		return err
	}
	if typ != 16 {
		return fmt.Errorf("want ClientKeyExchange, got %d", typ)
	}
	enc := cke
	if len(cke) == c.cfg.key.Size()+2 {
		enc = cke[2:]
	}
	pre := make([]byte, 48)
	rand.Read(pre) // Bleichenbacher-style: random premaster on failure
	if err := rsa.DecryptPKCS1v15SessionKey(nil, c.cfg.key, enc, pre); err != nil {
		return err
	}
	master := prf10(pre, "master secret", append(append([]byte{}, clientRandom...), serverRandom...), 48)
	macLen := 16
	newMac := md5.New
	if c.suite == suiteRC4SHA {
		macLen, newMac = 20, sha1.New
	}
	kb := prf10(master, "key expansion", append(append([]byte{}, serverRandom...), clientRandom...), 2*macLen+32)
	cMac, sMac := kb[:macLen], kb[macLen:2*macLen]
	cKey, sKey := kb[2*macLen:2*macLen+16], kb[2*macLen+16:2*macLen+32]

	if t, b, err := c.readRecord(); err != nil || t != recCCS {
		return fmt.Errorf("want CCS: %d %x %v", t, b, err)
	}
	ci, _ := rc4.NewCipher(cKey)
	c.in = &halfConn{rc4: ci, macKey: cMac, newMac: newMac}
	want := c.finished(master, "client finished")
	typ, fin, err := c.readHandshake(&buf)
	if err != nil {
		return err
	}
	if typ != 20 || !bytes.Equal(fin, want) {
		return fmt.Errorf("bad client Finished %d %x want %x", typ, fin, want)
	}
	sfin := hsMsg(20, c.finished(master, "server finished"))
	if err := c.writeRecord(recCCS, []byte{1}); err != nil {
		return err
	}
	co, _ := rc4.NewCipher(sKey)
	c.out = &halfConn{rc4: co, macKey: sMac, newMac: newMac}
	return c.writeRecord(recHandshake, sfin)
}

func (c *tlsConn) Read(p []byte) (int, error) {
	for len(c.rbuf) == 0 {
		typ, body, err := c.readRecord()
		if err != nil {
			return 0, err
		}
		switch typ {
		case recAppData:
			c.rbuf = body
		case recAlert:
			return 0, io.EOF
		default:
			return 0, fmt.Errorf("unexpected record %d", typ)
		}
	}
	n := copy(p, c.rbuf)
	c.rbuf = c.rbuf[n:]
	return n, nil
}

func (c *tlsConn) Write(p []byte) (int, error) {
	if err := c.writeRecord(recAppData, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *tlsConn) Close() error {
	if c.out != nil {
		c.writeRecord(recAlert, []byte{1, 0}) // close_notify
	}
	return c.Conn.Close()
}
