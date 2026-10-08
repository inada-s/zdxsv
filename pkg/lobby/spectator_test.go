package lobby

import (
	"bytes"
	"net"
	"testing"
	"time"

	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
)

type liveSent struct {
	pkt *proto.Packet
	to  *net.UDPAddr
}

func newLiveTest(t *testing.T) (*SpectatorRegistry, *[]liveSent) {
	r := NewSpectatorRegistry()
	var out []liveSent
	r.send = func(data []byte, to *net.UDPAddr) {
		pkt := new(proto.Packet)
		if err := pb.Unmarshal(data, pkt); err != nil {
			t.Fatal(err)
		}
		out = append(out, liveSent{pkt, to})
	}
	return r, &out
}

func livePush(code string, session int32, m *proto.SpectatorInputPush) *proto.Packet {
	m.BattleCode = pb.String(code)
	m.SessionId = pb.Int32(session)
	return &proto.Packet{Type: proto.MessageType_SpectatorInputPushType.Enum(), SpectatorInputPushData: m}
}

func lastAck(t *testing.T, out []liveSent) *proto.SpectatorInputAck {
	if len(out) == 0 || out[len(out)-1].pkt.GetType() != proto.MessageType_SpectatorInputAckType {
		t.Fatalf("no ack: %v", out)
	}
	return out[len(out)-1].pkt.GetSpectatorInputAckData()
}

var (
	liveUp    = &net.UDPAddr{IP: net.IPv4(203, 0, 113, 5), Port: 40001}
	liveOther = &net.UDPAddr{IP: net.IPv4(203, 0, 113, 6), Port: 40002}
	liveSpec  = &net.UDPAddr{IP: net.IPv4(198, 51, 100, 9), Port: 50000}
)

func TestLiveUplinkPush(t *testing.T) {
	r, out := newLiveTest(t)
	now := time.Unix(1700000000, 0)
	// not opened: ignored, no ack
	r.Handle(livePush("B1", 7, &proto.SpectatorInputPush{Header: []byte("h")}), liveUp, now)
	if len(*out) != 0 {
		t.Fatal("push to a battle that was not opened answered")
	}
	r.Open("B1", 7, now)
	r.Handle(livePush("B1", 8, &proto.SpectatorInputPush{Header: []byte("h")}), liveUp, now)
	if len(*out) != 0 {
		t.Fatal("push with a wrong session answered")
	}
	r.Handle(livePush("B1", 7, &proto.SpectatorInputPush{Header: []byte("hdr")}), liveUp, now)
	if a := lastAck(t, *out); !a.GetHeaderAck() || a.GetStateAck() != 0 || a.GetAckFrame() != 0 {
		t.Fatalf("ack %v", a)
	}
	// state: a gap is dropped, an overlap appends its new tail
	st := func(off int, data string) *proto.Packet {
		return livePush("B1", 7, &proto.SpectatorInputPush{State: []byte(data), StateOffset: pb.Int32(int32(off)), StateTotal: pb.Int32(8)})
	}
	r.Handle(st(4, "efgh"), liveUp, now)
	if a := lastAck(t, *out); a.GetStateAck() != 0 {
		t.Fatalf("gap kept: %v", a)
	}
	r.Handle(st(0, "abc"), liveUp, now)
	r.Handle(st(2, "cdefgh"), liveUp, now)
	if a := lastAck(t, *out); a.GetStateAck() != 8 {
		t.Fatalf("state ack %v", a)
	}
	// inputs: 2 bytes a frame
	in := func(f int, data string) *proto.Packet {
		return livePush("B1", 7, &proto.SpectatorInputPush{StartFrame: pb.Int32(int32(f)), FrameBytes: pb.Int32(2), InputData: []byte(data)})
	}
	r.Handle(in(0, "a0a1"), liveUp, now)
	r.Handle(in(3, "a3"), liveUp, now)
	r.Handle(in(1, "a1a2"), liveUp, now)
	if a := lastAck(t, *out); a.GetAckFrame() != 3 {
		t.Fatalf("frame ack %v", a)
	}
	// another address is not the uplink
	n := len(*out)
	r.Handle(in(3, "b3"), liveOther, now)
	if len(*out) != n {
		t.Fatal("second uplink answered")
	}
	// close only once every frame up to it arrived
	cl := func(f int) *proto.Packet {
		return livePush("B1", 7, &proto.SpectatorInputPush{StartFrame: pb.Int32(int32(f)), CloseReason: pb.String("end")})
	}
	r.Handle(cl(4), liveUp, now)
	if a := lastAck(t, *out); a.GetCloseAck() {
		t.Fatal("close with a missing frame")
	}
	r.Handle(cl(3), liveUp, now)
	if a := lastAck(t, *out); !a.GetCloseAck() {
		t.Fatal("close not acked")
	}
	if s := r.sessions["B1"]; string(s.inputs) != "a0a1a2" || string(s.state) != "abcdefgh" || string(s.header) != "hdr" {
		t.Fatalf("session %q %q %q", s.header, s.state, s.inputs)
	}
}

// liveSpectator is a client that keeps contiguous data and acks every push.
type liveSpectator struct {
	code    string
	cookie  []byte
	header  []byte
	state   []byte
	inputs  []byte
	closed  bool
	pushes  int
	dropped int
}

func (c *liveSpectator) subscribe() *proto.Packet {
	cookie := c.cookie
	if cookie == nil {
		cookie = make([]byte, liveCookieBytes)
	}
	return &proto.Packet{Type: proto.MessageType_SpectatorSubscribeType.Enum(), SpectatorSubscribeData: &proto.SpectatorSubscribeRequest{BattleCode: pb.String(c.code), Cookie: cookie}}
}

func (c *liveSpectator) receive(pkt *proto.Packet) *proto.Packet {
	if ch := pkt.GetSpectatorSubscribeChallengeData(); ch != nil {
		c.code, c.cookie = ch.GetBattleCode(), ch.GetCookie()
		return c.subscribe()
	}
	m := pkt.GetSpectatorInputPushData()
	c.pushes++
	if h := m.GetHeader(); h != nil && c.header == nil {
		c.header = h
	}
	if st := m.GetState(); st != nil && int(m.GetStateOffset()) == len(c.state) {
		c.state = append(c.state, st...)
	}
	fb := int(m.GetFrameBytes())
	if in := m.GetInputData(); in != nil && int(m.GetStartFrame())*fb == len(c.inputs) {
		c.inputs = append(c.inputs, in...)
	}
	if m.CloseReason != nil && int(m.GetStartFrame())*4 == len(c.inputs) {
		c.closed = true
	}
	return &proto.Packet{Type: proto.MessageType_SpectatorInputAckType.Enum(), SpectatorInputAckData: &proto.SpectatorInputAck{
		BattleCode: pb.String(c.code), HeaderAck: pb.Bool(c.header != nil), StateAck: pb.Int32(int32(len(c.state))),
		AckFrame: pb.Int32(int32(len(c.inputs) / 4)), CloseAck: pb.Bool(c.closed)}}
}

func TestLiveFanoutLossy(t *testing.T) {
	r, out := newLiveTest(t)
	now := time.Unix(1700000000, 0)
	r.Open("OLD", 1, now.Add(-time.Minute))
	r.Handle(livePush("OLD", 1, &proto.SpectatorInputPush{Header: []byte("old")}), liveOther, now)
	r.Open("B1", 7, now)
	state := make([]byte, 100000)
	for i := range state {
		state[i] = byte(i * 7)
	}
	inputs := make([]byte, 3000*4)
	for i := range inputs {
		inputs[i] = byte(i * 13)
	}
	r.Handle(livePush("B1", 7, &proto.SpectatorInputPush{Header: []byte("header")}), liveUp, now)
	for off := 0; off < len(state); off += liveChunkBytes {
		end := off + liveChunkBytes
		if end > len(state) {
			end = len(state)
		}
		r.Handle(livePush("B1", 7, &proto.SpectatorInputPush{State: state[off:end], StateOffset: pb.Int32(int32(off)), StateTotal: pb.Int32(int32(len(state)))}), liveUp, now)
	}
	// the battle goes on while the spectator joins: frames arrive over time
	pushed := 0
	pushFrames := func(n int) {
		if pushed+n > 3000 {
			n = 3000 - pushed
		}
		r.Handle(livePush("B1", 7, &proto.SpectatorInputPush{StartFrame: pb.Int32(int32(pushed)), FrameBytes: pb.Int32(4), InputData: inputs[pushed*4 : (pushed+n)*4]}), liveUp, now)
		pushed += n
		if pushed == 3000 {
			r.Handle(livePush("B1", 7, &proto.SpectatorInputPush{StartFrame: pb.Int32(3000), CloseReason: pb.String("end")}), liveUp, now)
		}
	}
	pushFrames(600)
	*out = nil

	// empty code: the newest battle with an uplink, then every 5th datagram to it is lost
	c := &liveSpectator{}
	r.Handle(c.subscribe(), liveSpec, now)
	n := 0
	for step := 0; step < 20000 && !c.closed; step++ {
		q := *out
		*out = nil
		for _, s := range q {
			if s.to != liveSpec {
				continue
			}
			if n++; n%5 == 0 {
				c.dropped++
				continue
			}
			if reply := c.receive(s.pkt); reply != nil {
				r.Handle(reply, liveSpec, now)
			}
		}
		now = now.Add(10 * time.Millisecond)
		if step%5 == 0 {
			r.Fanout(now)
			pushFrames(3)
		}
		if step%200 == 0 {
			r.Handle(c.subscribe(), liveSpec, now)
		}
	}
	if c.code != "B1" || !c.closed || string(c.header) != "header" || !bytes.Equal(c.state, state) || !bytes.Equal(c.inputs, inputs) {
		t.Fatalf("code %s closed %v header %q state %d/%d inputs %d/%d", c.code, c.closed, c.header, len(c.state), len(state), len(c.inputs), len(inputs))
	}
	t.Logf("pushes %d, dropped %d, until %v", c.pushes, c.dropped, now.Sub(time.Unix(1700000000, 0)))

	// silent subscribers are dropped, closed battles after the retention
	r.Fanout(now.Add(liveSubscriberTimeout + time.Second))
	if len(r.sessions["B1"].subs) != 0 || r.nsubs != 0 {
		t.Fatal("silent subscriber kept")
	}
	r.Sweep(now.Add(liveRetention + time.Second))
	if _, ok := r.sessions["B1"]; ok {
		t.Fatal("closed battle kept")
	}
}

func TestLiveCookie(t *testing.T) {
	r, out := newLiveTest(t)
	now := time.Unix(1700000010, 0)
	r.Open("B1", 7, now)
	sub := func(cookie []byte, from *net.UDPAddr, at time.Time) {
		r.Handle(&proto.Packet{Type: proto.MessageType_SpectatorSubscribeType.Enum(), SpectatorSubscribeData: &proto.SpectatorSubscribeRequest{BattleCode: pb.String("B1"), Cookie: cookie}}, from, at)
	}
	sub(make([]byte, liveCookieBytes), liveSpec, now)
	ch := (*out)[0].pkt.GetSpectatorSubscribeChallengeData()
	if ch == nil || ch.GetBattleCode() != "B1" || len(ch.GetCookie()) != liveCookieBytes {
		t.Fatalf("challenge %v", *out)
	}
	sub(ch.GetCookie(), liveOther, now)
	if len(r.sessions["B1"].subs) != 0 {
		t.Fatal("cookie accepted from another address")
	}
	sub(ch.GetCookie(), liveSpec, now.Add(2*liveCookieWindow+time.Second))
	if len(r.sessions["B1"].subs) != 0 {
		t.Fatal("expired cookie accepted")
	}
	sub(ch.GetCookie(), liveSpec, now.Add(liveCookieWindow))
	if len(r.sessions["B1"].subs) != 1 {
		t.Fatal("cookie of the previous window refused")
	}
}

func TestLiveUplinkChoice(t *testing.T) {
	infos := map[string]map[string]string{
		"AAAAAA": {"ggpo": "7001", "udp_rtt": "30"},
		"BBBBBB": {"ggpo": "7002", "udp_rtt": "12"},
		"CCCCCC": {"ggpo": "7003"},
		"DDDDDD": {"udp_rtt": "1"},
	}
	info := func(id string) map[string]string { return infos[id] }
	p := &AppPeer{}
	p.UserID = "AAAAAA"
	p.PlatformInfo = infos["AAAAAA"]
	if got := liveUplink(p, []string{"AAAAAA", "BBBBBB", "CCCCCC", "DDDDDD"}, info); got != "BBBBBB" {
		t.Errorf("lowest rtt: %s", got)
	}
	infos["BBBBBB"]["udp_rtt"] = "30"
	if got := liveUplink(p, []string{"CCCCCC", "BBBBBB", "AAAAAA"}, info); got != "BBBBBB" {
		t.Errorf("tie: %s, want the first in battle order", got)
	}
	if got := liveUplink(p, []string{"CCCCCC", "DDDDDD"}, info); got != "CCCCCC" {
		t.Errorf("unknown rtt: %s", got)
	}
	if got := liveUplink(p, []string{"DDDDDD"}, info); got != "" {
		t.Errorf("no ggpo: %s", got)
	}
}
