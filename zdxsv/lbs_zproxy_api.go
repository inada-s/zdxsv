// Package lobbyapi holds the JSON bodies zproxy exchanges with the lobby's HTTP API
// (lobby.APIHandler) and the lobby status.
package main

import "net"

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
