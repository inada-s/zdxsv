package lobby

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"zdxsv/pkg/db"
	"zdxsv/pkg/proto"

	pb "github.com/golang/protobuf/proto"
)

func livePush(code string, session int32, m *proto.SpectatorInputPush) *proto.SpectatorInputPush {
	m.BattleCode = pb.String(code)
	m.SessionId = pb.Int32(session)
	return m
}

func liveInputs(code string, session int32, f int, data string) *proto.SpectatorInputPush {
	return livePush(code, session, &proto.SpectatorInputPush{StartFrame: pb.Int32(int32(f)), FrameBytes: pb.Int32(2), InputData: []byte(data)})
}

func liveClose(code string, session int32, f int) *proto.SpectatorInputPush {
	return livePush(code, session, &proto.SpectatorInputPush{StartFrame: pb.Int32(int32(f)), CloseReason: pb.String("end")})
}

func TestLiveUplinkPush(t *testing.T) {
	r := NewSpectatorRegistry()
	now := time.Unix(1700000000, 0)
	if r.Push(livePush("B1", 7, &proto.SpectatorInputPush{Header: []byte("h")}), now) != nil {
		t.Fatal("push to a battle that was not opened answered")
	}
	r.Open("B1", 7, nil, now)
	if r.Push(livePush("B1", 8, &proto.SpectatorInputPush{Header: []byte("h")}), now) != nil {
		t.Fatal("push with a wrong session answered")
	}
	if a := r.Push(livePush("B1", 7, &proto.SpectatorInputPush{Header: []byte("hdr")}), now); !a.GetHeaderAck() || a.GetAckFrame() != 0 {
		t.Fatalf("ack %v", a)
	}
	if a := r.Push(livePush("B1", 7, &proto.SpectatorInputPush{State: []byte("abcd"), StateOffset: pb.Int32(0), StateTotal: pb.Int32(8)}), now); a.StateAck != nil {
		t.Fatalf("state acked: %v", a)
	}
	r.Push(liveInputs("B1", 7, 0, "a0a1"), now)
	r.Push(liveInputs("B1", 7, 3, "a3"), now)
	if a := r.Push(liveInputs("B1", 7, 1, "a1a2"), now); a.GetAckFrame() != 3 {
		t.Fatalf("frame ack %v", a)
	}
	if a := r.Push(liveClose("B1", 7, 4), now); a.GetCloseAck() {
		t.Fatal("close with a missing frame")
	}
	if a := r.Push(liveClose("B1", 7, 3), now); !a.GetCloseAck() {
		t.Fatal("close not acked")
	}
	if a := r.Push(liveInputs("B1", 7, 3, "a3"), now); a.GetAckFrame() != 3 {
		t.Fatalf("input after close kept: %v", a)
	}
	if s := r.sessions["B1"]; string(s.inputs) != "a0a1a2" || string(s.header) != "hdr" {
		t.Fatalf("session %q %q", s.header, s.inputs)
	}
}

type liveHTTP struct {
	t  *testing.T
	sv *httptest.Server
}

func newLiveHTTP(t *testing.T, r *SpectatorRegistry) *liveHTTP {
	sv := httptest.NewServer(&APIHandler{Live: r})
	t.Cleanup(sv.Close)
	return &liveHTTP{t, sv}
}

func (c *liveHTTP) push(m *proto.SpectatorInputPush) (*proto.SpectatorInputAck, int) {
	body, _ := pb.Marshal(m)
	res, err := http.Post(c.sv.URL+"/lbs/live_push", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(res.Body)
	a := new(proto.SpectatorInputAck)
	if res.StatusCode == 200 {
		if err := pb.Unmarshal(buf.Bytes(), a); err != nil {
			c.t.Fatal(err)
		}
	}
	return a, res.StatusCode
}

func (c *liveHTTP) pull(q url.Values) (*proto.SpectatorInputPush, int, time.Duration) {
	start := time.Now()
	res, err := http.Get(c.sv.URL + "/lbs/live_pull?" + q.Encode())
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(res.Body)
	m := new(proto.SpectatorInputPush)
	if res.StatusCode == 200 {
		if err := pb.Unmarshal(buf.Bytes(), m); err != nil {
			c.t.Fatal(err)
		}
	}
	return m, res.StatusCode, time.Since(start)
}

// A spectator that pulls with what it holds gets the header, rounds, every input and the close.
func TestLiveHTTP(t *testing.T) {
	r := NewSpectatorRegistry()
	c := newLiveHTTP(t, r)
	if _, code := c.push(livePush("B1", 7, &proto.SpectatorInputPush{Header: []byte("h")})); code != 404 {
		t.Fatalf("push to an unknown battle: %d", code)
	}
	if _, code, _ := c.pull(url.Values{"battle_code": {"B1"}}); code != 404 {
		t.Fatalf("pull of an unknown battle: %d", code)
	}
	r.Open("B1", 7, nil, time.Now())
	if a, code := c.push(livePush("B1", 7, &proto.SpectatorInputPush{Header: []byte("hdr"), RoundData: []byte("r1")})); code != 200 || !a.GetHeaderAck() || a.GetRoundAck() != 2 {
		t.Fatalf("push %d %v", code, a)
	}
	var in []byte
	for f := 0; f < liveMaxPullFrames+5; f++ {
		in = append(in, byte(f), byte(f>>8))
	}
	c.push(liveInputs("B1", 7, 0, string(in)))
	c.push(liveClose("B1", 7, liveMaxPullFrames+5))

	q := url.Values{"battle_code": {"B1"}, "id": {"s1"}}
	m, code, _ := c.pull(q)
	if code != 200 || string(m.GetHeader()) != "hdr" || m.InputData != nil || m.GetBattleCode() != "B1" {
		t.Fatalf("first pull %d %v", code, m)
	}
	q.Set("header", "1")
	var got []byte
	closed := false
	for i := 0; i < 5 && !closed; i++ {
		q.Set("frame", fmt.Sprint(len(got)/2))
		m, _, _ = c.pull(q)
		if rd := m.GetRoundData(); rd != nil {
			if string(rd) != "r1" {
				t.Fatalf("rounds %q", rd)
			}
			q.Set("round", fmt.Sprint(len(rd)))
		}
		if m.InputData != nil && int(m.GetStartFrame()) == len(got)/2 {
			got = append(got, m.GetInputData()...)
		}
		closed = m.CloseReason != nil && int(m.GetStartFrame()) == len(got)/2
	}
	if !closed || !bytes.Equal(got, in) || q.Get("round") != "2" {
		t.Fatalf("closed %v, %d of %d input bytes, round %s", closed, len(got), len(in), q.Get("round"))
	}
	if l := r.List(); len(l) != 1 || l[0].Spectators != 1 {
		t.Fatalf("list %v", l)
	}
}

// A pull without news waits for the next push, at most liveWait.
func TestLiveHTTPLongPoll(t *testing.T) {
	r := NewSpectatorRegistry()
	c := newLiveHTTP(t, r)
	r.Open("B1", 7, nil, time.Now())
	c.push(livePush("B1", 7, &proto.SpectatorInputPush{Header: []byte("hdr")}))
	q := url.Values{"battle_code": {"B1"}, "header": {"1"}, "frame": {"0"}}
	m, code, took := c.pull(q)
	if code != 200 || m.InputData != nil || took < liveWait-50*time.Millisecond || took > liveWait+500*time.Millisecond {
		t.Fatalf("idle pull %d %v after %v", code, m, took)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		r.Push(liveInputs("B1", 7, 0, "a0"), time.Now())
	}()
	m, _, took = c.pull(q)
	if string(m.GetInputData()) != "a0" || took > liveWait/2 {
		t.Fatalf("woken pull %v after %v", m, took)
	}
}

func TestLiveEndGrace(t *testing.T) {
	r := NewSpectatorRegistry()
	now := time.Unix(1700000000, 0)
	r.Open("B1", 7, []*db.ReplayUser{{UserID: "u1"}}, now)
	r.Push(liveInputs("B1", 7, 0, "a0a1"), now)
	r.End("B1", "nobody", now)
	r.End("B1", "u1", now)
	r.Tick(now.Add(liveEndGrace - time.Second))
	if !r.sessions["B1"].closed.IsZero() {
		t.Fatal("closed before the grace")
	}
	r.Tick(now.Add(liveEndGrace))
	m := r.Pull(&LivePull{Code: "B1", Header: true, Frame: 2}, 0, nil)
	if m.GetCloseReason() != "battle end" || m.GetStartFrame() != 2 {
		t.Fatalf("close %v", m)
	}
	r.Tick(now.Add(liveEndGrace + liveRetention + time.Second))
	if r.Pull(&LivePull{Code: "B1"}, 0, nil) != nil || r.nsubs != 0 {
		t.Fatalf("not dropped, %d subscribers", r.nsubs)
	}
}

func TestLiveSubscriberLimit(t *testing.T) {
	r := NewSpectatorRegistry()
	now := time.Now()
	r.Open("B1", 7, nil, now)
	for i := 0; i < liveMaxBattleSubscriber; i++ {
		if r.Pull(&LivePull{Code: "B1", ID: fmt.Sprint(i)}, 0, nil) == nil {
			t.Fatalf("subscriber %d refused", i)
		}
	}
	if r.Pull(&LivePull{Code: "B1", ID: "x"}, 0, nil) != nil || r.Pull(&LivePull{Code: "B1", ID: "0"}, 0, nil) == nil {
		t.Fatal("limit")
	}
	r.Tick(now.Add(liveSubscriberTimeout + time.Second))
	if r.nsubs != 0 || r.Pull(&LivePull{Code: "B1", ID: "x"}, 0, nil) == nil {
		t.Fatalf("timed out subscribers kept: %d", r.nsubs)
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

func TestLiveAutoNextPick(t *testing.T) {
	r := NewSpectatorRegistry()
	now := time.Unix(1700000020, 0)
	for i, code := range []string{"A", "B", "C"} {
		r.Open(code, uint32(i+1), nil, now.Add(time.Duration(i-3)*time.Minute))
		r.Push(livePush(code, int32(i+1), &proto.SpectatorInputPush{Header: []byte("h")}), now)
	}
	r.Push(liveInputs("C", 3, 0, "a0a1"), now)
	r.Push(liveClose("C", 3, 2), now)
	r.Open("D", 4, nil, now) // no uplink yet
	pick := func(skip ...string) string {
		m := r.Pull(&LivePull{Skip: skip, ID: "s"}, 0, nil)
		if m == nil {
			return ""
		}
		return m.GetBattleCode()
	}
	// running before closed, latest opened first, never one without an uplink
	for _, c := range []struct {
		skip []string
		want string
	}{{nil, "B"}, {[]string{"B"}, "A"}, {[]string{"X"}, "B"}, {[]string{"A", "B"}, ""}} {
		if got := pick(c.skip...); got != c.want {
			t.Errorf("skip %v: %q, want %q", c.skip, got, c.want)
		}
	}
}

func TestLiveList(t *testing.T) {
	r := NewSpectatorRegistry()
	now := time.Unix(1700000030, 0)
	users := []*db.ReplayUser{{UserID: "u1", UserName: "n1", Team: 1, Pos: 1}}
	for i, code := range []string{"A", "B", "C"} {
		r.Open(code, uint32(i+1), users, now.Add(time.Duration(i-3)*time.Minute))
		r.Push(livePush(code, int32(i+1), &proto.SpectatorInputPush{Header: []byte("h")}), now)
	}
	r.Push(liveInputs("A", 1, 0, "a0a1"), now)
	r.Push(liveInputs("C", 3, 0, "c0"), now)
	r.Push(liveClose("C", 3, 1), now)
	r.Open("D", 4, users, now) // no uplink yet
	var got []string
	for _, b := range r.List() {
		got = append(got, fmt.Sprintf("%s:%d:%d:%v:%s", b.BattleCode, b.StartUnix-now.Unix(), b.Frames, b.Closed, b.Users[0].UserName))
	}
	want := []string{"B:-120:0:false:n1", "A:-180:2:false:n1", "C:-60:1:true:n1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("list %v, want %v", got, want)
	}
}
