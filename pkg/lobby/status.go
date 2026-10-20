package lobby

import (
	"time"

	"zdxsv/pkg/lobby/lobbyrpc"
	"zdxsv/pkg/lobby/model"
)

var jst = time.FixedZone("Asia/Tokyo", 9*60*60)

const statusTimeFormat = "2006年 01月02日 15:04"

// StatusUser is a player on the status page.
type StatusUser struct {
	UserID   string
	Name     string
	Team     string
	UDP      string
	Platform string
}

// PlatformCount counts the users of one platform.
type PlatformCount struct {
	LobbyUserCount  int
	BattleUserCount int
}

// Status is the /lbs/status body: who is in the lobby and who is in a battle.
type Status struct {
	NowDate        string
	LobbyUserCount int
	LobbyUsers     []StatusUser

	BattleUserCount int
	BattleUsers     []StatusUser

	// Platforms counts users per platform; console and emulator players never meet.
	Platforms map[string]*PlatformCount
}

func newStatusUser(u lobbyrpc.User) StatusUser {
	user := StatusUser{
		UserID:   u.UserID,
		Name:     u.Name,
		Team:     u.Team,
		Platform: u.Platform,
	}
	if user.Platform == "" {
		user.Platform = model.PlatformConsole
	}
	if u.UDP {
		user.UDP = "○"
	}
	return user
}

// MakeStatus turns a lobby status into the /lbs/status body; a user counts once.
func MakeStatus(res *lobbyrpc.StatusResponse, now time.Time) Status {
	s := Status{
		NowDate:     now.In(jst).Format(statusTimeFormat),
		LobbyUsers:  []StatusUser{},
		BattleUsers: []StatusUser{},
		Platforms: map[string]*PlatformCount{
			model.PlatformConsole:  {},
			model.PlatformEmuX8664: {},
		},
	}
	count := func(platform string) *PlatformCount {
		c, ok := s.Platforms[platform]
		if !ok {
			c = &PlatformCount{}
			s.Platforms[platform] = c
		}
		return c
	}
	checked := map[string]bool{}
	for _, u := range res.LobbyUsers {
		if checked[u.UserID] {
			continue
		}
		checked[u.UserID] = true
		user := newStatusUser(u)
		s.LobbyUsers = append(s.LobbyUsers, user)
		count(user.Platform).LobbyUserCount++
	}
	for _, b := range res.Battles {
		for _, u := range b.Users {
			if checked[u.UserID] {
				continue
			}
			checked[u.UserID] = true
			user := newStatusUser(u)
			s.BattleUsers = append(s.BattleUsers, user)
			count(user.Platform).BattleUserCount++
		}
	}
	s.LobbyUserCount = len(s.LobbyUsers)
	s.BattleUserCount = len(s.BattleUsers)
	return s
}
