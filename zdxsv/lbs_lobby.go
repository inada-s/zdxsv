package main

const roomCount = 5

const LobbyCount = 26

type LbsLobby struct {
	ID         uint16
	Platform   string
	Rule       *Rule
	Start      string
	Users      map[string]*LbsUser
	Rooms      map[uint16]*LbsRoom
	EntryUsers []string
}

func NewLobby(platform string, lobbyID uint16) *LbsLobby {
	lobby := &LbsLobby{
		ID:         lobbyID,
		Platform:   platform,
		Rule:       NewRule(),
		Users:      make(map[string]*LbsUser),
		Rooms:      make(map[uint16]*LbsRoom),
		EntryUsers: make([]string, 0),
	}
	for i := uint16(0); i <= roomCount; i++ {
		lobby.Rooms[i] = NewRoom(lobbyID, i)
	}
	return lobby
}

func (l *LbsLobby) RoomCount() uint16 {
	return uint16(roomCount)
}

func (l *LbsLobby) Enter(u *LbsUser) {
	l.Users[u.UserID] = u
}

func (l *LbsLobby) Exit(userID string) {
	_, ok := l.Users[userID]
	if ok {
		delete(l.Users, userID)
		for i, id := range l.EntryUsers {
			if id == userID {
				l.EntryUsers = append(l.EntryUsers[:i], l.EntryUsers[i+1:]...)
				break
			}
		}
	}
}

func (l *LbsLobby) Entry(u *LbsUser, side byte) {
	u.Entry = side
	if side == EntryNone {
		for i, id := range l.EntryUsers {
			if id == u.UserID {
				l.EntryUsers = append(l.EntryUsers[:i], l.EntryUsers[i+1:]...)
				break
			}
		}
	} else {
		l.EntryUsers = append(l.EntryUsers, u.UserID)
	}
}

func (l *LbsLobby) GetEntryUserCount() (uint16, uint16) {
	a := uint16(0)
	b := uint16(0)
	for _, id := range l.EntryUsers {
		u, ok := l.Users[id]
		if ok {
			switch u.Entry {
			case EntryAeug:
				a++
			case EntryTitans:
				b++
			}
		}
	}
	return a, b
}

func (l *LbsLobby) CanBattleStart() bool {
	_, ok := l.startGroup()
	return ok
}

// Start values: the players per side a lobby battle needs. Empty: 2v2.
const (
	Start2v2  = "2v2"
	Start1v1  = "1v1"
	StartSolo = "solo"
)

func (l *LbsLobby) canStart(a, b int) bool {
	switch l.Start {
	case Start1v1:
		return 1 <= a && 1 <= b
	case StartSolo:
		return 1 <= a || 1 <= b
	}
	return 2 <= a && 2 <= b
}

// startGroup returns the sync fingerprint of the first entry group (in entry order)
// that can start a battle, else the first entry user's and false.
func (l *LbsLobby) startGroup() (string, bool) {
	first, seen := "", false
	counts := map[string]*[2]int{}
	for _, id := range l.EntryUsers {
		u, ok := l.Users[id]
		if !ok {
			continue
		}
		if !seen {
			first, seen = u.Sync, true
		}
		c := counts[u.Sync]
		if c == nil {
			c = &[2]int{}
			counts[u.Sync] = c
		}
		switch u.Entry {
		case EntryAeug:
			c[0]++
		case EntryTitans:
			c[1]++
		}
		if l.canStart(c[0], c[1]) {
			return u.Sync, true
		}
	}
	return first, false
}

// StartBattleUsers picks up to two users per side from the startGroup.
func (l *LbsLobby) StartBattleUsers() []*LbsUser {
	sync, _ := l.startGroup()
	a := uint16(0)
	b := uint16(0)
	ret := []*LbsUser{}
	for _, id := range l.EntryUsers {
		u, ok := l.Users[id]
		if ok && u.Sync == sync {
			switch u.Entry {
			case EntryAeug:
				if a < 2 {
					ret = append(ret, u)
				}
				a++
			case EntryTitans:
				if b < 2 {
					ret = append(ret, u)
				}
				b++
			}
		}
	}
	return ret
}

// Hack values: the special uses of a lobby, comma separated. Empty: none.
const (
	HackNone        = "none"
	HackPadDelay    = "pad_delay"
	HackWideScreen  = "widescreen"
	HackZproxyTest  = "zproxy_test"
	HackChatCommand = "chat_command"
)
