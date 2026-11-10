package zlog

import (
	"net"
	"regexp"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

var (
	ipv4Re = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)
	ipv6Re = regexp.MustCompile(`[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}`)
	bufs   = buffer.NewPool()
)

// maskEncoder masks every IP address in the encoded line, so addresses inside
// messages, errors and fields are covered alike.
type maskEncoder struct {
	zapcore.Encoder
}

func (e maskEncoder) Clone() zapcore.Encoder {
	return maskEncoder{e.Encoder.Clone()}
}

func (e maskEncoder) EncodeEntry(ent zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	b, err := e.Encoder.EncodeEntry(ent, fields)
	if err != nil {
		return b, err
	}
	masked := MaskIPs(b.Bytes())
	out := bufs.Get()
	_, _ = out.Write(masked)
	b.Free()
	return out, nil
}

// MaskIPs keeps the first octet of an IPv4 address and the first three groups
// of an IPv6 address, and replaces the remaining digits with x.
// Loopback and unspecified addresses stay as they are.
func MaskIPs(s []byte) []byte {
	s = ipv4Re.ReplaceAllFunc(s, func(m []byte) []byte {
		ip := net.ParseIP(string(m))
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return m
		}
		out := append([]byte(nil), m...)
		for i := indexByte(out, '.'); i < len(out); i++ {
			if '0' <= out[i] && out[i] <= '9' {
				out[i] = 'x'
			}
		}
		return out
	})
	return ipv6Re.ReplaceAllFunc(s, func(m []byte) []byte {
		ip := net.ParseIP(string(m))
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return m
		}
		out := append([]byte(nil), m...)
		colons := 0
		for i := range out {
			if out[i] == ':' {
				colons++
				continue
			}
			if 3 <= colons && isHex(out[i]) {
				out[i] = 'x'
			}
		}
		return out
	})
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return len(b)
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
