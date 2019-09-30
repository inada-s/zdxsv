package proto

import (
	"fmt"
	"testing"
)

func assert(f bool) {
	if !f {
		panic("assertion failed.")
	}
}

func testEq(a, b []byte) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMessageSeq(t *testing.T) {
	f := NewMessageFilter()
	msg := f.GenerateMessage("hoge", []byte("hoge"))
	assert(msg.GetSeq() == 1)
	msg = f.GenerateMessage("hoge", []byte("hoge"))
	assert(msg.GetSeq() == 2)
	msg = f.GenerateMessage("hoge", []byte("hoge"))
	assert(msg.GetSeq() == 3)
}

func TestProtoRUDP(t *testing.T) {
	t.Log("hoge")

	a := NewBuffer("a")
	af := NewMessageFilter()

	b := NewBuffer("b")
	bf := NewMessageFilter()

	/*
		var recvdata []byte
		var err error
	*/
	assert(a.begin == 1)
	assert(a.end == 1)
	assert(af.seq == 1)

	a.PushBattleMessage(af.GenerateMessage(a.GetId(), []byte("abc")))

	assert(a.end == 2)
	senddata, seq := a.GetSendData()
	ack := a.GetAck()
	t.Log(senddata, seq, ack)

	assert(b.ack == 0)
	b.ApplySeqAck(seq, ack)
	assert(bf.Filter(senddata[0]) == true)
	recvdata := senddata[0]
	t.Log("recv data", senddata[0])
	assert(string(recvdata.GetBody()) == "abc")
	assert(recvdata.GetUserId() == "a")
	assert(recvdata.GetSeq() == 1)
	assert(b.GetAck() == 1)
	t.Log("A send data before", b.begin, b.end)

	assert(a.end == 2)
	a.PushBattleMessage(af.GenerateMessage(a.GetId(), []byte("def")))
	assert(a.end == 3)
	a.PushBattleMessage(af.GenerateMessage(a.GetId(), []byte("ghi")))
	assert(a.end == 4)
	senddata, seq = a.GetSendData()
	t.Log("B send data before", b.begin, b.end)

	assert(b.ack == 1)
	buf := make([]byte, 0)
	for _, msg := range senddata {
		if bf.Filter(msg) {
			buf = append(buf, msg.GetBody()...)
		}
	}
	assert(testEq(buf, []byte("defghi")))
	buf = buf[:0]
	b.ApplySeqAck(seq, ack)
	assert(b.ack == 3)
	t.Log("C send data before", b.begin, b.end)

	assert(a.ack == 0)
	senddata, seq = a.GetSendData()
	for _, msg := range senddata {
		if bf.Filter(msg) {
			buf = append(buf, msg.GetBody()...)
		}
	}
	assert(testEq(buf, []byte("")))
	assert(a.ack == 0)

	t.Log("D send data before", b.begin, b.end)

	t.Log("add send data before", b.begin, b.end)
	data := []*proto.BattleMessage{}

	b.PushBattleMessage(bf.GenerateMessage(b.GetId(), []byte("hoge")))
	senddata, seq = b.GetSendData()
	data = append(data, senddata...)
	senddata, seq = b.GetSendData()
	data = append(data, senddata...)
	senddata, seq = b.GetSendData()
	data = append(data, senddata...)

	b.PushBattleMessage(bf.GenerateMessage(b.GetId(), []byte("piyo")))
	senddata, seq = b.GetSendData()
	data = append(data, senddata...)
	senddata, seq = b.GetSendData()
	data = append(data, senddata...)
	senddata, seq = b.GetSendData()
	data = append(data, senddata...)
	t.Log("add send data after", b.begin, b.end)

	a.ApplySeqAck(seq, b.GetAck())
	buf = buf[:0]
	for _, msg := range data {
		if af.Filter(msg) {
			buf = append(buf, msg.GetBody()...)
		}
	}
	t.Log(buf)
	assert(testEq(buf, []byte("hogepiyo")))
	assert(a.ack == 2)

	fmt.Println("ok")
}
