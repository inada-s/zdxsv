package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestMessageBodyReader_ReadEncryptedString(t *testing.T) {
	type fields struct {
		seq uint16
		r   *bytes.Reader
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			"example1",
			fields{401, bytes.NewReader([]byte{0x00, 0x22, 0x14, 0x80, 0x06, 0x37, 0x0b, 0x71, 0x1c, 0x4b, 0x0f, 0x3b, 0x1e, 0x53, 0x1e, 0x11, 0x04, 0x45, 0x17, 0x40, 0x16, 0x23, 0x1b, 0x54, 0x0c, 0x20, 0x1f, 0x4b, 0x2e, 0x5f, 0x23, 0x60, 0x34, 0x19, 0x27, 0x0e, 0x00})},
			"こんにちは初めましてごめんなさい",
		},
		{
			"example2",
			fields{402, bytes.NewReader([]byte{0x00, 0x26, 0x14, 0xb3, 0x06, 0xf5, 0x0b, 0xdf, 0x1c, 0x0e, 0x0d, 0xc0, 0x1e, 0xf5, 0x12, 0x52, 0x05, 0x7f, 0x16, 0x78, 0x17, 0x20, 0x1a, 0x3a, 0x0e, 0xf1, 0x0c, 0x46, 0x2f, 0x0f, 0x22, 0x73, 0x35, 0x10, 0x26, 0x52, 0x27, 0x0c, 0x29, 0xef, 0x00})},
			"パスワードはよろしく～戦いませんか？",
		},
		{
			"example3",
			fields{403, bytes.NewReader([]byte{0x00, 0x28, 0x17, 0x4f, 0x04, 0x3f, 0x0d, 0x6d, 0x1e, 0x3b, 0x11, 0x71, 0x09, 0x12, 0x15, 0x21, 0x06, 0x44, 0x19, 0x25, 0x18, 0xc5, 0x16, 0x57, 0x0e, 0x25, 0x21, 0x76, 0x2f, 0xcc, 0x25, 0x19, 0x36, 0x08, 0x29, 0x4a, 0x24, 0x0e, 0x2d, 0x60, 0x3e, 0x04, 0x00})},
			"そろそろ落ちます参加しま～すありがとう",
		},
		{
			"example4",
			fields{404, bytes.NewReader([]byte{0x00, 0x24, 0x15, 0x35, 0x10, 0x3a, 0x07, 0x75, 0x1f, 0x37, 0x06, 0x7f, 0x1d, 0x71, 0x01, 0xf1, 0x07, 0x52, 0x18, 0x24, 0x15, 0x2e, 0x1f, 0xf5, 0x18, 0x3b, 0x2b, 0x07, 0x22, 0x47, 0x24, 0x45, 0x37, 0x7b, 0x2b, 0xc1, 0x25, 0x14, 0x00})},
			"了解お疲れ様でした～部屋作りま～す",
		},
		{
			"Issue 22",
			fields{266, bytes.NewReader([]byte{0x00, 0x08, 0x02, 0xb2, 0x8f, 0x6b, 0x82, 0x63, 0x90, 0x5c, 0x00})},
			"ＧＭⅡ",
		},
		{
			"face",
			fields{405, bytes.NewReader([]byte{0x00, 0x08, 0x02, 0x91, 0x01, 0xcd, 0x0c, 0xd1, 0x03, 0xf4, 0x00})},
			"＾ゞ）",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &MessageBodyReader{
				seq: tt.fields.seq,
				r:   tt.fields.r,
			}
			if got := m.ReadEncryptedString(); got != tt.want {
				t.Errorf("MessageBodyReader.ReadEncryptedString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSerializeHeaderLayout(t *testing.T) {
	m := &LbsMessage{
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
	m := &LbsMessage{
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
	full := (&LbsMessage{Body: []byte{1, 2, 3}}).Serialize()
	for _, n := range []int{0, 1, HeaderSize - 1, HeaderSize, len(full) - 1} {
		if k, m := Deserialize(full[:n]); k != 0 || m != nil {
			t.Errorf("Deserialize(%d bytes) = %d, %v; want 0, nil", n, k, m)
		}
	}
}

func TestConstructors(t *testing.T) {
	req := &LbsMessage{Command: 0x4242, Seq: 9}
	cases := []struct {
		name     string
		m        *LbsMessage
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
	m := &LbsMessage{Body: []byte{0x00, 0x04, 'a', 'b', 0x00, 0x00}}
	if got := m.Reader().ReadString(); got != "ab" {
		t.Errorf("ReadString = %q, want ab", got)
	}
}

func TestLittleEndianWriters(t *testing.T) {
	m := &LbsMessage{}
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
		m := &LbsMessage{Seq: 405, Body: body}
		if got := m.Reader().ReadEncryptedString(); got != "" {
			t.Errorf("%s: ReadEncryptedString = %q, want empty", name, got)
		}
	}
	if got := (&LbsMessage{Seq: 405, Body: good}).Reader().ReadEncryptedString(); got != "＾ゞ）" {
		t.Errorf("control: ReadEncryptedString = %q", got)
	}
}

func TestString(t *testing.T) {
	cases := []struct {
		m    *LbsMessage
		want string
	}{
		{&LbsMessage{Direction: ClientToServer, Category: CategoryQuestion, Command: 0xAB, Seq: 3}, "C->S [Q] ID:0xAB Seq:3 Body(0 bytes):"},
		{&LbsMessage{Direction: ServerToClient, Category: CategoryAnswer, Body: []byte{1}}, "C<-S [A] ID:0x0 Seq:0 Body(1 bytes):"},
		{&LbsMessage{Direction: ServerToClient, Category: CategoryNotice}, "C<-S [N]"},
		{&LbsMessage{Direction: ServerToClient, Category: CategoryCustom}, "C<-S [C]"},
	}
	for _, c := range cases {
		if got := c.m.String(); !strings.HasPrefix(got, c.want) {
			t.Errorf("String() = %q, want prefix %q", got, c.want)
		}
	}
}
