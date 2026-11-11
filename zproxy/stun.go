package main

import (
	"encoding/json"
	"fmt"
)

// StunPacket is a Ping or a Pong zproxy exchanges with the lobby's STUN server and with
// other zproxies, as JSON.
type StunPacket struct {
	Type       string `json:"type"`
	Timestamp  int64  `json:"timestamp"`
	UserID     string `json:"user_id,omitempty"`
	PublicAddr string `json:"public_addr,omitempty"` // Pong only
}

const (
	StunPing = "ping"
	StunPong = "pong"
)

func MarshalStun(p StunPacket) []byte {
	if p.Type != StunPong {
		p.PublicAddr = ""
	}
	b, _ := json.Marshal(p)
	return b
}

// UnmarshalStun decodes a Ping or a Pong; unknown fields are skipped.
func UnmarshalStun(b []byte) (StunPacket, error) {
	var p StunPacket
	if err := json.Unmarshal(b, &p); err != nil {
		return StunPacket{}, fmt.Errorf("stun: %w", err)
	}
	if p.Type != StunPing && p.Type != StunPong {
		return StunPacket{}, fmt.Errorf("stun: type %q", p.Type)
	}
	if p.Type == StunPing {
		p.PublicAddr = ""
	}
	return p, nil
}
