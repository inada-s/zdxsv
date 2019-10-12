package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

func main() {
	flag.Parse()
	log.Fatal(listenAndServe(":443"))
}

func listenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("create listener: %s", err)
	}

	for {
		c, err := l.Accept()
		if err != nil {
			return fmt.Errorf("accept new conn: %s", err)
		}

		conn := &Conn{
			TCPConn: c.(*net.TCPConn),
		}
		go conn.proxy()
	}
}

func isGameConsoleConnection(r io.Reader) bool {
	var hdr struct {
		Type         uint8
		Major, Minor uint8
		Length       uint16
	}

	if err := binary.Read(r, binary.BigEndian, &hdr); err != nil {
		return false
	}

	log.Println("hdr:", hdr)

	// FIXME: more strict validation.
	if hdr.Type == 128 && hdr.Major == 100 && hdr.Minor == 1 && hdr.Length == 769 {
		return true
	}

	return false
}

func (c *Conn) proxy() {
	defer c.Close()

	if err := c.SetReadDeadline(time.Now().Add(*helloTimeout)); err != nil {
		c.internalError("Setting read deadline for ClientHello: %s", err)
		return
	}

	var (
		err    error
		tmpBuf bytes.Buffer
	)

	isGameConsole := isGameConsoleConnection(io.TeeReader(c, &tmpBuf))

	if err = c.SetReadDeadline(time.Time{}); err != nil {
		c.internalError("Clearing read deadline for ClientHello: %s", err)
		return
	}

	c.backend = "https-portal:443"
	if isGameConsole {
		c.backend = "legacyweb:443"
	}

	backend, err := net.DialTimeout("tcp", c.backend, 10*time.Second)
	if err != nil {
		c.internalError("failed to dial backend %q: %s", c.backend, err)
		return
	}
	defer backend.Close()

	c.backendConn = backend.(*net.TCPConn)

	if !isGameConsole {
		remote := c.TCPConn.RemoteAddr().(*net.TCPAddr)
		local := c.TCPConn.LocalAddr().(*net.TCPAddr)
		family := "TCP6"
		if remote.IP.To4() != nil {
			family = "TCP4"
		}
		if _, err := fmt.Fprintf(c.backendConn, "PROXY %s %s %s %d %d\r\n", family, remote.IP, local.IP, remote.Port, local.Port); err != nil {
			c.internalError("failed to send PROXY header to %q: %s", c.backend, err)
			return
		}
	}

	// Replay the piece of the handshake we had to read to do the
	// routing, then blindly proxy any other bytes.
	n, err := io.Copy(c.backendConn, &tmpBuf)
	log.Println("written ", n, "bytes")
	if err != nil {
		c.internalError("failed to replay handshake to %q: %s", c.backend, err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go proxy(&wg, c.TCPConn, c.backendConn)
	go proxy(&wg, c.backendConn, c.TCPConn)
	wg.Wait()
}
