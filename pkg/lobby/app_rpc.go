package lobby

import (
	"fmt"
	"strings"
	"time"
	. "zdxsv/pkg/lobby/lobbyrpc"

	"github.com/golang/glog"
	"github.com/valyala/gorpc"
)

const (
	requiredVersion = 1005
)

type lobbyRPC struct {
	app *App
}

func (m *lobbyRPC) newHandler() gorpc.HandlerFunc {
	return func(remoteAddr string, req interface{}) interface{} {
		switch req := req.(type) {
		case *RegisterProxyRequest:
			return m.registerProxy(remoteAddr, req)
		case *BattleInfoRequest:
			return m.getBattleInfo(remoteAddr, req)
		case *StatusRequest:
			return m.getStatus(remoteAddr, req)
		default:
			return fmt.Errorf("Error")
		}
	}
}

func newLobbyRPCServer(app *App, addr string) *gorpc.Server {
	m := lobbyRPC{app: app}
	return gorpc.NewTCPServer(addr, m.newHandler())
}

func (m *lobbyRPC) registerProxy(remoteAddr string, req *RegisterProxyRequest) *RegisterProxyResponse {
	glog.Infof("RegisterProxyRequest %v %+v\n", remoteAddr, req)
	res := new(RegisterProxyResponse)

	if req.CurrentVersion < requiredVersion {
		res.Result = false
		res.Message = "プロキシソフトのバージョンが古いです"
		return res
	}

	if len(req.UDPAddrs) == 0 {
		res.Result = false
		res.Message = "UDPアドレスが取得できていません"
		return res
	}

	arrRemote := strings.Split(remoteAddr, ":")
	if len(arrRemote) != 2 {
		res.Result = false
		res.Message = "無効なグローバルIPアドレスです"
		return res
	}

	isLANTest := false
	if strings.HasPrefix(arrRemote[0], "192.168") {
		isLANTest = true
	}
	if strings.HasPrefix(arrRemote[0], "127.0.0.1") {
		isLANTest = true
	}

	if req.LocalIP == nil || req.LocalIP.To4() == nil {
		res.Result = false
		res.Message = "無効なローカルIPアドレスです"
		return res
	}

	m.app.Locked(func(app *App) {
		var userPeer *AppPeer
		for userId, peer := range app.users {
			arr := strings.Split(peer.conn.Address(), ":")
			if 0 < len(arr) {
				if arr[0] == arrRemote[0] && req.UserId == "_AUTO_" {
					userPeer = peer
					break
				} else if isLANTest && userId == req.UserId {
					userPeer = peer
					break
				}
			}
		}

		if userPeer == nil {
			res.Result = false
			res.Message = "ロビーにユーザが見つかりません"
			return
		}

		userPeer.proxyIP = req.LocalIP
		userPeer.proxyPort = req.Port
		userPeer.proxyRegTime = time.Now()
		userPeer.proxyUDPAddrs = req.UDPAddrs
		userPeer.proxyP2PConnected = req.P2PConnected
		res.Result = true
		res.UserId = userPeer.UserId
		res.SessionId = userPeer.SessionId
		for _, u := range app.users {
			user := User{
				UserId: u.UserId,
				Name:   u.Name,
				Team:   u.Team,
				UDP:    time.Since(u.proxyRegTime).Seconds() < 20,
			}
			user.UDPAddrs = append(user.UDPAddrs, u.proxyUDPAddrs...)
			res.LobbyUsers = append(res.LobbyUsers, user)
		}
		res.Message = "登録成功"
		glog.Infoln("Register zproxy", userPeer.UserId, userPeer.SessionId)
	})
	return res
}

func (m *lobbyRPC) getBattleInfo(remoteAddr string, req *BattleInfoRequest) *BattleInfoResponse {
	glog.Infof("BattleInfoRequest %v %+v\n", remoteAddr, req)
	res := new(BattleInfoResponse)

	if req.SessionId == "" {
		res.Result = false
		res.Message = "セッションIDが無効です."
	}

	m.app.Locked(func(app *App) {
		battle, ok := app.battles[req.SessionId]
		if !ok {
			res.Result = false
			res.Message = "対戦情報が見つかりません."
			return
		}

		userId := ""
		for _, u := range battle.Users {
			if u.SessionId == req.SessionId {
				userId = u.UserId
			}
		}

		if userId == "" {
			res.Result = false
			res.Message = "対戦情報にユーザが見つかりません."
			return
		}

		res.Result = true
		res.BattleIP = battle.ServerIP
		res.Port = battle.ServerPort
		res.IsTest = battle.TestBattle
		for _, u := range battle.Users {
			res.Users = append(res.Users, User{
				UserId: u.UserId,
				Name:   u.Name,
				Team:   u.Team,
			})
		}
		res.Message = "対戦情報取得成功"
	})

	return res
}

func (m *lobbyRPC) getStatus(remoteAddr string, _ *StatusRequest) *StatusResponse {
	res := new(StatusResponse)

	m.app.Locked(func(app *App) {
		for _, u := range app.users {
			user := User{
				UserId: u.UserId,
				Name:   u.Name,
				Team:   u.Team,
				UDP:    time.Since(u.proxyRegTime).Seconds() < 20,
			}
			res.LobbyUsers = append(res.LobbyUsers, user)
		}

		checked := map[string]bool{}
		for sid, b := range app.battles {
			if _, ok := checked[sid]; ok {
				continue
			}
			battle := Battle{}
			battle.AeugIds = append(battle.AeugIds, b.AeugIds...)
			battle.TitansIds = append(battle.TitansIds, b.TitansIds...)
			for _, u := range b.Users {
				_, isUDP := b.UDPUsers[u.UserId]
				battle.Users = append(battle.Users, User{
					UserId: u.UserId,
					Name:   u.Name,
					Team:   u.Team,
					UDP:    isUDP,
				})
				checked[u.SessionId] = true
			}
			res.Battles = append(res.Battles, battle)
		}
	})

	return res
}
