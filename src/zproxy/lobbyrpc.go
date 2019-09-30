package main

import (
	"fmt"
	"time"
	"zdxsv/pkg/lobby/lobbyrpc"

	"github.com/valyala/gorpc"
)

var (
	lobbyRPCClient *gorpc.Client
)

func setupLobbyRPC() {
	lobbyRPCClient = gorpc.NewTCPClient(conf.LobbyRPCAddr)
	lobbyRPCClient.Start()
}

func registerProxy(req *lobbyrpc.RegisterProxyRequest) (*lobbyrpc.RegisterProxyResponse, error) {
	rawResp, err := lobbyRPCClient.CallTimeout(req, time.Second)
	if err != nil {
		return nil, err
	}
	if resp, ok := rawResp.(*lobbyrpc.RegisterProxyResponse); ok {
		return resp, nil
	}
	return nil, fmt.Errorf("Invalid RPC cast.")
}

func getBattleInfo(req *lobbyrpc.BattleInfoRequest) (*lobbyrpc.BattleInfoResponse, error) {
	rawResp, err := lobbyRPCClient.CallTimeout(req, time.Second)
	if err != nil {
		return nil, err
	}
	if resp, ok := rawResp.(*lobbyrpc.BattleInfoResponse); ok {
		return resp, nil
	}
	return nil, fmt.Errorf("Invalid RPC cast.")
}
