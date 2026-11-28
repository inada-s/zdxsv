package main

import (
	"fmt"
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

func brief(seqs []uint32) any {
	if len(seqs) > 8 {
		return fmt.Sprintf("%d messages", len(seqs))
	}
	return seqs
}

func TestBattleBufferAckWindow(t *testing.T) {
	pending := func(b *BattleBuffer) []uint32 {
		t.Helper()
		pkt, err := ParseBattlePacket(b.AppendPacket(nil))
		if err != nil {
			t.Fatal(err)
		}
		var seqs []uint32
		for _, m := range pkt.Messages {
			seqs = append(seqs, m.Seq)
		}
		return seqs
	}
	push3 := func(b *BattleBuffer, first uint32) {
		for i := uint32(0); i < 3; i++ {
			b.Push(BattleMessage{UserID: "U1", Seq: first + i})
		}
	}

	b := NewBattleBuffer("U1")
	push3(b, 1)
	for _, c := range []struct {
		ack  uint32
		want []uint32
	}{
		{1000, []uint32{1, 2, 3}},
		{0xfffffff0, []uint32{1, 2, 3}},
		{0, []uint32{1, 2, 3}},
		{2, []uint32{3}},
		{1, []uint32{3}},
		{4, []uint32{3}},
		{3, nil},
	} {
		b.ApplySeqAck(0, c.ack)
		if got := pending(b); !reflect.DeepEqual(got, c.want) {
			t.Errorf("after ack %d: pending %v, want %v", c.ack, brief(got), c.want)
		}
	}

	w := NewBattleBuffer("U1")
	w.begin, w.end = 0xfffffffe, 0xfffffffe
	push3(w, 0xfffffffe)
	w.ApplySeqAck(0, 1000)
	if got := pending(w); len(got) != 3 {
		t.Errorf("wrapping window: %d pending after a later ack, want 3", len(got))
	}
	w.ApplySeqAck(0, 0xfffffffe)
	if got := pending(w); !reflect.DeepEqual(got, []uint32{0xffffffff, 0}) {
		t.Errorf("wrapping window: pending %v after acking the first", brief(got))
	}
	w.ApplySeqAck(0, 0)
	if got := pending(w); got != nil {
		t.Errorf("wrapping window: pending %v after acking all", brief(got))
	}
}

func TestWireNotProtobuf(t *testing.T) {
	ping := MarshalStun(StunPacket{Type: StunPing, Timestamp: 1})
	if IsBattlePacket(ping) {
		t.Errorf("Ping % x taken for a battle packet", ping)
	}
}
