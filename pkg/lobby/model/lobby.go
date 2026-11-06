package model

const roomCount = 5

type Lobby struct {
	ID         uint16
	Platform   string
	Rule       *Rule
	Users      map[string]*User
	Rooms      map[uint16]*Room
	EntryUsers []string
}

func NewLobby(platform string, lobbyID uint16) *Lobby {
	lobby := &Lobby{
		ID:         lobbyID,
		Platform:   platform,
		Rule:       NewRule(),
		Users:      make(map[string]*User),
		Rooms:      make(map[uint16]*Room),
		EntryUsers: make([]string, 0),
	}
	for i := uint16(0); i <= roomCount; i++ {
		lobby.Rooms[i] = NewRoom(lobbyID, i)
	}
	return lobby
}

func (l *Lobby) RoomCount() uint16 {
	return uint16(roomCount)
}

func (l *Lobby) Enter(u *User) {
	l.Users[u.UserID] = u
}

func (l *Lobby) Exit(userID string) {
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

func (l *Lobby) Entry(u *User, side byte) {
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

func (l *Lobby) GetEntryUserCount() (uint16, uint16) {
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

func (l *Lobby) CanBattleStart() bool {
	_, ok := l.startGroup()
	return ok
}

func (l *Lobby) canStart(a, b int) bool {
	if l.ID == uint16(2) {
		return 1 <= a && 1 <= b
	}
	if l.ID == uint16(3) {
		return 1 <= a || 1 <= b
	}
	return 2 <= a && 2 <= b
}

// startGroup returns the sync fingerprint of the first entry group (in entry order)
// that can start a battle, else the first entry user's and false.
func (l *Lobby) startGroup() (string, bool) {
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
func (l *Lobby) StartBattleUsers() []*User {
	sync, _ := l.startGroup()
	a := uint16(0)
	b := uint16(0)
	ret := []*User{}
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
