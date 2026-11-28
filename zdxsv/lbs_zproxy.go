package main

import (
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	requiredVersion = 1008

	proxyRegistersPerIP   = 30
	proxyBattleInfosPerIP = 30
	maxProxyP2PConnected  = 256
)

// RegisterProxy registers the zproxy at ip for the lobby user connected from the same host:
// the one with UserID, or for UserID _AUTO_ the only one. The lobby host's own addresses
// count as one host. The answer carries the user's session ID, the battle server's only
// credential, so the lobby connection's host is the proof that the caller is the user.
func (a *Lbs) RegisterProxy(ip string, req *RegisterProxyRequest) *RegisterProxyResponse {
	floodLog.Info("RegisterProxyRequest", zap.String("addr", ip), zap.String("user_id", req.UserID))
	res := new(RegisterProxyResponse)

	if !a.proxyLimit.allow(net.ParseIP(ip), time.Now()) {
		res.Result = false
		res.Message = "登録要求が多すぎます. しばらく待ってください"
		return res
	}

	if req.CurrentVersion < requiredVersion {
		res.Result = false
		res.Message = "プロキシソフトのバージョンが古いです"
		return res
	}

	remote := net.ParseIP(ip).To4()
	if remote == nil {
		res.Result = false
		res.Message = "無効なグローバルIPアドレスです"
		return res
	}

	localIP := req.LocalIP.To4()
	if localIP == nil || !lanIP(localIP) {
		res.Result = false
		res.Message = "無効なローカルIPアドレスです"
		return res
	}

	udpAddrs := proxyUDPAddrs(req.UDPAddrs)
	if len(udpAddrs) == 0 {
		res.Result = false
		res.Message = "UDPアドレスが取得できていません"
		return res
	}

	if len(req.P2PConnected) > maxProxyP2PConnected {
		res.Result = false
		res.Message = "P2P接続情報が多すぎます"
		return res
	}

	a.Locked(func(app *Lbs) {
		var found []*LbsPeer
		elsewhere := false
		for _, peer := range app.users {
			if req.UserID != "_AUTO_" && peer.UserID != req.UserID {
				continue
			}
			if !sameHost(net.ParseIP(peer.IP()), remote) {
				elsewhere = elsewhere || peer.UserID == req.UserID
				continue
			}
			found = append(found, peer)
		}
		if elsewhere {
			res.Result = false
			res.Message = "ユーザは別のIPアドレスからロビーに接続しています. PS2と同じネットワークでプロキシを起動してください. PCでVPNを使っている場合はVPNを切ってください"
			return
		}
		if len(found) == 0 {
			res.Result = false
			res.Message = "ロビーにユーザが見つかりません"
			return
		}
		if len(found) > 1 {
			res.Result = false
			res.Message = "同じIPアドレスのユーザが複数います. -userid でユーザIDを指定してください"
			return
		}
		userPeer := found[0]

		userPeer.proxyIP = localIP
		userPeer.proxyPort = req.Port
		userPeer.proxyRegTime = time.Now()
		userPeer.proxyUDPAddrs = udpAddrs
		userPeer.proxyP2PConnected = app.p2pUsers(userPeer, req.P2PConnected)
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
			uHost := net.ParseIP(u.IP())
			for _, addr := range u.proxyUDPAddrs {
				if lanIP(udpAddrIP(addr)) && !sameHost(uHost, remote) {
					continue
				}
				user.UDPAddrs = append(user.UDPAddrs, addr)
			}
			res.LobbyUsers = append(res.LobbyUsers, user)
		}
		res.Message = "登録成功"
		floodLog.Info("register zproxy", zap.String("user_id", userPeer.UserID))
	})
	return res
}

const maxProxyUDPAddrs = 4

// proxyUDPAddrs keeps the IPv4 host:port addresses. The public one is from STUN and need
// not be the IP the registration came from.
func proxyUDPAddrs(addrs []string) []string {
	var ok []string
	for _, addr := range addrs {
		if udpAddrIP(addr) == nil || slices.Contains(ok, addr) {
			continue
		}
		ok = append(ok, addr)
		if len(ok) == maxProxyUDPAddrs {
			break
		}
	}
	return ok
}

// udpAddrIP is the IPv4 of a host:port with a non-zero port, nil otherwise.
func udpAddrIP(addr string) net.IP {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	if n, err := strconv.Atoi(port); err != nil || n <= 0 || 65535 < n {
		return nil
	}
	return net.ParseIP(host).To4()
}

// p2pUsers is the users among connected (zproxy's ping answers, keyed by the answering
// address or user ID) other than self.
func (app *Lbs) p2pUsers(self *LbsPeer, connected map[string]struct{}) map[string]struct{} {
	users := map[string]struct{}{}
	for key := range connected {
		for _, u := range app.users {
			if u != self && (u.UserID == key || slices.Contains(u.proxyUDPAddrs, key)) {
				users[u.UserID] = struct{}{}
			}
		}
	}
	return users
}

func lanIP(ip net.IP) bool {
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast())
}

var hostIPs = sync.OnceValue(func() []net.IP {
	var ips []net.IP
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		logger.Warn("interface addresses", zap.Error(err))
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			ips = append(ips, n.IP)
		}
	}
	return ips
})

func localHost(ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() || slices.ContainsFunc(hostIPs(), ip.Equal))
}

func sameHost(a, b net.IP) bool {
	return a != nil && (a.Equal(b) || (localHost(a) && localHost(b)))
}

// BattleInfo is the battle of the zproxy whose user has session req.SessionID.
func (a *Lbs) BattleInfo(ip string, req *BattleInfoRequest) *BattleInfoResponse {
	floodLog.Info("BattleInfoRequest", zap.String("addr", ip))
	res := new(BattleInfoResponse)

	if !a.battleInfoLimit.allow(net.ParseIP(ip), time.Now()) {
		res.Result = false
		res.Message = "要求が多すぎます. しばらく待ってください"
		return res
	}

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
		if _, ok := checked[sid]; ok || b.Ended {
			continue
		}
		battle := ZproxyBattle{Platform: b.Platform}
		battle.AeugIDs = append(battle.AeugIDs, b.AeugIDs...)
		battle.TitansIDs = append(battle.TitansIDs, b.TitansIDs...)
		for _, u := range b.Users {
			checked[u.SessionID] = true
			if b.Left[u.SessionID] {
				continue
			}
			_, isUDP := b.UDPUsers[u.UserID]
			battle.Users = append(battle.Users, ZproxyUser{
				UserID:   u.UserID,
				Name:     u.Name,
				Team:     u.Team,
				UDP:      isUDP,
				Platform: b.Platform,
			})
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
