package model

import (
	"time"
)

const (
	RoomStateUnavailable = 0
	RoomStateEmpty       = 1
	RoomStatePrepare     = 2
	RoomStateRecruit     = 3
	RoomStateFull        = 4
)

type Room struct {
	Id        uint16
	LobbyId   uint16
	Name      string
	MaxPlayer uint16
	Password  string
	Owner     string
	Deadline  time.Time
	Users     []*User
	Status    byte
	Rule      *Rule
}

func NewRoom(lobbyId, roomId uint16) *Room {
	return &Room{
		Id:      roomId,
		LobbyId: lobbyId,
		Name:    "(空き)",
		Status:  RoomStateEmpty,
		Rule:    NewRule(),
		Users:   make([]*User, 0),
	}
}

func (r *Room) Enter(u *User) {
	if len(r.Users) == 0 {
		r.Owner = u.UserId
		r.Deadline = time.Now().Add(30 * time.Minute)
		r.MaxPlayer = r.Rule.playerCount
	}

	r.Users = append(r.Users, u)

	if len(r.Users) == int(r.MaxPlayer) {
		r.Status = RoomStateFull
	} else {
		r.Status = RoomStateRecruit
	}
}

func (r *Room) Exit(userId string) {
	for i, u := range r.Users {
		if u.UserId == userId {
			r.Users, r.Users[len(r.Users)-1] = append(r.Users[:i], r.Users[i+1:]...), nil
			break
		}
	}

	if len(r.Users) == int(r.MaxPlayer) {
		r.Status = RoomStateFull
	} else {
		r.Status = RoomStateRecruit
	}

	if len(r.Users) == 0 {
		r.Remove()
	}
}

func (r *Room) Remove() {
	*r = *NewRoom(r.LobbyId, r.Id)
}

func (r *Room) Entry(u *User, side byte) {
	u.Entry = side
}

func (r *Room) GetEntryUserCount() (uint16, uint16) {
	a := uint16(0)
	b := uint16(0)
	for _, u := range r.Users {
		switch u.Entry {
		case EntryAeug:
			a++
		case EntryTitans:
			b++
		}
	}
	return a, b
}

func (r *Room) CanBattleStart() bool {
	a, b := r.GetEntryUserCount()
	return 0 < a && 0 < b && a <= 2 && b <= 2
}

func (r *Room) StartBattleUsers() (active []*User, inactive []*User) {
	a := uint16(0)
	b := uint16(0)
	for _, u := range r.Users {
		switch u.Entry {
		case EntryAeug:
			if a < 2 {
				active = append(active, u)
			} else {
				inactive = append(inactive, u)
			}
			a++
		case EntryTitans:
			if b < 2 {
				active = append(active, u)
			} else {
				inactive = append(inactive, u)
			}
			b++
		default:
			inactive = append(inactive, u)
		}
	}
	return active, inactive
}
