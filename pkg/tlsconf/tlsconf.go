// Package tlsconf serves the emulators' TLS ports. The emulator trusts no CA and checks
// no hostname or expiry: it accepts a server certificate that has a pinned public key (Pin)
// or is signed by one. The server sends its certificate and the zdxsv CA's (scripts/tls-keys.sh).
package tlsconf

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/golang/glog"
)

// HandshakeTimeout bounds a client's TLS handshake; Accept never waits for one.
const HandshakeTimeout = 10 * time.Second

// ServerConfig loads certFile (PEM: the server certificate, then the CA certificate) and
// keyFile (the server key).
func ServerConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	if len(cert.Certificate) < 2 {
		return nil, fmt.Errorf("%s: %d certificate(s), want the server certificate and the CA certificate", certFile, len(cert.Certificate))
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// Pin is the hex SHA-256 of the certificate's SubjectPublicKeyInfo.
func Pin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// ClientConfig accepts a server whose certificate has one of pins or is signed by a
// certificate in its chain that has one.
func ClientConfig(pins ...string) *tls.Config {
	pinned := func(c *x509.Certificate) bool {
		for _, p := range pins {
			if Pin(c) == p {
				return true
			}
		}
		return false
	}
	return &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("no certificate")
			}
			leaf := cs.PeerCertificates[0]
			if pinned(leaf) {
				return nil
			}
			// The CA certificate is public: it vouches for the server only through its signature.
			for _, c := range cs.PeerCertificates[1:] {
				if pinned(c) {
					if err := leaf.CheckSignatureFrom(c); err != nil {
						return fmt.Errorf("pinned certificate did not sign the server certificate: %w", err)
					}
					return nil
				}
			}
			return fmt.Errorf("no pinned key in the server's certificates")
		},
	}
}

// NewListener serves TLS on inner. Accept returns connections whose handshake is done;
// a failed handshake is logged and closed. Accept errors other than a closed listener
// are logged and skipped.
func NewListener(inner net.Listener, cfg *tls.Config) net.Listener {
	l := &listener{Listener: inner, conns: make(chan net.Conn), done: make(chan struct{})}
	go l.run(cfg)
	return l
}

type listener struct {
	net.Listener
	conns     chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

func (l *listener) run(cfg *tls.Config) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			select {
			case <-l.done:
				return
			default:
			}
			glog.Errorln("tls accept:", err)
			time.Sleep(10 * time.Millisecond)
			continue
		}
		go l.handshake(tls.Server(c, cfg))
	}
}

func (l *listener) handshake(c *tls.Conn) {
	c.SetDeadline(time.Now().Add(HandshakeTimeout))
	if err := c.Handshake(); err != nil {
		glog.Infoln("tls handshake", c.RemoteAddr(), err)
		c.Close()
		return
	}
	c.SetDeadline(time.Time{})
	select {
	case l.conns <- c:
	case <-l.done:
		c.Close()
	}
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *listener) Close() error {
	err := net.ErrClosed
	l.closeOnce.Do(func() {
		close(l.done)
		err = l.Listener.Close()
	})
	return err
}
