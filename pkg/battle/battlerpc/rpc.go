package battlerpc

import (
	"sync"

	"github.com/golang/glog"
)

const MaxRoom = 128

type BattleRpc struct {
	mNext      sync.Mutex
	nextRoomID int

	mMap       sync.RWMutex
	sessionMap map[string]*BattleInfo
}

func NewBattleRpc() *BattleRpc {
	return &BattleRpc{
		sessionMap: make(map[string]*BattleInfo),
	}
}

type User struct {
	UserID    string
	SessionID string
	Name      string
	Team      string
	Entry     byte
	P2PMap    map[string]struct{}
}

type BattleInfoArgs struct {
	Users []User
}

type BattleInfo struct {
	Users  []User
	RoomID int
}

func (m *BattleRpc) GetBattleInfo(sessionID string) *BattleInfo {
	m.mMap.Lock()
	info, _ := m.sessionMap[sessionID]
	m.mMap.Unlock()
	return info
}

func (m *BattleRpc) ClearBattleInfo(sessionID string) {
	m.mMap.Lock()
	delete(m.sessionMap, sessionID)
	m.mMap.Unlock()
}

func (m *BattleRpc) NotifyBattleUsers(args *BattleInfoArgs, reply *int) error {
	m.mNext.Lock()
	rid := m.nextRoomID
	m.nextRoomID = (m.nextRoomID + 1) % MaxRoom
	m.mNext.Unlock()

	info := &BattleInfo{
		Users:  args.Users,
		RoomID: rid,
	}

	glog.Infoln("Receive BattleInfo", *info)
	m.mMap.Lock()
	for _, u := range info.Users {
		m.sessionMap[u.SessionID] = info
	}
	m.mMap.Unlock()
	*reply = 1
	return nil
}

func (m *BattleRpc) PingPong(_ *string, reply *string) error {
	*reply = "Pong"
	return nil
}
