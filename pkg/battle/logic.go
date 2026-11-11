package battle

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"

	"zdxsv/pkg/message"
)

type BasePeer struct {
	sessionID string
	userID    string
	position  int
	roomID    int
}

func (p *BasePeer) SetUserID(userID string) {
	p.userID = userID
}

func (p *BasePeer) SetSessionID(sessionID string) {
	p.sessionID = sessionID
}

func (p *BasePeer) SessionID() string {
	return p.sessionID
}

func (p *BasePeer) UserID() string {
	return p.userID
}

func (p *BasePeer) SetPosition(pos int) {
	p.position = pos
}

func (p *BasePeer) Position() int {
	return p.position
}

func (p *BasePeer) SetRoomID(id int) {
	p.roomID = id
}

func (p *BasePeer) RoomID() int {
	return p.roomID
}

type Peer interface {
	SetUserID(string)
	SetSessionID(string)
	UserID() string
	SessionID() string
	SetPosition(int)
	Position() int
	SetRoomID(int)
	RoomID() int
	AddSendData([]byte)
	AddSendMessage(Message)
	Address() string
	Close() error
}

const MaxRoom = 128

type User struct {
	UserID    string
	SessionID string
	Name      string
	Team      string
	Entry     byte
	P2PMap    map[string]struct{}
}

type BattleInfo struct {
	Users  []User
	RoomID int
}

type Logic struct {
	mtx        sync.Mutex
	nextRoomID int
	waiting    map[string]*BattleInfo
	rooms      []*Room
}

func NewLogic() *Logic {
	return &Logic{
		waiting: make(map[string]*BattleInfo),
		rooms:   make([]*Room, MaxRoom),
	}
}

// AddBattle takes the users of a battle the lobby started. Each joins by its session id.
func (m *Logic) AddBattle(users []User) {
	m.mtx.Lock()
	info := &BattleInfo{Users: users, RoomID: m.nextRoomID}
	m.nextRoomID = (m.nextRoomID + 1) % MaxRoom
	for _, u := range users {
		m.waiting[u.SessionID] = info
	}
	m.mtx.Unlock()
	zlog.Info("battle users", zap.Any("info", *info))
}

func (m *Logic) GetBattleInfo(sessionID string) *BattleInfo {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	return m.waiting[sessionID]
}

func (m *Logic) FindWaitingUser(sessionID string) (*User, bool) {
	return findUser(m.GetBattleInfo(sessionID), sessionID)
}

func findUser(info *BattleInfo, sessionID string) (*User, bool) {
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

func (m *Logic) Join(p Peer, sessionID string) *Room {
	m.mtx.Lock()
	info := m.waiting[sessionID]
	user, ok := findUser(info, sessionID)
	if !ok {
		m.mtx.Unlock()
		return nil
	}
	delete(m.waiting, sessionID)
	room := m.rooms[info.RoomID]
	if room == nil {
		room = newRoom(info.RoomID)
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
