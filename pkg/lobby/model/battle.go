package model

import (
	"net"
	"time"
)

type Battle struct {
	ServerIP   net.IP
	ServerPort uint16
	Users      []User
	AeugIds    []string
	TitansIds  []string
	UDPUsers   map[string]bool
	P2PMap     map[string]map[string]struct{}
	Rule       *Rule
	LobbyId    uint16
	StartTime  time.Time
	TestBattle bool
}

func NewBattle(lobbyId uint16) *Battle {
	return &Battle{
		Users:     make([]User, 0),
		AeugIds:   make([]string, 0),
		TitansIds: make([]string, 0),
		UDPUsers:  map[string]bool{},
		P2PMap:    map[string]map[string]struct{}{},
		Rule:      NewRule(),
		LobbyId:   lobbyId,
	}
}

func (b *Battle) SetRule(rule *Rule) {
	b.Rule = rule
}

func (b *Battle) Add(s *User) {
	cp := *s
	cp.Battle = nil
	cp.Lobby = nil
	cp.Room = nil
	b.Users = append(b.Users, cp)
	if s.Entry == EntryAeug {
		b.AeugIds = append(b.AeugIds, cp.UserId)
	} else if s.Entry == EntryTitans {
		b.TitansIds = append(b.TitansIds, cp.UserId)
	}
}

func (b *Battle) SetBattleServer(ip net.IP, port uint16) {
	b.ServerIP = ip
	b.ServerPort = port
}

func (b *Battle) GetPosition(userId string) byte {
	for i, u := range b.Users {
		if userId == u.UserId {
			return byte(i + 1)
		}
	}
	return 0
}

func (b *Battle) GetUserByPos(pos byte) *User {
	pos -= 1
	if pos < 0 || len(b.Users) < int(pos) {
		return nil
	}
	return &b.Users[pos]
}
