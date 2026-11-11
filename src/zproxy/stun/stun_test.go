package stun

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Bytes the protobuf library marshals for these packets.
var golden = []struct {
	pkt Packet
	hex string
}{
	{Packet{Type: Ping, Timestamp: 1}, "08025a020801"},
	{Packet{Type: Ping, Timestamp: 1234567890123456789, UserID: "ABC123"}, "08025a12089582a6efc79e8491111206414243313233"},
	{Packet{Type: Pong, Timestamp: -5, UserID: "SERVER", PublicAddr: "203.0.113.1:8250"},
		"0803622508fbffffffffffffffff0112065345525645521a103230332e302e3131332e313a38323530"},
}

func TestGolden(t *testing.T) {
	for _, g := range golden {
		want, _ := hex.DecodeString(g.hex)
		if got := Marshal(g.pkt); !bytes.Equal(got, want) {
			t.Errorf("Marshal(%+v) = %x, want %s", g.pkt, got, g.hex)
		}
		got, err := Unmarshal(want)
		if err != nil || got != g.pkt {
			t.Errorf("Unmarshal(%s) = %+v, %v", g.hex, got, err)
		}
	}
}

func TestUnmarshalSkipsUnknownFields(t *testing.T) {
	// Pong golden + fixed32 field 4 and fixed64 field 13 on the packet, varint field 9 in the body
	b, _ := hex.DecodeString("0803622708fbffffffffffffffff0112065345525645521a103230332e302e3131332e313a383235304807" + "2501020304" + "690102030405060708")
	got, err := Unmarshal(b)
	want := golden[2].pkt
	if err != nil || got != want {
		t.Errorf("Unmarshal = %+v, %v; want %+v", got, err, want)
	}
}

func TestUnmarshalBad(t *testing.T) {
	for _, h := range []string{
		"",                 // no type
		"0801",             // type 1
		"08",               // truncated varint
		"08025a05",         // body longer than the packet
		"08020f",           // wire type 7
		"0003",             // field 0
		"00030102",         // battle packet
		"08025a0408011201", // body: user id longer than the body
	} {
		b, _ := hex.DecodeString(h)
		if p, err := Unmarshal(b); err == nil {
			t.Errorf("Unmarshal(%s) = %+v, nil error", h, p)
		}
	}
}
