package battlerpc

import (
	"sync"

	"github.com/golang/glog"
)

const MaxRoom = 128

type BattleRpc struct {
	mNext      sync.Mutex
	nextRoomId int

	mMap       sync.RWMutex
	sessionMap map[string]*BattleInfo
}

func NewBattleRpc() *BattleRpc {
	return &BattleRpc{
		sessionMap: make(map[string]*BattleInfo),
	}
}

type User struct {
	UserId    string
	SessionId string
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
	RoomId int
}

func (m *BattleRpc) GetBattleInfo(sessionId string) *BattleInfo {
	m.mMap.Lock()
	info, _ := m.sessionMap[sessionId]
	m.mMap.Unlock()
	return info
}

func (m *BattleRpc) ClearBattleInfo(sessionId string) {
	m.mMap.Lock()
	delete(m.sessionMap, sessionId)
	m.mMap.Unlock()
}

func (m *BattleRpc) NotifyBattleUsers(args *BattleInfoArgs, reply *int) error {
	m.mNext.Lock()
	rid := m.nextRoomId
	m.nextRoomId = (m.nextRoomId + 1) % MaxRoom
	m.mNext.Unlock()

	info := &BattleInfo{
		Users:  args.Users,
		RoomId: rid,
	}

	glog.Infoln("Receive BattleInfo", *info)
	m.mMap.Lock()
	for _, u := range info.Users {
		m.sessionMap[u.SessionId] = info
	}
	m.mMap.Unlock()
	*reply = 1
	return nil
}

func (m *BattleRpc) PingPong(_ *string, reply *string) error {
	*reply = "Pong"
	return nil
}
