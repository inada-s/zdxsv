// Follwoing parts of this file are ported from the tcpproxy project.
// - The Conn type
// - The function proxy
// These are distributed in the Apache License 2.0
// cf. https://github.com/google/tcpproxy
//
// Copyright 2016 Google Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

var (
	helloTimeout = flag.Duration("hello-timeout", 3*time.Second, "how long to wait for the TLS ClientHello")
)

// A Conn handles the TLS proxying of one user connection.
type Conn struct {
	*net.TCPConn

	backend     string
	tlsMinor    int
	backendConn *net.TCPConn
}

func (c *Conn) logf(msg string, args ...interface{}) {
	msg = fmt.Sprintf(msg, args...)
	log.Printf("%s <> %s: %s", c.RemoteAddr(), c.LocalAddr(), msg)
}

func (c *Conn) abort(alert byte, msg string, args ...interface{}) {
	c.logf(msg, args...)
	alertMsg := []byte{21, 3, byte(c.tlsMinor), 0, 2, 2, alert}

	if err := c.SetWriteDeadline(time.Now().Add(*helloTimeout)); err != nil {
		c.logf("error while setting write deadline during abort: %s", err)
		// Do NOT send the alert if we can't set a write deadline,
		// that could result in leaking a connection for an extended
		// period.
		return
	}

	if _, err := c.Write(alertMsg); err != nil {
		c.logf("error while sending alert: %s", err)
	}
}

func (c *Conn) internalError(msg string, args ...interface{}) {
	c.abort(80, msg, args...)
}

func (c *Conn) sniFailed(msg string, args ...interface{}) {
	c.abort(112, msg, args...)
}

func proxy(wg *sync.WaitGroup, a, b net.Conn) {
	defer wg.Done()
	atcp, btcp := a.(*net.TCPConn), b.(*net.TCPConn)
	if _, err := io.Copy(atcp, btcp); err != nil {
		log.Printf("%s<>%s -> %s<>%s: %s", atcp.RemoteAddr(), atcp.LocalAddr(), btcp.LocalAddr(), btcp.RemoteAddr(), err)
	}
	btcp.CloseWrite()
	atcp.CloseRead()
}
