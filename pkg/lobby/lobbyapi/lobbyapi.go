// Package lobbyapi holds the JSON bodies zproxy exchanges with the lobby's HTTP API
// (lobby.APIHandler) and the lobby status.
package lobbyapi

import (
	"net"
)

const (
	RegisterProxyPath = "/lbs/zproxy/register"
	BattleInfoPath    = "/lbs/zproxy/battle_info"
)

type RegisterProxyRequest struct {
	CurrentVersion int
	UserID         string
	LocalIP        net.IP
	Port           uint16
	UDPAddrs       []string
	P2PConnected   map[string]struct{}
}

type RegisterProxyResponse struct {
	Result     bool
	Message    string
	SessionID  string
	UserID     string
	LobbyUsers []User
}

type BattleInfoRequest struct {
	SessionID string
}

type BattleInfoResponse struct {
	Result   bool
	Message  string
	Users    []User
	BattleIP net.IP
	Port     uint16
	IsTest   bool
}

type User struct {
	UserID   string
	Name     string
	Team     string
	UDP      bool
	UDPAddrs []string
	Platform string
}

type Battle struct {
	Users     []User
	AeugIDs   []string
	TitansIDs []string
	Platform  string
}

type StatusResponse struct {
	LobbyUsers []User
	Battles    []Battle
}
