package message

import (
	"bytes"
	"strings"
	"testing"
)

func TestSerializeHeaderLayout(t *testing.T) {
	m := &Message{
		Direction: ServerToClient,
		Category:  CategoryAnswer,
		Command:   0x6101,
		Seq:       0x0203,
		Status:    StatusSuccess,
		Body:      []byte{0xAA, 0xBB},
	}
	want := []byte{0x18, 0x02, 0x61, 0x01, 0x00, 0x02, 0x02, 0x03, 0x00, 0xFF, 0xFF, 0xFF, 0xAA, 0xBB}
	if got := m.Serialize(); !bytes.Equal(got, want) {
		t.Fatalf("Serialize() = % x, want % x", got, want)
	}
	if m.BodySize != 2 {
		t.Errorf("BodySize = %d, want 2", m.BodySize)
	}
}

func TestDeserializeRoundTrip(t *testing.T) {
	m := &Message{
		Direction: ClientToServer,
		Category:  CategoryNotice,
		Command:   0x1234,
		Seq:       77,
		Status:    StatusError,
		Body:      []byte("hello"),
	}
	data := append(m.Serialize(), 0xEE) // trailing byte belongs to the next message
	n, got := Deserialize(data)
	if n != HeaderSize+5 {
		t.Fatalf("n = %d, want %d", n, HeaderSize+5)
	}
	if got.Direction != m.Direction || got.Category != m.Category || got.Command != m.Command ||
		got.Seq != m.Seq || got.Status != m.Status || got.BodySize != 5 || string(got.Body) != "hello" {
		t.Errorf("Deserialize() = %+v, want %+v", got, m)
	}
}

func TestDeserializeIncomplete(t *testing.T) {
	full := (&Message{Body: []byte{1, 2, 3}}).Serialize()
	for _, n := range []int{0, 1, HeaderSize - 1, HeaderSize, len(full) - 1} {
		if k, m := Deserialize(full[:n]); k != 0 || m != nil {
			t.Errorf("Deserialize(%d bytes) = %d, %v; want 0, nil", n, k, m)
		}
	}
}

func TestConstructors(t *testing.T) {
	req := &Message{Command: 0x4242, Seq: 9}
	cases := []struct {
		name     string
		m        *Message
		dir, cat byte
		command  uint16
		keepSeq  bool
	}{
		{"ServerQuestion", NewServerQuestion(0x10), ServerToClient, CategoryQuestion, 0x10, false},
		{"ServerAnswer", NewServerAnswer(req), ServerToClient, CategoryAnswer, 0x4242, true},
		{"ServerNotice", NewServerNotice(0x11), ServerToClient, CategoryNotice, 0x11, false},
		{"ClientQuestion", NewClientQuestion(0x12), ClientToServer, CategoryQuestion, 0x12, false},
		{"ClientAnswer", NewClientAnswer(req), ClientToServer, CategoryAnswer, 0x4242, true},
		{"ClientNotice", NewClientNotice(0x13), ClientToServer, CategoryNotice, 0x13, false},
	}
	for _, c := range cases {
		if c.m.Direction != c.dir || c.m.Category != c.cat || c.m.Command != c.command || c.m.Status != StatusSuccess {
			t.Errorf("%s: got %+v", c.name, c.m)
		}
		if c.keepSeq && c.m.Seq != req.Seq {
			t.Errorf("%s: Seq = %d, want request seq %d", c.name, c.m.Seq, req.Seq)
		}
	}
	if a, b := NewServerNotice(1).Seq, NewServerNotice(1).Seq; a == b {
		t.Errorf("new messages share seq %d", a)
	}
}

func TestSequenceGeneratorWraps(t *testing.T) {
	g := sequenceGenerator()
	if got := g(); got != 2 {
		t.Fatalf("first seq = %d, want 2", got)
	}
	var last uint16
	for i := 0; i < 0xFFFC; i++ {
		last = g()
	}
	if last != 0xFFFE {
		t.Fatalf("seq before wrap = %d, want 0xFFFE", last)
	}
	if a, b := g(), g(); a != 0 || b != 1 {
		t.Errorf("seq after wrap = %d, %d; want 0, 1", a, b)
	}
}

func TestWriterReaderRoundTrip(t *testing.T) {
	m := NewServerNotice(0x1)
	w := m.Writer()
	w.Write8(0x01).Write16(0x0203).Write32(0x04050607).WriteString("ガンダムZ").WriteString("")
	if n, err := w.Write([]byte{0x99}); n != 1 || err != nil {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if w.Msg() != m || w.BodyLen() != len(m.Body) || int(m.BodySize) != len(m.Body) {
		t.Fatalf("writer state: BodyLen %d, BodySize %d, len(Body) %d", w.BodyLen(), m.BodySize, len(m.Body))
	}

	// the wire form survives a Serialize/Deserialize trip
	_, m2 := Deserialize(m.Serialize())
	r := m2.Reader()
	if v := r.Read8(); v != 0x01 {
		t.Errorf("Read8 = %x", v)
	}
	if v := r.Read16(); v != 0x0203 {
		t.Errorf("Read16 = %x", v)
	}
	if v := r.Read32(); v != 0x04050607 {
		t.Errorf("Read32 = %x", v)
	}
	// ReadString returns the raw Shift_JIS bytes; only WriteString encodes
	sjis := r.ReadString()
	if want := "\x83\x4b\x83\x93\x83\x5f\x83\x80\x5a"; sjis != want {
		t.Errorf("ReadString = % x, want % x", sjis, want)
	}
	if v := r.ReadString(); v != "" {
		t.Errorf("empty ReadString = %q", v)
	}
	if r.Remaining() != 1 || r.Read8() != 0x99 || r.Remaining() != 0 {
		t.Errorf("tail byte not read back")
	}
	if v := r.ReadString(); v != "" {
		t.Errorf("ReadString at end = %q", v)
	}
}

func TestReadStringTrimsNul(t *testing.T) {
	m := &Message{Body: []byte{0x00, 0x04, 'a', 'b', 0x00, 0x00}}
	if got := m.Reader().ReadString(); got != "ab" {
		t.Errorf("ReadString = %q, want ab", got)
	}
}

func TestLittleEndianWriters(t *testing.T) {
	m := &Message{}
	m.Writer().Write8LE(0x01).Write16LE(0x0203).Write32LE(0x04050607)
	want := []byte{0x01, 0x03, 0x02, 0x07, 0x06, 0x05, 0x04}
	if !bytes.Equal(m.Body, want) {
		t.Errorf("Body = % x, want % x", m.Body, want)
	}
}

func TestReadEncryptedStringRejects(t *testing.T) {
	// "＾ゞ）" at seq 405, see TestMessageBodyReader_ReadEncryptedString
	good := []byte{0x00, 0x08, 0x02, 0x91, 0x01, 0xcd, 0x0c, 0xd1, 0x03, 0xf4}
	bad := append([]byte{}, good...)
	bad[3]++ // checksum
	cases := map[string][]byte{
		"empty":     {},
		"size<=2":   {0x00, 0x02, 0x00, 0x00},
		"truncated": good[:7],
		"checksum":  bad,
	}
	for name, body := range cases {
		m := &Message{Seq: 405, Body: body}
		if got := m.Reader().ReadEncryptedString(); got != "" {
			t.Errorf("%s: ReadEncryptedString = %q, want empty", name, got)
		}
	}
	if got := (&Message{Seq: 405, Body: good}).Reader().ReadEncryptedString(); got != "＾ゞ）" {
		t.Errorf("control: ReadEncryptedString = %q", got)
	}
}

func TestString(t *testing.T) {
	cases := []struct {
		m    *Message
		want string
	}{
		{&Message{Direction: ClientToServer, Category: CategoryQuestion, Command: 0xAB, Seq: 3}, "C->S [Q] ID:0xAB Seq:3 Body(0 bytes):"},
		{&Message{Direction: ServerToClient, Category: CategoryAnswer, Body: []byte{1}}, "C<-S [A] ID:0x0 Seq:0 Body(1 bytes):"},
		{&Message{Direction: ServerToClient, Category: CategoryNotice}, "C<-S [N]"},
		{&Message{Direction: ServerToClient, Category: CategoryCustom}, "C<-S [C]"},
	}
	for _, c := range cases {
		if got := c.m.String(); !strings.HasPrefix(got, c.want) {
			t.Errorf("String() = %q, want prefix %q", got, c.want)
		}
	}
}
