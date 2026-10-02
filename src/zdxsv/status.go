package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/valyala/gorpc"

	"zdxsv/pkg/assets"
	"zdxsv/pkg/config"
	. "zdxsv/pkg/lobby/lobbyrpc"
	"zdxsv/pkg/lobby/model"
)

var (
	current statusParam
	jst     = time.FixedZone("Asia/Tokyo", 9*60*60)
)

const (
	timeFormat = "2006年 01月02日 15:04"
)

func init() {
	current.statusData = makeStatus(&StatusResponse{}, time.Now())
}

type statusUser struct {
	UserID   string
	Name     string
	Team     string
	UDP      string
	Platform string
}

type platformCount struct {
	LobbyUserCount  int
	BattleUserCount int
}

type statusData struct {
	NowDate        string
	LobbyUserCount int
	LobbyUsers     []statusUser

	BattleUserCount int
	BattleUsers     []statusUser

	// Platforms counts users per platform; console and emulator players never meet.
	Platforms map[string]*platformCount
}

type statusParam struct {
	sync.RWMutex
	statusData
}

func newStatusUser(u User) statusUser {
	user := statusUser{
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

// makeStatus turns a lobby status into the /api/stat body; a user counts once.
func makeStatus(res *StatusResponse, now time.Time) statusData {
	s := statusData{
		NowDate:     now.In(jst).Format(timeFormat),
		LobbyUsers:  []statusUser{},
		BattleUsers: []statusUser{},
		Platforms: map[string]*platformCount{
			model.PlatformConsole:  {},
			model.PlatformEmuX8664: {},
		},
	}
	count := func(platform string) *platformCount {
		c, ok := s.Platforms[platform]
		if !ok {
			c = &platformCount{}
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

func pollLobby() {
	c := gorpc.NewTCPClient(config.Conf.Lobby.RPCAddr)
	c.Start()
	defer c.Stop()
	for {
		time.Sleep(10 * time.Second)
		rawResp, err := c.CallTimeout(&StatusRequest{}, time.Second)
		if err != nil {
			glog.Errorln(err)
			continue
		}

		if res, ok := rawResp.(*StatusResponse); ok {
			s := makeStatus(res, time.Now())
			current.Lock()
			current.statusData = s
			current.Unlock()
		}
	}
}

func redirectToIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusFound)
}

func getApiStat(w http.ResponseWriter, r *http.Request) {
	current.RLock()
	defer current.RUnlock()
	bin, err := json.Marshal(current.statusData)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf8")
	w.WriteHeader(200)
	w.Write(bin)
}

func dummyCurrent() {
	res := &StatusResponse{}
	for i := 0; i < 10; i++ {
		user := User{
			UserID:   fmt.Sprintf("%06d", i),
			Name:     fmt.Sprintf("%06dさん", i),
			Team:     fmt.Sprintf("%06dチーム", i),
			UDP:      i%2 == 0,
			Platform: model.PlatformConsole,
		}
		if i%3 == 0 {
			user.Platform = model.PlatformEmuX8664
		}
		res.LobbyUsers = append(res.LobbyUsers, user)
	}
	go func() {
		for {
			s := makeStatus(res, time.Now())
			current.Lock()
			current.statusData = s
			current.Unlock()
			time.Sleep(time.Second)
		}
	}()
}

func mainStatus() {
	go pollLobby()

	if _, err := assets.Asset("assets/checkfile"); err != nil {
		glog.Fatalln(err)
	}
	router := http.NewServeMux()
	router.HandleFunc("/api/stat", getApiStat)
	err := http.ListenAndServe(stripHost(config.Conf.Status.Addr), router)
	if err != nil {
		glog.Fatalln(err)
	}
}
