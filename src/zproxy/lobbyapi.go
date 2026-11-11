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
	"zdxsv/pkg/lobby/lobbyapi"
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

func registerProxy(req *lobbyapi.RegisterProxyRequest) (*lobbyapi.RegisterProxyResponse, error) {
	res := new(lobbyapi.RegisterProxyResponse)
	return res, callLobbyAPI(lobbyapi.RegisterProxyPath, req, res)
}

func getBattleInfo(req *lobbyapi.BattleInfoRequest) (*lobbyapi.BattleInfoResponse, error) {
	res := new(lobbyapi.BattleInfoResponse)
	return res, callLobbyAPI(lobbyapi.BattleInfoPath, req, res)
}
