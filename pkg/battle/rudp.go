package battle

import "sync"

const ringSize = 4096

// sendMax caps the messages of one packet.
const sendMax = 50

// BattleBuffer keeps the messages sent to one peer until the peer acks them.
type BattleBuffer struct {
	mtx   sync.Mutex
	id    string
	ack   uint32 // the last seq received from the peer
	begin uint32 // the first seq not acked yet
	end   uint32 // the next seq to give
	ring  []McsMessage
}

func NewBattleBuffer(id string) *BattleBuffer {
	return &BattleBuffer{id: id, begin: 1, end: 1, ring: make([]McsMessage, ringSize)}
}

func (b *BattleBuffer) SetID(id string) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.id = id
}

func (b *BattleBuffer) ID() string {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return b.id
}

func (b *BattleBuffer) Push(msg McsMessage) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.ring[b.end%ringSize] = msg
	b.end++
}

// AppendPacket appends a battle packet of the unacked messages to dst.
func (b *BattleBuffer) AppendPacket(dst []byte) []byte {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	e := b.end
	if b.begin+sendMax < e {
		e = b.begin + sendMax
	}
	l, r := b.begin%ringSize, e%ringSize
	var msgs []McsMessage
	if l <= r {
		msgs = b.ring[l:r]
	} else {
		msgs = append(append(msgs, b.ring[l:]...), b.ring[:r]...)
	}
	return AppendBattle(dst, e-1, b.ack, msgs)
}

func (b *BattleBuffer) ApplySeqAck(seq, ack uint32) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.begin = ack + 1
	b.ack = seq
}

// MessageFilter numbers the messages a user sends and passes each received
// message once and in order, only from the accepted users.
type MessageFilter struct {
	mtx     sync.Mutex
	seq     uint32
	recvSeq map[string]uint32
}

func NewMessageFilter(acceptIDs []string) *MessageFilter {
	mf := &MessageFilter{seq: 1, recvSeq: map[string]uint32{}}
	for _, id := range acceptIDs {
		mf.recvSeq[id] = 0
	}
	return mf
}

func (m *MessageFilter) GenerateMessage(userID string, data []byte) McsMessage {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	msg := McsMessage{UserID: userID, Seq: m.seq, Body: data}
	m.seq++
	return msg
}

func (m *MessageFilter) Filter(msg McsMessage) bool {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	ack, ok := m.recvSeq[msg.UserID]
	if !ok {
		return false
	}
	if ack == 0 || msg.Seq == ack+1 {
		m.recvSeq[msg.UserID] = msg.Seq
		return true
	}
	return false
}
