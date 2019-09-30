package rudp

import (
	"fmt"
)

/*
# ACK-PACKET
# -------------------------- #
# <2 Byte FFFF> <4 Byte Ack> #
# -------------------------- #
# DATA-PACKET
# ----------------------------------------------------- #
# <2 Byte Packet Size> <4 Byte Packet Index> <Raw Data> #
# ----------------------------------------------------- #
*/

const headerLength = 6
const ringSize = 1024

func uint32ToBin(dst []byte, v uint32) {
	dst[0] = byte((v >> 24) & 0xFF)
	dst[1] = byte((v >> 16) & 0xFF)
	dst[2] = byte((v >> 8) & 0xFF)
	dst[3] = byte(v & 0xFF)
}

func uint16ToBin(dst []byte, v uint16) {
	dst[0] = byte((v >> 8) & 0xFF)
	dst[1] = byte(v & 0xFF)
}

func binToUint16(bin []byte) uint16 {
	return uint16(bin[0])<<8 | uint16(bin[1])
}

func binToUint32(bin []byte) uint32 {
	return uint32(bin[0])<<24 | uint32(bin[1])<<16 |
		uint32(bin[2])<<8 | uint32(bin[3])
}

type RUDP struct {
	ack   uint32   //相手から最後に受信したID
	begin uint32   //まだ相手の応答がない開始のシーケンス番号
	end   uint32   //次に割り振るシーケンス番号
	rbuf  [][]byte //リングバッファ
}

func NewRUDP() *RUDP {
	return &RUDP{
		ack:   0,
		begin: 1,
		end:   1,
		rbuf:  make([][]byte, ringSize, ringSize),
	}
}

func (r *RUDP) AddSendData(data []byte) {
	index := r.end
	buf := &r.rbuf[index%ringSize]
	r.end++

	packetLength := headerLength + len(data)
	header := make([]byte, headerLength)
	uint16ToBin(header[0:2], uint16(packetLength))
	uint32ToBin(header[2:headerLength], index)
	if 0 < len(*buf) {
		*buf = (*buf)[:0]
	}
	*buf = append(*buf, header...)
	*buf = append(*buf, data...)
}

func (r *RUDP) getHeader() []byte {
	header := make([]byte, headerLength)
	uint16ToBin(header[0:2], 0xFFFF)
	uint32ToBin(header[2:headerLength], r.ack)
	return header
}

func (r *RUDP) GetSendData() []byte {
	result := make([]byte, 0, 128)
	result = append(result, r.getHeader()...)
	for i := r.begin; i < r.end; i++ {
		result = append(result, r.rbuf[i%ringSize]...)
		if 1200 < len(result) {
			break
		}
	}
	return result
}

func (r *RUDP) ReceiveFilter(data []byte) ([]byte, error) {
	result := make([]byte, 0, 128)
	for len(data) >= headerLength {
		size := binToUint16(data[0:2])
		index := binToUint32(data[2:headerLength])
		if size == 0xFFFF {
			data = data[headerLength:]
			r.begin = index + 1
		} else if len(data) >= int(size) {
			if index == r.ack+1 {
				result = append(result, data[headerLength:size]...)
				r.ack++
			}
			data = data[size:]
		} else {
			return nil, fmt.Errorf("not match header. size=%v index=%v ack=%v", size, index, r.ack)
		}
	}
	return result, nil
}
