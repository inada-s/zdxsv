package proto

import "testing"

func TestFinScanner(t *testing.T) {
	fin := []byte{4, 0xF0, 0, 0}
	other := []byte{6, 1, 2, 3, 4, 5}
	// A frame whose payload holds the fin bytes is not a fin.
	wrapped := []byte{6, 4, 0xF0, 0, 0, 9}
	cat := func(bs ...[]byte) []byte {
		var r []byte
		for _, b := range bs {
			r = append(r, b...)
		}
		return r
	}

	tests := []struct {
		name  string
		reads [][]byte
		want  []bool
	}{
		{"fin alone", [][]byte{other, fin}, []bool{false, true}},
		{"fin after a frame in one read", [][]byte{cat(other, fin)}, []bool{true}},
		{"fin before a frame in one read", [][]byte{cat(fin, other)}, []bool{true}},
		{"fin split across reads", [][]byte{cat(other, fin[:2]), fin[2:]}, []bool{false, true}},
		{"frame split, then fin", [][]byte{other[:3], cat(other[3:], fin)}, []bool{false, true}},
		{"fin bytes inside a frame", [][]byte{wrapped}, []bool{false}},
		{"fin bytes inside a split frame", [][]byte{wrapped[:1], wrapped[1:]}, []bool{false, false}},
		{"no fin", [][]byte{other, cat(other, other)}, []bool{false, false}},
		{"zero size: exact fin read still found", [][]byte{{0, 1}, wrapped, fin}, []bool{false, false, true}},
	}
	join := append([]byte{130}, make([]byte, PS2JoinSize-1)...)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s FinScanner
			if s.Scan(join) {
				t.Fatal("fin in the join")
			}
			for i, r := range tt.reads {
				if got := s.Scan(r); got != tt.want[i] {
					t.Errorf("read %d % x: got %v, want %v", i, r, got, tt.want[i])
				}
			}
		})
	}
}

func TestFinScannerJoin(t *testing.T) {
	fin := []byte{4, 0xF0, 0, 0}
	join := append([]byte{130}, make([]byte, PS2JoinSize-1)...)
	tests := []struct {
		name  string
		reads [][]byte
		want  []bool
	}{
		// 130 is not a frame size: framing it would swallow the fin.
		{"join, then fin", [][]byte{join, fin}, []bool{false, true}},
		{"join split across reads", [][]byte{join[:10], join[10:], fin}, []bool{false, false, true}},
		// The rest of the join's last read is dropped, as the battle server does.
		{"join read longer than the join", [][]byte{append(join, 9, 9), fin}, []bool{false, true}},
		{"fin before the join is complete", [][]byte{join[:10], fin}, []bool{false, false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s FinScanner
			for i, r := range tt.reads {
				if got := s.Scan(r); got != tt.want[i] {
					t.Errorf("read %d % x: got %v, want %v", i, r, got, tt.want[i])
				}
			}
		})
	}
}
