package proto

// FinScanner finds the battle end frame (04 F0 00 00) in a PS2 TCP stream.
// The stream is a run of frames whose first byte is the frame size; a read
// can hold several frames or end inside one, so the scanner keeps the
// partial frame between reads. It must see the stream from its first byte.
type FinScanner struct {
	frame []byte
	lost  bool
}

// Scan consumes one read and reports whether a fin frame completed in it.
func (s *FinScanner) Scan(data []byte) bool {
	if s.lost {
		// Without framing only a read that is exactly the fin frame is trusted.
		return len(data) == 4 && data[0] == 4 && data[1] == 0xF0 && data[2] == 0 && data[3] == 0
	}
	fin := false
	for 0 < len(data) {
		if len(s.frame) == 0 && data[0] == 0 {
			// A zero size frame cannot be stepped over: framing is lost.
			s.lost = true
			break
		}
		size := int(data[0])
		if 0 < len(s.frame) {
			size = int(s.frame[0])
		}
		n := size - len(s.frame)
		if len(data) < n {
			n = len(data)
		}
		s.frame = append(s.frame, data[:n]...)
		data = data[n:]
		if len(s.frame) == size {
			if size == 4 && s.frame[1] == 0xF0 && s.frame[2] == 0 && s.frame[3] == 0 {
				fin = true
			}
			s.frame = s.frame[:0]
		}
	}
	return fin
}
