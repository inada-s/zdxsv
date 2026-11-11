package battle

import (
	"reflect"
	"testing"

	pb "github.com/golang/protobuf/proto"
	"zdxsv/pkg/proto"
)

func TestWireRoundTrip(t *testing.T) {
	msgs := []McsMessage{{UserID: "U1", Seq: 7, Body: []byte{1, 2, 3}}, {UserID: "USER02", Seq: 1 << 31, Body: []byte{}}}
	for _, c := range []struct {
		b    []byte
		want Packet
	}{
		{AppendHello(nil, "12345678"), Packet{Kind: KindHello, SessionID: "12345678"}},
		{AppendHelloAnswer(nil, "12345678", true), Packet{Kind: KindHelloAnswer, SessionID: "12345678", OK: true}},
		{AppendHelloAnswer(nil, "", false), Packet{Kind: KindHelloAnswer}},
		{AppendBattle(nil, 5, 4, nil), Packet{Kind: KindBattle, Seq: 5, Ack: 4}},
		{AppendBattle(nil, 1, 0, msgs), Packet{Kind: KindBattle, Seq: 1, Messages: msgs}},
	} {
		got, err := ParsePacket(c.b)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParsePacket(% x) = %+v, %v; want %+v", c.b, got, err, c.want)
		}
	}
}

func TestWireBad(t *testing.T) {
	full := AppendBattle(nil, 1, 0, []McsMessage{{UserID: "U1", Seq: 1, Body: []byte{9, 9}}})
	for i := 3; i < len(full); i++ {
		if i == 10 {
			continue // no messages is valid
		}
		if _, err := ParsePacket(full[:i]); err == nil {
			t.Errorf("ParsePacket(% x) accepted a cut packet", full[:i])
		}
	}
	for _, b := range [][]byte{nil, {0}, {0, 9}, {0, KindHelloAnswer}} {
		if _, err := ParsePacket(b); err == nil {
			t.Errorf("ParsePacket(% x) = nil error", b)
		}
	}
}

func TestWireNotProtobuf(t *testing.T) {
	ping, err := pb.Marshal(&proto.Packet{Type: proto.MessageType_Ping.Enum(), PingData: &proto.PingMessage{Timestamp: pb.Int64(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if IsWirePacket(ping) {
		t.Errorf("Ping % x taken for a battle packet", ping)
	}
}
