package main

import (
	"context"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

type PS2Server struct {
	chNewConn chan *PS2Conn
}

type PS2Conn struct {
	conn *net.TCPConn
}

func NewPS2Server() *PS2Server {
	return &PS2Server{
		chNewConn: make(chan *PS2Conn),
	}
}

func (sv *PS2Server) Listen(addr string) error {
	tcpAddr, err := net.ResolveTCPAddr("tcp4", addr)
	if err != nil {
		return err
	}
	ln, err := net.ListenTCP("tcp4", tcpAddr)
	if err != nil {
		return err
	}
	defer ln.Close()
	for {
		conn, err := ln.AcceptTCP()
		if err != nil {
			return err
		}
		sv.chNewConn <- &PS2Conn{conn}
	}
	return nil
}

func (sv *PS2Server) Accept() <-chan *PS2Conn {
	return sv.chNewConn
}

func (c *PS2Conn) Serve(ctx context.Context, onRead func([]byte)) error {
	buf := make([]byte, 1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			n, err := c.conn.Read(buf)
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			tmp := make([]byte, n)
			copy(tmp, buf[:n])
			onRead(tmp)
		}
	}
}

func (c *PS2Conn) Write(data []byte) {
	for sum := 0; sum < len(data); {
		n, err := c.conn.Write(data[sum:])
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			log.Println("writeTCP error", err)
			c.Close() // Force close TCP
			return
		}
		sum += n
	}
}

func (c *PS2Conn) Close() error {
	return c.conn.Close()
}
