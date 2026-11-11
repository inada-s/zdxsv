package battle

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"

	"zdxsv/pkg/message"
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
}

type Mcs struct {
	mtx        sync.Mutex
	nextRoomID int
	waiting    map[string]*BattleInfo
	rooms      []*McsRoom
}

func NewMcs() *Mcs {
	return &Mcs{
		waiting: make(map[string]*BattleInfo),
		rooms:   make([]*McsRoom, MaxRoom),
	}
}

// AddBattle takes the users of a battle the lobby started. Each joins by its session id.
func (m *Mcs) AddBattle(users []McsUser) {
	m.mtx.Lock()
	info := &BattleInfo{Users: users, RoomID: m.nextRoomID}
	m.nextRoomID = (m.nextRoomID + 1) % MaxRoom
	for _, u := range users {
		m.waiting[u.SessionID] = info
	}
	m.mtx.Unlock()
	zlog.Info("battle users", zap.Any("info", *info))
}

func (m *Mcs) GetBattleInfo(sessionID string) *BattleInfo {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	return m.waiting[sessionID]
}

func (m *Mcs) FindWaitingUser(sessionID string) (*McsUser, bool) {
	return findMcsUser(m.GetBattleInfo(sessionID), sessionID)
}

func findMcsUser(info *BattleInfo, sessionID string) (*McsUser, bool) {
	if info == nil {
		zlog.Error("BattleInfo not found", zap.Any("session_id", sessionID))
		return nil, false
	}
	for _, u := range info.Users {
		if sessionID == u.SessionID {
			return &u, true
		}
	}
	zlog.Error("user not found in BattleInfo", zap.Any("session_id", sessionID))
	return nil, false
}

func (m *Mcs) Join(p McsPeer, sessionID string) *McsRoom {
	m.mtx.Lock()
	info := m.waiting[sessionID]
	user, ok := findMcsUser(info, sessionID)
	if !ok {
		m.mtx.Unlock()
		return nil
	}
	delete(m.waiting, sessionID)
	room := m.rooms[info.RoomID]
	if room == nil {
		room = newMcsRoom(info.RoomID)
		m.rooms[info.RoomID] = room
	}
	m.mtx.Unlock()

	p.SetUserID(user.UserID)
	p.SetSessionID(sessionID)
	room.Join(p)
	return room
}

// ParseSessionID decodes the session id field of a battle join: always 10 digits.
func ParseSessionID(value string) (string, error) {
	if len(value) != 10 {
		return "", fmt.Errorf("Invalid value length")
	}
	return message.DecodeSessionID(value)
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
