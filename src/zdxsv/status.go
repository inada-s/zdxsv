package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/valyala/gorpc"

	"zdxsv/pkg/assets"
	. "zdxsv/pkg/lobby/lobbyrpc"
)

var (
	current statusParam
	jst     = time.FixedZone("Asia/Tokyo", 9*60*60)
)

const (
	timeFormat = "2006年 01月02日 15:04"
)

func init() {
	current = statusParam{
		NowDate:         time.Now().In(jst).Format(timeFormat),
		LobbyUserCount:  0,
		LobbyUsers:      []statusUser{},
		BattleUserCount: 0,
		BattleUsers:     []statusUser{},
	}
}

type statusUser struct {
	UserId string
	Name   string
	Team   string
	UDP    string
}

type statusParam struct {
	sync.RWMutex

	NowDate        string
	LobbyUserCount int
	LobbyUsers     []statusUser

	BattleUserCount int
	BattleUsers     []statusUser
}

func pollLobby() {
	lobbyRpcAddr := conf.Lobby.RPCAddr
	if strings.Contains(os.Getenv("ZDXSV_ON_DOCKER_HOST"), "lobby") {
		lobbyRpcAddr = resolveDockerHostAddr() + stripHost(conf.Lobby.RPCAddr)
	}
	c := gorpc.NewTCPClient(lobbyRpcAddr)
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
			current.Lock()
			current.NowDate = time.Now().In(jst).Format(timeFormat)
			current.LobbyUsers = current.LobbyUsers[:0]
			checked := map[string]bool{}

			for _, u := range res.LobbyUsers {
				_, ok := checked[u.UserId]
				if ok {
					continue
				}
				checked[u.UserId] = true
				user := statusUser{
					UserId: u.UserId,
					Name:   u.Name,
					Team:   u.Team,
				}
				if u.UDP {
					user.UDP = fmt.Sprintf("○")
				}
				current.LobbyUsers = append(current.LobbyUsers, user)
			}

			current.BattleUsers = current.BattleUsers[:0]
			for _, b := range res.Battles {
				for _, u := range b.Users {
					_, ok := checked[u.UserId]
					if ok {
						continue
					}
					checked[u.UserId] = true
					user := statusUser{
						UserId: u.UserId,
						Name:   u.Name,
						Team:   u.Team,
					}
					if u.UDP {
						user.UDP = fmt.Sprintf("○")
					}
					current.BattleUsers = append(current.BattleUsers, user)
				}
			}
			current.LobbyUserCount = len(current.LobbyUsers)
			current.BattleUserCount = len(current.BattleUsers)
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
	bin, err := json.Marshal(current)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf8")
	w.WriteHeader(200)
	w.Write(bin)
}

func dummyCurrent() {
	current.Lock()
	defer current.Unlock()
	current.LobbyUsers = current.LobbyUsers[:0]
	current.BattleUsers = current.BattleUsers[:0]

	for i := 0; i < 10; i++ {
		user := statusUser{
			UserId: fmt.Sprintf("%06d", i),
			Name:   fmt.Sprintf("%06dさん", i),
			Team:   fmt.Sprintf("%06dチーム", i),
		}
		if i%2 == 0 {
			user.UDP = fmt.Sprintf("○")
		}
		current.LobbyUsers = append(current.LobbyUsers, user)
		current.BattleUsers = append(current.BattleUsers, user)
	}
	go func() {
		for {
			time.Sleep(time.Second)
			current.Lock()
			current.NowDate = time.Now().Format(timeFormat)
			current.Unlock()
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
	err := http.ListenAndServe(stripHost(conf.Status.Addr), router)
	if err != nil {
		glog.Fatalln(err)
	}
}
