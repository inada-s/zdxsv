package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

const lobbyAPITimeout = time.Second

// The lobby finds this proxy's user by the IPv4 address of its lobby connection.
// Registering every few seconds keeps the connection open, so the battle info request at
// battle start needs no new TLS handshake.
var lobbyAPIClient = &http.Client{
	Timeout: lobbyAPITimeout,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: lobbyAPITimeout}).DialContext(ctx, "tcp4", addr)
		},
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: lobbyAPITimeout,
	},
}

func callLobbyAPI(path string, req, res any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	resp, err := lobbyAPIClient.Post(strings.TrimSuffix(conf.LobbyAPIURL, "/")+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lobby API %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(res)
}

func registerProxy(req *RegisterProxyRequest) (*RegisterProxyResponse, error) {
	res := new(RegisterProxyResponse)
	return res, callLobbyAPI(RegisterProxyPath, req, res)
}

func getBattleInfo(req *BattleInfoRequest) (*BattleInfoResponse, error) {
	res := new(BattleInfoResponse)
	return res, callLobbyAPI(BattleInfoPath, req, res)
}

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
	Result     bool        `json:"result"`
	Message    string      `json:"message"`
	SessionID  string      `json:"session_id"`
	UserID     string      `json:"user_id"`
	LobbyUsers []LobbyUser `json:"lobby_users"`
}

type BattleInfoRequest struct {
	SessionID string `json:"session_id"`
}

type BattleInfoResponse struct {
	Result   bool        `json:"result"`
	Message  string      `json:"message"`
	Users    []LobbyUser `json:"users"`
	BattleIP net.IP      `json:"battle_ip"`
	Port     uint16      `json:"port"`
	IsTest   bool        `json:"is_test"`
}

type LobbyUser struct {
	UserID   string   `json:"user_id"`
	Name     string   `json:"name"`
	Team     string   `json:"team"`
	UDP      bool     `json:"udp"`
	UDPAddrs []string `json:"udp_addrs"`
	Platform string   `json:"platform"`
}
