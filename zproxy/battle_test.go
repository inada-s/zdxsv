package main

import (
	"reflect"
	"testing"
)

func TestWireRoundTrip(t *testing.T) {
	msgs := []BattleMessage{{UserID: "U1", Seq: 7, Body: []byte{1, 2, 3}}, {UserID: "USER02", Seq: 1 << 31, Body: []byte{}}}
	for _, c := range []struct {
		b    []byte
		want BattlePacket
	}{
		{AppendHello(nil, "12345678"), BattlePacket{Kind: KindHello, SessionID: "12345678"}},
		{AppendHelloAnswer(nil, "12345678", true), BattlePacket{Kind: KindHelloAnswer, SessionID: "12345678", OK: true}},
		{AppendHelloAnswer(nil, "", false), BattlePacket{Kind: KindHelloAnswer}},
		{AppendBattle(nil, 5, 4, nil), BattlePacket{Kind: KindBattle, Seq: 5, Ack: 4}},
		{AppendBattle(nil, 1, 0, msgs), BattlePacket{Kind: KindBattle, Seq: 1, Messages: msgs}},
	} {
		got, err := ParseBattlePacket(c.b)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseBattlePacket(% x) = %+v, %v; want %+v", c.b, got, err, c.want)
		}
	}
}

func TestWireBad(t *testing.T) {
	full := AppendBattle(nil, 1, 0, []BattleMessage{{UserID: "U1", Seq: 1, Body: []byte{9, 9}}})
	for i := 3; i < len(full); i++ {
		if i == 10 {
			continue // no messages is valid
		}
		if _, err := ParseBattlePacket(full[:i]); err == nil {
			t.Errorf("ParseBattlePacket(% x) accepted a cut packet", full[:i])
		}
	}
	for _, b := range [][]byte{nil, {0}, {0, 9}, {0, KindHelloAnswer}} {
		if _, err := ParseBattlePacket(b); err == nil {
			t.Errorf("ParseBattlePacket(% x) = nil error", b)
		}
	}
}

func TestWireNotProtobuf(t *testing.T) {
	ping := MarshalStun(StunPacket{Type: StunPing, Timestamp: 1})
	if IsBattlePacket(ping) {
		t.Errorf("Ping % x taken for a battle packet", ping)
	}
}
