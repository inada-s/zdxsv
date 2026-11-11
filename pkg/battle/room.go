package battle

import (
	"sync"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
)

type McsRoom struct {
	sync.RWMutex

	id    int
	peers []McsPeer // 追加はappend 削除はnil代入 インデックスがposと一致するように維持
}

func newMcsRoom(id int) *McsRoom {
	return &McsRoom{id: id}
}

func (r *McsRoom) SendMessage(peer McsPeer, msg McsMessage) {
	k := peer.Position()

	r.RLock()
	for i := 0; i < len(r.peers); i++ {
		if i == k {
			continue
		}

		other := r.peers[i]
		if other != nil {
			if zlog.DebugEnabled() {
				zlog.Debug("room message", zap.Any("from", peer.UserID()), zap.Any("to", other.UserID()), zap.Binary("body", msg.Body))
			}
			other.AddSendMessage(msg)
		}
	}
	r.RUnlock()
}

func (r *McsRoom) Clear() {
	r.Lock()
	for i := 0; i < len(r.peers); i++ {
		r.peers[i] = nil
	}
	r.peers = r.peers[:0]
	r.Unlock()
}

func (r *McsRoom) Join(p McsPeer) {
	p.SetRoomID(r.id)
	r.Lock()
	p.SetPosition(len(r.peers))
	r.peers = append(r.peers, p)
	r.Unlock()
}

func (r *McsRoom) Leave(p McsPeer) {
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

	zlog.Info("leave peer", zap.Any("addr", p.Address()))
}
