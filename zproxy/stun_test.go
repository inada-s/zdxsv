package main

import (
	"os"
	"strings"
	"testing"
)

// testdata/wire/ping.json and pong.json are shared with the lobby server and emulators.
func TestStunFixtures(t *testing.T) {
	for name, want := range map[string]StunPacket{
		"ping.json": {Type: StunPing, Timestamp: 1234567890123456789, UserID: "ABC123"},
		"pong.json": {Type: StunPong, Timestamp: 1234567890123456789, UserID: "SERVER", PublicAddr: "203.0.113.1:8250"},
	} {
		b, err := os.ReadFile("../testdata/wire/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if got := MarshalStun(want); string(got) != strings.TrimSpace(string(b)) {
			t.Errorf("MarshalStun(%+v) = %s, want %s", want, got, b)
		}
		if got, err := UnmarshalStun(b); err != nil || got != want {
			t.Errorf("UnmarshalStun(%s) = %+v, %v", name, got, err)
		}
	}
}

func TestUnmarshalStun(t *testing.T) {
	got, err := UnmarshalStun([]byte(`{"type":"ping","timestamp":-5,"public_addr":"x","later":[1]}`))
	if want := (StunPacket{Type: StunPing, Timestamp: -5}); err != nil || got != want {
		t.Errorf("Unmarshal = %+v, %v; want %+v", got, err, want)
	}
	for _, s := range []string{
		"",
		`{"timestamp":1}`,
		`{"type":"hello","timestamp":1}`,
		`{"type":"ping",`,
		"\x08\x02\x5a\x02\x08\x01", // a protobuf Ping
		"\x00\x03\x01\x02",         // battle packet
	} {
		if p, err := UnmarshalStun([]byte(s)); err == nil {
			t.Errorf("UnmarshalStun(%q) = %+v, nil error", s, p)
		}
	}
}
