package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"time"

	"zdxsv/pkg/config"
	"zdxsv/pkg/dnas"

	"github.com/golang/glog"
)

func mainDNAS() {
	s, err := dnas.NewServer(config.Conf.DNAS.Region, config.Conf.Login.Addr)
	if err != nil {
		glog.Fatalln(err)
	}
	glog.Infoln("dnas listen", config.Conf.DNAS.Addr, "login", config.Conf.Login.Addr)
	glog.Fatalln(s.ListenAndServe(config.Conf.DNAS.Addr))
}

// mainDNASCheck queries a DNAS server at addr as the game's PS2 does
// (i-connect, then others), then the login page proxy (/00000020/health), and
// exits 1 unless all three answer as expected.
func mainDNASCheck(addr string) {
	ok := true
	for _, q := range []struct {
		kind  string
		query []byte
	}{
		{"i-connect", dnasQuery(0x2c, "01180000", "c9acca0f30eecbf7", 308)},
		{"others", dnasQuery(0x1b, "01188001", "bdfe36f1209991ec", 184)},
	} {
		want, note := dnas.Answer("gai-gw", q.kind == "i-connect", q.query)
		c, err := net.DialTimeout("tcp", addr, 10*time.Second)
		if err == nil {
			c.SetDeadline(time.Now().Add(20 * time.Second))
			var status int
			var got []byte
			status, got, err = dnas.Query(c, "POST", "/gai-gw/v2.5_"+q.kind, q.query)
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
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err == nil {
		c.SetDeadline(time.Now().Add(20 * time.Second))
		var status int
		status, _, err = dnas.Query(c, "GET", "/00000020/health", nil)
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

// dnasQuery: a query of n bytes for game id gid and query type qt (hex).
func dnasQuery(idOff int, qt, gid string, n int) []byte {
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
