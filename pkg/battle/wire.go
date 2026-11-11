package battle

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Battle UDP packets between zproxy and the battle server, and between zproxies.
// They share zproxy's socket with protobuf Ping/Pong; byte 0 is 0, which no
// protobuf message starts with.
const (
	KindHello       byte = 1
	KindHelloAnswer byte = 2
	KindBattle      byte = 3
)

type Message struct {
	UserID string
	Seq    uint32
	Body   []byte
}

type Packet struct {
	Kind      byte
	SessionID string
	OK        bool
	Seq       uint32
	Ack       uint32
	Messages  []Message
}

func IsWirePacket(b []byte) bool {
	return len(b) >= 2 && b[0] == 0
}

func AppendHello(b []byte, sessionID string) []byte {
	b = append(b, 0, KindHello)
	return append(b, sessionID...)
}

func AppendHelloAnswer(b []byte, sessionID string, ok bool) []byte {
	b = append(b, 0, KindHelloAnswer, 0)
	if ok {
		b[len(b)-1] = 1
	}
	return append(b, sessionID...)
}

func AppendBattle(b []byte, seq, ack uint32, msgs []Message) []byte {
	b = append(b, 0, KindBattle)
	b = binary.BigEndian.AppendUint32(b, seq)
	b = binary.BigEndian.AppendUint32(b, ack)
	for _, m := range msgs {
		b = append(b, byte(len(m.UserID)))
		b = append(b, m.UserID...)
		b = binary.BigEndian.AppendUint32(b, m.Seq)
		b = binary.BigEndian.AppendUint16(b, uint16(len(m.Body)))
		b = append(b, m.Body...)
	}
	return b
}

var errShort = errors.New("battle packet: short")

// ParsePacket decodes b. Message bodies are copies.
func ParsePacket(b []byte) (Packet, error) {
	var p Packet
	if !IsWirePacket(b) {
		return p, fmt.Errorf("battle packet: not a battle packet")
	}
	p.Kind = b[1]
	b = b[2:]
	switch p.Kind {
	case KindHello:
		p.SessionID = string(b)
	case KindHelloAnswer:
		if len(b) < 1 {
			return p, errShort
		}
		p.OK = b[0] == 1
		p.SessionID = string(b[1:])
	case KindBattle:
		if len(b) < 8 {
			return p, errShort
		}
		p.Seq = binary.BigEndian.Uint32(b)
		p.Ack = binary.BigEndian.Uint32(b[4:])
		b = b[8:]
		for len(b) > 0 {
			n := int(b[0])
			if len(b) < 1+n+6 {
				return p, errShort
			}
			m := Message{UserID: string(b[1 : 1+n])}
			b = b[1+n:]
			m.Seq = binary.BigEndian.Uint32(b)
			size := int(binary.BigEndian.Uint16(b[4:]))
			b = b[6:]
			if len(b) < size {
				return p, errShort
			}
			m.Body = append([]byte{}, b[:size]...)
			b = b[size:]
			p.Messages = append(p.Messages, m)
		}
	default:
		return p, fmt.Errorf("battle packet: kind %d", p.Kind)
	}
	return p, nil
}
