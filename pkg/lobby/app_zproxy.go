package lobby

import (
	"net"
	"strings"
	"time"
	. "zdxsv/pkg/lobby/lobbyapi"

	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
)

const (
	requiredVersion = 1005
)

// RegisterProxy registers the zproxy at ip of a lobby user: the user with that IP for
// UserID _AUTO_, or from a LAN or the lobby host the user UserID.
func (a *App) RegisterProxy(ip string, req *RegisterProxyRequest) *RegisterProxyResponse {
	zlog.Info("RegisterProxyRequest", zap.String("addr", ip), zap.Any("req", req))
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

	remote := net.ParseIP(ip).To4()
	if remote == nil {
		res.Result = false
		res.Message = "無効なグローバルIPアドレスです"
		return res
	}
	isLANTest := remote.IsLoopback() || strings.HasPrefix(remote.String(), "192.168.")

	if req.LocalIP == nil || req.LocalIP.To4() == nil {
		res.Result = false
		res.Message = "無効なローカルIPアドレスです"
		return res
	}

	a.Locked(func(app *App) {
		var userPeer *AppPeer
		for userID, peer := range app.users {
			arr := strings.Split(peer.conn.Address(), ":")
			if 0 < len(arr) {
				if arr[0] == remote.String() && req.UserID == "_AUTO_" {
					userPeer = peer
					break
				} else if isLANTest && userID == req.UserID {
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
		res.UserID = userPeer.UserID
		res.SessionID = userPeer.SessionID
		for _, u := range app.users {
			user := User{
				UserID: u.UserID,
				Name:   u.Name,
				Team:   u.Team,
				UDP:    time.Since(u.proxyRegTime).Seconds() < 20,
			}
			user.UDPAddrs = append(user.UDPAddrs, u.proxyUDPAddrs...)
			res.LobbyUsers = append(res.LobbyUsers, user)
		}
		res.Message = "登録成功"
		zlog.Info("register zproxy", zap.String("user_id", userPeer.UserID), zap.Any("session_id", userPeer.SessionID))
	})
	return res
}

// BattleInfo is the battle of the zproxy whose user has session req.SessionID.
func (a *App) BattleInfo(req *BattleInfoRequest) *BattleInfoResponse {
	zlog.Info("BattleInfoRequest", zap.Any("req", req))
	res := new(BattleInfoResponse)

	if req.SessionID == "" {
		res.Result = false
		res.Message = "セッションIDが無効です."
	}

	a.Locked(func(app *App) {
		battle, ok := app.battles[req.SessionID]
		if !ok {
			res.Result = false
			res.Message = "対戦情報が見つかりません."
			return
		}

		userID := ""
		for _, u := range battle.Users {
			if u.SessionID == req.SessionID {
				userID = u.UserID
			}
		}

		if userID == "" {
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
				UserID: u.UserID,
				Name:   u.Name,
				Team:   u.Team,
			})
		}
		res.Message = "対戦情報取得成功"
	})

	return res
}

// Status lists the lobby users and battles, from the app's goroutine.
func (a *App) Status() *StatusResponse {
	var res *StatusResponse
	a.Locked(func(app *App) {
		res = app.status()
	})
	return res
}

// status lists lobby users and battles with their platform. Call it locked.
func (app *App) status() *StatusResponse {
	res := new(StatusResponse)
	for _, u := range app.users {
		user := User{
			UserID:   u.UserID,
			Name:     u.Name,
			Team:     u.Team,
			UDP:      time.Since(u.proxyRegTime).Seconds() < 20,
			Platform: u.Platform,
		}
		res.LobbyUsers = append(res.LobbyUsers, user)
	}

	checked := map[string]bool{}
	for sid, b := range app.battles {
		if _, ok := checked[sid]; ok {
			continue
		}
		battle := Battle{Platform: b.Platform}
		battle.AeugIDs = append(battle.AeugIDs, b.AeugIDs...)
		battle.TitansIDs = append(battle.TitansIDs, b.TitansIDs...)
		for _, u := range b.Users {
			_, isUDP := b.UDPUsers[u.UserID]
			battle.Users = append(battle.Users, User{
				UserID:   u.UserID,
				Name:     u.Name,
				Team:     u.Team,
				UDP:      isUDP,
				Platform: b.Platform,
			})
			checked[u.SessionID] = true
		}
		res.Battles = append(res.Battles, battle)
	}
	return res
}
