package main

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

type BaseMcsPeer struct {
	sessionID string
	userID    string
	position  int
	roomID    int
}

func (p *BaseMcsPeer) SetUserID(userID string) {
	p.userID = userID
}

func (p *BaseMcsPeer) SetSessionID(sessionID string) {
	p.sessionID = sessionID
}

func (p *BaseMcsPeer) SessionID() string {
	return p.sessionID
}

func (p *BaseMcsPeer) UserID() string {
	return p.userID
}

func (p *BaseMcsPeer) SetPosition(pos int) {
	p.position = pos
}

func (p *BaseMcsPeer) Position() int {
	return p.position
}

func (p *BaseMcsPeer) SetRoomID(id int) {
	p.roomID = id
}

func (p *BaseMcsPeer) RoomID() int {
	return p.roomID
}

type McsPeer interface {
	SetUserID(string)
	SetSessionID(string)
	UserID() string
	SessionID() string
	SetPosition(int)
	Position() int
	SetRoomID(int)
	RoomID() int
	AddSendData([]byte)
	AddSendMessage(McsMessage)
	Address() string
	Close() error
}

const MaxRoom = 128

type McsUser struct {
	UserID    string
	SessionID string
	Name      string
	Team      string
	Entry     byte
	P2PMap    map[string]struct{}
}

type BattleInfo struct {
	Users  []McsUser
	RoomID int

	expires time.Time
	room    *McsRoom
}

// mcsWaitingTTL bounds the wait for a user who never joins, such as one who
// plays the battle without the battle server.
const mcsWaitingTTL = 5 * time.Minute

type Mcs struct {
	mtx        sync.Mutex
	nextRoomID int
	waiting    map[string]*BattleInfo
	rooms      map[int]*McsRoom
	now        func() time.Time
}

func NewMcs() *Mcs {
	return &Mcs{
		waiting: make(map[string]*BattleInfo),
		rooms:   make(map[int]*McsRoom),
		now:     time.Now,
	}
}

// AddBattle takes the users of a battle the lobby started. Each joins by its session id.
func (m *Mcs) AddBattle(users []McsUser) {
	m.mtx.Lock()
	now := m.now()
	for id, info := range m.waiting {
		if now.After(info.expires) {
			delete(m.waiting, id)
		}
	}
	info := &BattleInfo{Users: users, RoomID: m.nextRoomID, expires: now.Add(mcsWaitingTTL)}
	info.room = newMcsRoom(info.RoomID)
	info.room.onEmpty = m.removeRoom
	m.nextRoomID++
	for _, u := range users {
		m.waiting[u.SessionID] = info
	}
	m.mtx.Unlock()
	logger.Info("battle users", zap.Any("info", *info))
}

func (m *Mcs) GetBattleInfo(sessionID string) *BattleInfo {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	return m.waitingInfo(sessionID)
}

func (m *Mcs) waitingInfo(sessionID string) *BattleInfo {
	info := m.waiting[sessionID]
	if info != nil && m.now().After(info.expires) {
		delete(m.waiting, sessionID)
		return nil
	}
	return info
}

func (m *Mcs) FindWaitingUser(sessionID string) (*McsUser, bool) {
	return findMcsUser(m.GetBattleInfo(sessionID), sessionID)
}

func findMcsUser(info *BattleInfo, sessionID string) (*McsUser, bool) {
	if info == nil {
		floodLog.Error("BattleInfo not found", zap.Any("session_id", sessionID))
		return nil, false
	}
	for _, u := range info.Users {
		if sessionID == u.SessionID {
			return &u, true
		}
	}
	floodLog.Error("user not found in BattleInfo", zap.Any("session_id", sessionID))
	return nil, false
}

func (m *Mcs) Join(p McsPeer, sessionID string) *McsRoom {
	m.mtx.Lock()
	info := m.waitingInfo(sessionID)
	user, ok := findMcsUser(info, sessionID)
	if !ok {
		m.mtx.Unlock()
		return nil
	}
	delete(m.waiting, sessionID)
	room := info.room
	m.rooms[room.id] = room
	p.SetUserID(user.UserID)
	p.SetSessionID(sessionID)
	room.Join(p)
	m.mtx.Unlock()
	return room
}

func (m *Mcs) removeRoom(r *McsRoom) {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	r.RLock()
	defer r.RUnlock()
	for _, p := range r.peers {
		if p != nil {
			return
		}
	}
	if m.rooms[r.id] == r {
		delete(m.rooms, r.id)
	}
}

// ParseSessionID decodes the session id field of a battle join: always 10 digits.
func ParseSessionID(value string) (string, error) {
	if len(value) != 10 {
		return "", fmt.Errorf("Invalid value length")
	}
	return DecodeSessionID(value)
}

func IsFinData(buf []byte) bool {
	if len(buf) == 4 &&
		buf[0] == 4 &&
		buf[1] == 240 &&
		buf[2] == 0 &&
		buf[3] == 0 {
		return true
	}
	return false
}

type McsRoom struct {
	sync.RWMutex

	id    int
	peers []McsPeer // 追加はappend 削除はnil代入 インデックスがposと一致するように維持

	onEmpty func(*McsRoom)
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
			if logDebugEnabled() {
				logger.Debug("room message", zap.Any("from", peer.UserID()), zap.Any("to", other.UserID()), zap.Binary("body", msg.Body))
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
		if r.onEmpty != nil {
			r.onEmpty(r)
		}
	}

	logger.Info("leave peer", zap.Any("addr", p.Address()))
}
