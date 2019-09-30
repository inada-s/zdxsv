package message

import (
	"encoding/hex"
	"testing"

	"flag"
	"github.com/axgle/mahonia"
)

func init() {
	flag.Set("logtostderr", "true")
	flag.Set("v", "10")
	flag.Parse()
}

func TestCP932EncodeDecode(t *testing.T) {
	// sjis string
	data := []byte{0x47, 0x4f, 0x8c, 0xbe, 0x8c, 0xea}
	in := string(data[:])

	// mahonia
	result := mahonia.NewDecoder("Shift_JIS").ConvertString(in)
	t.Log("%v", result)
}

func TestMessage(t *testing.T) {
	data, err := hex.DecodeString("007022b0884d897f98588d83825a916b8244958e1b171a11091389ac9adfa360312b262d2b272a2139232e25233f3239a380b482b9d0b8fdbddeb464bc3c497c53064785cbc759865283424757b55390c1dbd6dddbd7dad1c9d3ded55a5b602065a86454690c6ba8f9e3eee5e3fff2f9e1fb")
	if err != nil {
		t.FailNow()
	}
	msg := &Message{}
	msg.Category = CategoryQuestion
	msg.Direction = ClientToServer
	msg.Seq = 0x0010
	msg.Body = data
	r := msg.Reader()
	str := r.ReadEncryptedString()
	t.Log(str)
	if str == "" {
		t.FailNow()
	}
	t.Log("foo")
}

func TestDecodeMessageLogin(t *testing.T) {
	_, msg := Deserialize([]byte{129, 1, 97, 50, 0, 26, 0, 71, 0, 255, 255, 255, 0, 8, 1, 158, 112, 126, 135, 243, 128, 141, 0, 14, 6, 204, 176, 144, 65, 106, 82, 108, 69, 110, 72, 104, 73, 98})
	r := msg.Reader()
	t.Log(r.ReadEncryptedString())
	println(r.ReadEncryptedString())
	t.Log(r.ReadEncryptedString())
}
