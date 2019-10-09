package main

import "zdxsv/pkg/battle"

func mainBattle() {
	logic := battle.NewLogic()
	tcpsv := battle.NewTCPServer(logic)
	udpsv := battle.NewUDPServer(logic)
	go udpsv.ListenAndServe(stripHost(conf.Battle.Addr))
	go tcpsv.ListenAndServe(stripHost(conf.Battle.Addr))
	logic.ServeRpc(stripHost(conf.Battle.RPCAddr))
}
