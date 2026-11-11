// Package stun encodes the Ping/Pong datagrams zproxy exchanges with the lobby's STUN
// server and with other zproxies. On the wire they are the protobuf (proto2) message
// Packet{type = 1, ping_data = 11 | pong_data = 12}, Ping/PongMessage{timestamp = 1,
// user_id = 2, public_addr = 3 (Pong only)}.
package stun

import (
	"encoding/binary"
	"errors"
	"fmt"
)

type Type int32

const (
	Ping Type = 2
	Pong Type = 3
)

const (
	fieldType = 1
	fieldPing = 11
	fieldPong = 12

	fieldTimestamp  = 1
	fieldUserID     = 2
	fieldPublicAddr = 3

	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

type Packet struct {
	Type       Type
	Timestamp  int64
	UserID     string
	PublicAddr string // Pong only
}

func appendTag(b []byte, field, wire int) []byte {
	return binary.AppendUvarint(b, uint64(field<<3|wire))
}

func appendString(b []byte, field int, s string) []byte {
	b = appendTag(b, field, wireBytes)
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

// Marshal encodes p. Empty strings are left out.
func Marshal(p Packet) []byte {
	var body []byte
	body = appendTag(body, fieldTimestamp, wireVarint)
	body = binary.AppendUvarint(body, uint64(p.Timestamp))
	if p.UserID != "" {
		body = appendString(body, fieldUserID, p.UserID)
	}
	if p.Type == Pong && p.PublicAddr != "" {
		body = appendString(body, fieldPublicAddr, p.PublicAddr)
	}

	b := appendTag(nil, fieldType, wireVarint)
	b = binary.AppendUvarint(b, uint64(p.Type))
	field := fieldPing
	if p.Type == Pong {
		field = fieldPong
	}
	return appendString(b, field, string(body))
}

var errShort = errors.New("stun: truncated")

// fields calls fn for each varint and length-delimited field of the protobuf message b.
func fields(b []byte, fn func(field, wire int, v uint64, data []byte)) error {
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return errShort
		}
		b = b[n:]
		field, wire := int(tag>>3), int(tag&7)
		if field == 0 {
			return errors.New("stun: field 0")
		}
		switch wire {
		case wireVarint:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return errShort
			}
			b = b[n:]
			fn(field, wire, v, nil)
		case wireBytes:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return errShort
			}
			data := b[n : n+int(l)]
			b = b[n+int(l):]
			fn(field, wire, 0, data)
		case wireFixed64, wireFixed32:
			size := 8
			if wire == wireFixed32 {
				size = 4
			}
			if len(b) < size {
				return errShort
			}
			b = b[size:]
		default:
			return fmt.Errorf("stun: wire type %d", wire)
		}
	}
	return nil
}

// Unmarshal decodes a Ping or a Pong; unknown fields are skipped.
func Unmarshal(b []byte) (Packet, error) {
	var p Packet
	var hasType bool
	var body []byte
	err := fields(b, func(field, wire int, v uint64, data []byte) {
		switch {
		case field == fieldType && wire == wireVarint:
			p.Type, hasType = Type(v), true
		case (field == fieldPing || field == fieldPong) && wire == wireBytes:
			body = data
		}
	})
	if err != nil {
		return Packet{}, err
	}
	if !hasType {
		return Packet{}, errors.New("stun: no type")
	}
	if p.Type != Ping && p.Type != Pong {
		return Packet{}, fmt.Errorf("stun: type %d", p.Type)
	}
	err = fields(body, func(field, wire int, v uint64, data []byte) {
		switch {
		case field == fieldTimestamp && wire == wireVarint:
			p.Timestamp = int64(v)
		case field == fieldUserID && wire == wireBytes:
			p.UserID = string(data)
		case field == fieldPublicAddr && wire == wireBytes && p.Type == Pong:
			p.PublicAddr = string(data)
		}
	})
	if err != nil {
		return Packet{}, err
	}
	return p, nil
}
