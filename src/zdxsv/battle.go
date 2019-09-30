package main

import "zdxsv/pkg/battle"

func mainBattle() {
	logic := battle.NewLogic()
	tcpsv := battle.NewTCPServer(logic)
	udpsv := battle.NewUDPServer(logic)
	go udpsv.ListenAndServe(conf.Battle.Addr)
	go tcpsv.ListenAndServe(conf.Battle.Addr)
	logic.ServeRpc(conf.Battle.RPCAddr)
}
