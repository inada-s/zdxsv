package main

import (
	"bytes"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

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
