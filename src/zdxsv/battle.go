package main

import (
	"zdxsv/pkg/battle"
	"zdxsv/pkg/config"
)

func mainBattle() {
	logic := battle.NewLogic()
	tcpsv := battle.NewTCPServer(logic)
	udpsv := battle.NewUDPServer(logic)
	go udpsv.ListenAndServe(stripHost(config.Conf.Battle.Addr))
	go tcpsv.ListenAndServe(stripHost(config.Conf.Battle.Addr))
	logic.ServeRpc(stripHost(config.Conf.Battle.RPCAddr))
}
