package battle

import (
	"fmt"
	"net/http"
	"net/rpc"
	"strconv"
	"sync"

	"github.com/golang/glog"

	"zdxsv/pkg/battle/battlerpc"
	"zdxsv/pkg/proto"
)

type BasePeer struct {
	sessionId string
	userId    string
	position  int
	roomId    int
}

func (p *BasePeer) SetUserId(userId string) {
	p.userId = userId
}

func (p *BasePeer) SetSessionId(sessionId string) {
	p.sessionId = sessionId
}

func (p *BasePeer) SessionId() string {
	return p.sessionId
}

func (p *BasePeer) UserId() string {
	return p.userId
}

func (p *BasePeer) SetPosition(pos int) {
	p.position = pos
}

func (p *BasePeer) Position() int {
	return p.position
}

func (p *BasePeer) SetRoomId(id int) {
	p.roomId = id
}

func (p *BasePeer) RoomId() int {
	return p.roomId
}

type Peer interface {
	SetUserId(string)
	SetSessionId(string)
	UserId() string
	SessionId() string
	SetPosition(int)
	Position() int
	SetRoomId(int)
	RoomId() int
	AddSendData([]byte)
	AddSendMessage(*proto.BattleMessage)
	Address() string
	Close() error
}

type Logic struct {
	*battlerpc.BattleRpc

	roomsMtx sync.Mutex
	rooms    []*Room
}

func NewLogic() *Logic {
	l := &Logic{}
	l.BattleRpc = battlerpc.NewBattleRpc()
	l.rooms = make([]*Room, battlerpc.MaxRoom)
	return l
}

func (m *Logic) ServeRpc(addr string) {
	rpc.RegisterName("Logic", m.BattleRpc)
	rpc.HandleHTTP()
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		glog.Errorln(err.Error())
	}
}

func (m *Logic) FindWaitingUser(sessionId string) (*battlerpc.User, bool) {
	info := m.GetBattleInfo(sessionId)
	if info == nil {
		glog.Errorln("BattleInfo not found. sessionId=", sessionId)
		return nil, false
	}

	for _, u := range info.Users {
		if sessionId == u.SessionId {
			return &u, true
		}
	}
	glog.Errorln("User not found in BattleInfo. sessionId=", sessionId)
	return nil, false
}

func (m *Logic) Join(p Peer, sessionId string) *Room {
	user, ok := m.FindWaitingUser(sessionId)
	if !ok {
		return nil
	}

	p.SetUserId(user.UserId)
	p.SetSessionId(sessionId)
	info := m.GetBattleInfo(sessionId)
	m.ClearBattleInfo(sessionId)

	m.roomsMtx.Lock()
	room := m.rooms[info.RoomId]
	if room == nil {
		room = newRoom(info.RoomId)
		m.rooms[info.RoomId] = room
	}
	m.roomsMtx.Unlock()

	room.Join(p)
	return room
}

func ParseSessionId(value string) (string, error) {
	if len(value) != 10 {
		return "", fmt.Errorf("Invalid value length")
	}
	var1, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return "", err
	}
	var2 := fmt.Sprintf("%010d", var1-100001)
	return var2[1:5] + var2[6:], nil
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
