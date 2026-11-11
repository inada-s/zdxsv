package main

import (
	"net"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	requiredVersion = 1008
)

// RegisterProxy registers the zproxy at ip of a lobby user: the user with that IP for
// UserID _AUTO_, or from a LAN or the lobby host the user UserID.
func (a *Lbs) RegisterProxy(ip string, req *RegisterProxyRequest) *RegisterProxyResponse {
	logger.Info("RegisterProxyRequest", zap.String("addr", ip), zap.Any("req", req))
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

	a.Locked(func(app *Lbs) {
		var userPeer *LbsPeer
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
			user := ZproxyUser{
				UserID: u.UserID,
				Name:   u.Name,
				Team:   u.Team,
				UDP:    time.Since(u.proxyRegTime).Seconds() < 20,
			}
			user.UDPAddrs = append(user.UDPAddrs, u.proxyUDPAddrs...)
			res.LobbyUsers = append(res.LobbyUsers, user)
		}
		res.Message = "登録成功"
		logger.Info("register zproxy", zap.String("user_id", userPeer.UserID), zap.Any("session_id", userPeer.SessionID))
	})
	return res
}

// BattleInfo is the battle of the zproxy whose user has session req.SessionID.
func (a *Lbs) BattleInfo(req *BattleInfoRequest) *BattleInfoResponse {
	logger.Info("BattleInfoRequest", zap.Any("req", req))
	res := new(BattleInfoResponse)

	if req.SessionID == "" {
		res.Result = false
		res.Message = "セッションIDが無効です."
	}

	a.Locked(func(app *Lbs) {
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
			res.Users = append(res.Users, ZproxyUser{
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
func (a *Lbs) Status() *StatusResponse {
	var res *StatusResponse
	a.Locked(func(app *Lbs) {
		res = app.status()
	})
	return res
}

// status lists lobby users and battles with their platform. Call it locked.
func (app *Lbs) status() *StatusResponse {
	res := new(StatusResponse)
	for _, u := range app.users {
		user := ZproxyUser{
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
		battle := ZproxyBattle{Platform: b.Platform}
		battle.AeugIDs = append(battle.AeugIDs, b.AeugIDs...)
		battle.TitansIDs = append(battle.TitansIDs, b.TitansIDs...)
		for _, u := range b.Users {
			_, isUDP := b.UDPUsers[u.UserID]
			battle.Users = append(battle.Users, ZproxyUser{
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

// The JSON bodies zproxy exchanges with the lobby's HTTP API
// (APIHandler) and the lobby status.
const (
	RegisterProxyPath = "/lbs/zproxy/register"
	BattleInfoPath    = "/lbs/zproxy/battle_info"
)

type RegisterProxyRequest struct {
	CurrentVersion int                 `json:"current_version"`
	UserID         string              `json:"user_id"`
	LocalIP        net.IP              `json:"local_ip"`
	Port           uint16              `json:"port"`
	UDPAddrs       []string            `json:"udp_addrs"`
	P2PConnected   map[string]struct{} `json:"p2p_connected"`
}

type RegisterProxyResponse struct {
	Result     bool         `json:"result"`
	Message    string       `json:"message"`
	SessionID  string       `json:"session_id"`
	UserID     string       `json:"user_id"`
	LobbyUsers []ZproxyUser `json:"lobby_users"`
}

type BattleInfoRequest struct {
	SessionID string `json:"session_id"`
}

type BattleInfoResponse struct {
	Result   bool         `json:"result"`
	Message  string       `json:"message"`
	Users    []ZproxyUser `json:"users"`
	BattleIP net.IP       `json:"battle_ip"`
	Port     uint16       `json:"port"`
	IsTest   bool         `json:"is_test"`
}

type ZproxyUser struct {
	UserID   string   `json:"user_id"`
	Name     string   `json:"name"`
	Team     string   `json:"team"`
	UDP      bool     `json:"udp"`
	UDPAddrs []string `json:"udp_addrs"`
	Platform string   `json:"platform"`
}

type ZproxyBattle struct {
	Users     []ZproxyUser `json:"users"`
	AeugIDs   []string     `json:"aeug_ids"`
	TitansIDs []string     `json:"titans_ids"`
	Platform  string       `json:"platform"`
}

type StatusResponse struct {
	LobbyUsers []ZproxyUser   `json:"lobby_users"`
	Battles    []ZproxyBattle `json:"battles"`
}
