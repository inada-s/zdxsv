package battle

import (
	"encoding/binary"
	"encoding/hex"
	"sync"

	"github.com/golang/glog"

	"zdxsv/pkg/proto"
)

type Room struct {
	sync.RWMutex

	id    int
	peers []Peer // 追加はappend 削除はnil代入 インデックスがposと一致するように維持

	last6Fr  []int
	lastHash []int
}

func newRoom(id int) *Room {
	return &Room{id: id}
}

const (
	PadUp     = 0x2000
	PadDown   = 0x1000
	PadLeft   = 0x0800
	PadRight  = 0x0400
	PadCircle = 0x0020
	PadCross  = 0x0040
	PadTri    = 0x0100
	PadRect   = 0x0200
	PadStart  = 0x8000
	PadSelect = 0x4000
	PadL1     = 0x0080
	PadL2     = 0x0008
	PadL3     = 0x0002
	PadR1     = 0x0010
	PadR2     = 0x0004
)

func padString(pad1, pad2 uint16) string {
	ret := ""
	if pad2&PadUp != 0 {
		ret += " ↑"
	}
	if pad2&PadDown != 0 {
		ret += " ↓"
	}
	if pad2&PadLeft != 0 {
		ret += " ←"
	}
	if pad2&PadRight != 0 {
		ret += " →"
	}

	if pad1&PadUp != 0 {
		ret += " L↑"
	}
	if pad1&PadDown != 0 {
		ret += " L↓"
	}
	if pad1&PadLeft != 0 {
		ret += " L←"
	}
	if pad1&PadRight != 0 {
		ret += " L→"
	}

	if pad2&PadCircle != 0 {
		ret += " ○"
	}
	if pad2&PadCross != 0 {
		ret += " ×"
	}
	if pad2&PadTri != 0 {
		ret += " △"
	}
	if pad2&PadRect != 0 {
		ret += " □"
	}
	if pad2&PadStart != 0 {
		ret += " St"
	}
	if pad2&PadSelect != 0 {
		ret += " Sl"
	}
	if pad2&PadL1 != 0 {
		ret += " L1"
	}
	if pad2&PadL2 != 0 {
		ret += " L2"
	}
	if pad2&PadR1 != 0 {
		ret += " R1"
	}
	if pad2&PadR2 != 0 {
		ret += " R2"
	}
	return ret
}

func (r *Room) SendMessage(peer Peer, msg *proto.BattleMessage) {
	k := peer.Position()
	r.RLock()
	lastFr := r.last6Fr[k]
	lastHash := r.lastHash[k]
	/*
		for _, fr := range r.last6Fr {
			if lastFr < fr {
				lastFr = fr
			}
		}
	*/
	r.RUnlock()

	if true {
		body := msg.GetBody()
		fid := byte(0)
		flag := false
		hash := byte(0)
		_ = fid
		_ = flag
		_ = hash

		for 0 < len(body) {
			x := body[0]
			switch x {
			case 6:
				// 2fr nop
				// [0] := 6 (length)
				// [1] := player_id | 0x20
				// [2] := frame_id | 128
				// [3] := hash
				// [4] := frame_id | 128
				// [5] := hash

				// fid = body[2] & 0x7F
				// flag? = (body[2] >> 7) == 1
				// hash = body[3]
				// fid = body[4] & 0x7F
				// flag? = (body[4] >> 7) == 1
				// hash = body[5]

				r.Lock()
				r.last6Fr[k] = int(body[4] & 0x7F)
				r.lastHash[k] = int(body[5])
				r.Unlock()
			case 12:
				// 2fr update pad state (single chane)

				// lastnop0620b686b75d
				// [12] TTNCHY>XMMFXD 0c21003880003e 5d 8001 b9 5d
				// lastnop0620ba5dbb5d
				// [12] TTNCHY>XMMFXD 0c21003c0000663f0001bd3f
				// lastnop0620bc3fbd3f
				// [12] TTNCHY>XMMFXD 0c21be3f003f8000663f8001

				if body[2] == 0 {
					// #TypeA ([2] == 00)
					// [0] := c (length)
					// [1] := player_id | 0x20
					// [2] := 00 (TypeA)
					// [3] := ?
					// [4-5] := pad state
					// [6] := ?
					// [7] := hash
					// [8-9] := pad state
					// [10] := frame_id | 128
					// [11] := hash

					// plid := body[1]
					/*
						unk1 := body[3]
						pad1 := binary.BigEndian.Uint16(body[4:6])
						unk2 := body[6]
						has1 := body[7]
						pad2 := binary.BigEndian.Uint16(body[8:10])
						frid := body[10]
						has2 := body[11]
						glog.Info("TypeA")
							// glog.Info("pid", plid)
						glog.Infof("fr %x", frid)
						glog.Infof("unk %x %x", unk1, unk2)
						glog.Infof("pad %x %x", pad1, pad2)
						glog.Infof("has %x %x", has1, has2)
					*/

					if false {
						pad1 := binary.BigEndian.Uint16(body[4:6])
						pad2 := binary.BigEndian.Uint16(body[8:10])
						glog.Info(padString(pad1, pad2))
					}

					r.Lock()
					r.last6Fr[k] = int(body[10] & 0x7F)
					r.lastHash[k] = int(body[11])
					r.Unlock()

					/*
						fr := int(body[10] & 0x7F)
						if lastFr < fr {
							glog.Infoln(peer.UserId(), "Delay ", fr-lastFr)
						}
					*/
				} else {
					// #TypeB ([2] != 00)
					// [0] := c (length)
					// [1] := player_id | 0x20
					// [2] := frame_id | 128
					// [3] := hash
					// [4] := 00 ?
					// [5] := ?
					// [6-7] := pad state
					// [8] := ?
					// [9] := hash
					// [10-11] := pad state

					/*
						glog.Info("TypeB")
						// plid := body[1]
						frid := body[2]
						has1 := body[3]
						zero := body[4]
						unk1 := body[5]
						pad1 := binary.BigEndian.Uint16(body[6:8])
						unk2 := body[8]
						has2 := body[9]
						pad2 := binary.BigEndian.Uint16(body[10:12])
						// glog.Info("pid", plid)
						glog.Infof("fr %x", frid)
						glog.Infof("unk %x %x", unk1, unk2)
						glog.Infof("pad %x %x", pad1, pad2)
						glog.Infof("has %x %x", has1, has2)
						if zero != 0 {
							glog.Error("expected zero in typeB")
						}
					*/

					if false {
						pad1 := binary.BigEndian.Uint16(body[6:8])
						pad2 := binary.BigEndian.Uint16(body[10:12])
						glog.Info(padString(pad1, pad2))
					}
					r.Lock()
					r.last6Fr[k] = int(body[2]&0x7F) + 1
					r.lastHash[k] = int(body[9])
					r.Unlock()

					/*
						fr := int(body[2] & 0x7F)
						if lastFr < fr {
							glog.Infoln(peer.UserId(), "Delay ", fr-lastFr)
						}
					*/
				}

				// glog.Info("lastnop", lastnop)
				// glog.Infof("[12] %v %v", peer.UserId(), hex.EncodeToString(body[:x]))
			case 4:
				{
					// [0] = 4 (length)
					// [1] = (type << 4) | player_id
					// [2] = dat1
					// [3] = dat2

					// Type 1 : after join a room     ex: 04100000 04110000
					// Type 3 : after scene change    ex: 04300000 04310000
					// Type 7 : wait a player?        ex: 0471000e
					// Type 9 : sync (every 160 fr ?) ex: 04910200 04910100
					// glog.Info("lastnop", lastnop)
					glog.Infof("lastFr:%x(%x) lastHash:%x", lastFr, lastFr|0x80, lastHash)
					glog.Infof("[4] %v %v", peer.UserId(), hex.EncodeToString(body[:x]))
				}
			case 18:
				{
					// 2fr update pad state (double chane)
					// ex: Press X 1fr
					// lastFr:1b lastHash:30
					// 12 20 001c 0080 17 3c 0041 001d 0000 7b 8d 0001
					// [0] := 12 (length)
					// [1] := player_id | 0x20
					// [2] := 0 always zero
					// [3] := frame_id 1c
					// [4-5] := pad1-1 0080
					// [6] := ? 17
					// [7] := ? hash
					// [8-9] := pad2-1 0041
					// [10] := 00 always zero
					// [11] := frame_id 1d
					// [12-13] := pad1-2 0000
					// [14] := ? 7b
					// [15] := ? hash
					// [16-17] := pad2-2 0001

					// RDown 1fr
					// lastFr:38 lastHash:df
					// 12 20 00 39 1000 40 df 0001 003a 0000 40 df 0001

					if true {
						pad11 := binary.BigEndian.Uint16(body[4:6])
						pad21 := binary.BigEndian.Uint16(body[8:10])
						glog.Info(padString(pad11, pad21))
					}
					if true {
						pad11 := binary.BigEndian.Uint16(body[12:14])
						pad21 := binary.BigEndian.Uint16(body[16:18])
						glog.Info(padString(pad11, pad21))
					}

					r.Lock()
					r.last6Fr[k] = int(body[11] & 0x7F)
					r.lastHash[k] = int(body[15])
					r.Unlock()
					glog.Infof("lastFr:%x(%x) lastHash:%x", lastFr, lastFr|0x80, lastHash)
					glog.Infof("[18] %v %v", peer.UserId(), hex.EncodeToString(body[:x]))
				}
			default:
				glog.Errorln("Unkown Length", x)
			}
			body = body[x:]
		}
	}

	if true {
		body := msg.GetBody()
		for i := 0; i < len(body); {
			x := body[i]
			switch x {
			case 4:
				if body[i+1] == 0x90 {
					glog.Infof("[mod]1")
					body[i+1] = 0x10
					body[i+2] = 0x00
					body[i+3] = 0x00
				}
				if body[i+1] == 0x91 {
					glog.Infof("[mod]2")
					body[i+1] = 0x10
					body[i+2] = 0x00
					body[i+3] = 0x00
				}
				i += int(x)
			default:
				i += int(x)
			}
		}
		msg.Body = body
	}

	if glog.V(2) {
		r.RLock()
		delta := r.last6Fr[k] - lastFr
		if delta < 0 {
			delta = r.last6Fr[k] - lastFr + 64
		}
		if delta != 2 {
			glog.Infof("Fr %v > %v (delta %v)", lastFr, r.last6Fr[k], delta)
			glog.Infof("%v %v", msg.GetUserId(), hex.EncodeToString(msg.GetBody()))
		}
		r.RUnlock()
	}

	r.RLock()
	for i := 0; i < len(r.peers); i++ {
		if i == k {
			continue
		}

		other := r.peers[i]
		if other != nil {
			if glog.V(2) {
				// glog.Infof("[ROOM] %v>%v %v", peer.UserId(), other.UserId(), hex.EncodeToString(msg.GetBody()))
			}
			other.AddSendMessage(msg)
		}
	}
	r.RUnlock()
}

func (r *Room) Clear() {
	r.Lock()
	for i := 0; i < len(r.peers); i++ {
		r.peers[i] = nil
	}
	r.peers = r.peers[:0]
	r.Unlock()
}

func (r *Room) Join(p Peer) {
	p.SetRoomId(r.id)
	r.Lock()
	p.SetPosition(len(r.peers))
	r.peers = append(r.peers, p)
	r.last6Fr = append(r.last6Fr, 0)
	r.lastHash = append(r.lastHash, 0)
	r.Unlock()
}

func (r *Room) Leave(p Peer) {
	pos := p.Position()

	r.Lock()
	if pos < len(r.peers) {
		r.peers[pos] = nil
	}
	empty := true
	for i := 0; i < len(r.peers); i++ {
		if r.peers[i] != nil {
			empty = false
			break
		}
	}
	r.Unlock()
	if empty {
		r.Clear()
	}

	glog.Infof("leave peer %v", p.Address())
}
