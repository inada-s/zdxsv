package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
)

var loadTLS = sync.OnceValues(func() (*tls.Config, error) {
	return TLSServerConfig(Conf.TLS.CertFile, Conf.TLS.KeyFile)
})

// tlsListen opens the TLS port addr (like the game ports, on all interfaces) for the
// server name, or returns nil when no certificate is configured. Errors end the process.
func tlsListen(name, addr string) net.Listener {
	if c := Conf.TLS; c.CertFile == "" && c.KeyFile == "" {
		return nil
	}
	cfg, err := loadTLS()
	if err != nil {
		logger.Fatal("tls", zap.Error(err))
	}
	l, err := net.Listen("tcp", stripHost(addr))
	if err != nil {
		logger.Fatal("tls", zap.String("name", name), zap.Error(err))
	}
	logger.Info("tls listen", zap.String("name", name), zap.Any("addr", addr))
	return NewTLSListener(l, cfg)
}

// mainTLSCheck connects to the TLS port addr, prints the pin of each certificate the
// server sends and the size of its first message, and exits 1 when no certificate
// matches one of pins or the server sends nothing within 5 s.
func mainTLSCheck(addr string, pins []string) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	c, err := tls.DialWithDialer(d, "tcp", addr, TLSClientConfig(pins...))
	if err != nil {
		fmt.Println("FAIL", addr, err)
		os.Exit(1)
	}
	defer c.Close()
	for _, cert := range c.ConnectionState().PeerCertificates {
		fmt.Println("chain", TLSPin(cert), cert.Subject.CommonName)
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	n, err := c.Read(buf)
	if n == 0 {
		fmt.Println("FAIL", addr, "first message:", err)
		os.Exit(1)
	}
	fmt.Println("OK", addr, "first message", n, "B")
}
