package rudp

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

func test16(v uint16) {
	b := make([]byte, 2)
	uint16ToBin(b, v)
	assert(v == binToUint16(b))
}

func test32(v uint32) {
	b := make([]byte, 4)
	uint32ToBin(b, v)
	assert(v == binToUint32(b))
}

func TestBitconv(_ *testing.T) {
	test16(0x0000)
	test16(0x00FF)
	test16(0x0100)
	test16(0xFFFF)
	test32(0x00000000)
	test32(0x0000FF00)
	test32(0x00010000)
	test32(0xFFFFFFFF)
}

func TestRUDP(t *testing.T) {
	t.Log("hoge")
	a := NewRUDP()
	b := NewRUDP()

	var recvdata []byte
	var err error

	assert(a.end == 1)
	a.AddSendData([]byte("abc"))
	assert(a.end == 2)
	send_data := a.GetSendData()
	t.Log(send_data)

	assert(b.ack == 0)
	recvdata, err = b.ReceiveFilter(send_data)
	t.Log(recvdata)
	assert(err == nil)
	assert(testEq(recvdata, []byte("abc")))
	assert(b.ack == 1)
	t.Log("A send data before", b.begin, b.end)

	assert(a.end == 2)
	a.AddSendData([]byte("def"))
	assert(a.end == 3)
	a.AddSendData([]byte("ghi"))
	assert(a.end == 4)
	send_data = a.GetSendData()
	t.Log("B send data before", b.begin, b.end)

	assert(b.ack == 1)
	recvdata, err = b.ReceiveFilter(send_data)
	assert(err == nil)
	assert(testEq(recvdata, []byte("defghi")))
	assert(b.ack == 3)
	t.Log("C send data before", b.begin, b.end)

	assert(a.ack == 0)
	send_data = b.GetSendData()
	recvdata, err = a.ReceiveFilter(send_data)
	assert(err == nil)
	assert(testEq(recvdata, []byte("")))
	assert(a.ack == 0)
	t.Log("D send data before", b.begin, b.end)

	t.Log("add send data before", b.begin, b.end)
	b.AddSendData([]byte("hoge"))
	send_data = b.GetSendData()
	send_data = append(send_data, b.GetSendData()...)
	send_data = append(send_data, b.GetSendData()...)
	send_data = append(send_data, b.GetSendData()...)
	b.AddSendData([]byte("piyo"))
	send_data = append(send_data, b.GetSendData()...)
	send_data = append(send_data, b.GetSendData()...)
	send_data = append(send_data, b.GetSendData()...)
	t.Log("add send data after", b.begin, b.end)
	recvdata, err = a.ReceiveFilter(send_data)
	assert(err == nil)
	t.Log(send_data)
	t.Log(recvdata)
	assert(testEq(recvdata, []byte("hogepiyo")))
	assert(a.ack == 2)

	fmt.Println("ok")
}
