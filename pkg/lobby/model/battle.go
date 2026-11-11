package model

import (
	"crypto/rand"
	"encoding/binary"
	"net"
	"time"
)

type LbsBattle struct {
	BattleCode string
	ServerIP   net.IP
	ServerPort uint16
	Users      []LbsUser
	AeugIDs    []string
	TitansIDs  []string
	UDPUsers   map[string]bool
	P2PMap     map[string]map[string]struct{}
	Rule       *Rule
	LobbyID    uint16
	Platform   string
	StartTime  time.Time
	TestBattle bool
	// GgpoSession: the GGPO and live uplink session id (battle info ggpo_session), random per
	// battle so that only its players know it (the battle code is public).
	GgpoSession uint32
	// PlatformInfo: each player's platform info when the battle was made, by user id.
	// The battle info reads it: a player leaves the lobby when it gets the battle
	// server address, before the others ask for theirs.
	PlatformInfo map[string]map[string]string
}

func NewBattle(platform string, lobbyID uint16) *LbsBattle {
	return &LbsBattle{
		Users:        make([]LbsUser, 0),
		AeugIDs:      make([]string, 0),
		TitansIDs:    make([]string, 0),
		UDPUsers:     map[string]bool{},
		P2PMap:       map[string]map[string]struct{}{},
		Rule:         NewRule(),
		LobbyID:      lobbyID,
		Platform:     platform,
		GgpoSession:  newGgpoSession(),
		PlatformInfo: map[string]map[string]string{},
	}
}

func (b *LbsBattle) SetRule(rule *Rule) {
	b.Rule = rule
}

func (b *LbsBattle) Add(s *LbsUser) {
	cp := *s
	cp.Battle = nil
	cp.Lobby = nil
	cp.Room = nil
	b.Users = append(b.Users, cp)
	if s.Entry == EntryAeug {
		b.AeugIDs = append(b.AeugIDs, cp.UserID)
	} else if s.Entry == EntryTitans {
		b.TitansIDs = append(b.TitansIDs, cp.UserID)
	}
}

func (b *LbsBattle) NumOfEntryUsers() uint16 {
	return uint16(len(b.AeugIDs) + len(b.TitansIDs))
}

func (b *LbsBattle) SetBattleServer(ip net.IP, port uint16) {
	b.ServerIP = ip
	b.ServerPort = port
}

func (b *LbsBattle) GetPosition(userID string) byte {
	for i, u := range b.Users {
		if userID == u.UserID {
			return byte(i + 1)
		}
	}
	return 0
}

func (b *LbsBattle) GetUserByPos(pos byte) *LbsUser {
	if pos == 0 || len(b.Users) < int(pos) {
		return nil
	}
	return &b.Users[pos-1]
}

type BattleResult struct {
	Unk01       uint16 `json:"unk_01"`
	BattleCode  string `json:"battle_code"`
	Unk03       byte   `json:"unk_03"`
	Unk04       byte   `json:"unk_04"`
	Unk05       byte   `json:"unk_05"`
	Unk06       byte   `json:"unk_06"`
	BattleCount byte   `json:"battle_count"`
	WinCount    byte   `json:"win_count"`
	LoseCount   byte   `json:"lose_count"`
	KillCount   uint32 `json:"kill_count"`
	DeathCount  uint32 `json:"death_count"`
	TotalFrame  uint32 `json:"total_frame"`
	Unk13       uint32 `json:"unk_13"`
	Unk14       uint32 `json:"unk_14"`
	Side        byte   `json:"side"`
	Unk16       byte   `json:"unk_16"`
	Unk17       byte   `json:"unk_17"`
	Unk18       byte   `json:"unk_18"`
	Unk19       byte   `json:"unk_19"`
	Unk20       uint16 `json:"unk_20"`
	Unk21       uint16 `json:"unk_21"`
	Unk22       uint16 `json:"unk_22"`
	Unk23       uint16 `json:"unk_23"`
	Unk24       uint16 `json:"unk_24"`
	Unk25       uint16 `json:"unk_25"`
	Unk26       uint16 `json:"unk_26"`
	Unk27       uint16 `json:"unk_27"`
}

// newGgpoSession returns a random nonzero session id.
func newGgpoSession() uint32 {
	var b [4]byte
	for {
		rand.Read(b[:])
		if s := binary.LittleEndian.Uint32(b[:]); s != 0 {
			return s
		}
	}
}
