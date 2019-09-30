package model

const roomCount = 5

type Lobby struct {
	Id         uint16
	Rule       *Rule
	Users      map[string]*User
	Rooms      map[uint16]*Room
	EntryUsers []string
}

func NewLobby(lobbyId uint16) *Lobby {
	lobby := &Lobby{
		Id:         lobbyId,
		Rule:       NewRule(),
		Users:      make(map[string]*User),
		Rooms:      make(map[uint16]*Room),
		EntryUsers: make([]string, 0),
	}
	for i := uint16(0); i <= roomCount; i++ {
		lobby.Rooms[i] = NewRoom(lobbyId, i)
	}
	return lobby
}

func (l *Lobby) RoomCount() uint16 {
	return uint16(roomCount)
}

func (l *Lobby) Enter(u *User) {
	l.Users[u.UserId] = u
}

func (l *Lobby) Exit(userId string) {
	_, ok := l.Users[userId]
	if ok {
		delete(l.Users, userId)
		for i, id := range l.EntryUsers {
			if id == userId {
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
			if id == u.UserId {
				l.EntryUsers = append(l.EntryUsers[:i], l.EntryUsers[i+1:]...)
				break
			}
		}
	} else {
		l.EntryUsers = append(l.EntryUsers, u.UserId)
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
	a, b := l.GetEntryUserCount()
	if l.Id == uint16(2) {
		return 1 <= a && 1 <= b
	}
	return 2 <= a && 2 <= b
}

func (l *Lobby) StartBattleUsers() []*User {
	a := uint16(0)
	b := uint16(0)
	ret := []*User{}
	for _, id := range l.EntryUsers {
		u, ok := l.Users[id]
		if ok {
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
