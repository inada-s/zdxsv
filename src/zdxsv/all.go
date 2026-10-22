package main

import (
	"net/http"

	"zdxsv/pkg/battle"
	"zdxsv/pkg/config"
	"zdxsv/pkg/lobby"

	"github.com/golang/glog"
)

// mainAll runs every server in this process: dns, dnas (the 443 router with the login
// pages behind /00000020/), lobby and battle. Unlike the one-server commands it does not
// listen on ZDXSV_LOGIN_ADDR (the game reaches the login pages through dnas) nor on
// ZDXSV_BATTLE_RPC_ADDR (the lobby drives the battle logic directly), and a listener
// that cannot bind ends the process.
func mainAll() {
	prepareDB()
	login := prepareLogin()
	dnsServer, err := newDNSServer()
	if err != nil {
		glog.Fatalln("dns:", err)
	}
	dnasRouter, err := newDNASRouter(login)
	if err != nil {
		glog.Fatalln("dnas:", err)
	}
	serve := func(name string, f func() error) {
		go func() { glog.Fatalln(name+":", f()) }()
	}
	glog.Infoln("dns listen", config.Conf.DNS.Addr)
	serve("dns", dnsServer.ListenAndServe)
	glog.Infoln("dnas listen", config.Conf.DNAS.Addr, "https backend", config.Conf.DNAS.HTTPSBackend)
	serve("dnas", func() error { return dnasRouter.ListenAndServe(config.Conf.DNAS.Addr) })

	logic := battle.NewLogic()
	battleAddr := stripHost(config.Conf.Battle.Addr)
	glog.Infoln("battle listen", battleAddr)
	serve("battle udp", func() error { return battle.NewUDPServer(logic).ListenAndServe(battleAddr) })
	serve("battle tcp", func() error { return battle.NewTCPServer(logic).ListenAndServe(battleAddr) })

	app := lobby.NewApp()
	app.SetBattleServer(logic.BattleRpc)
	serveLobby(app)
}

// prepareLogin readies the login pages and returns their handler.
func prepareLogin() http.Handler {
	if _, err := assetsCheck(); err != nil {
		glog.Fatalln(err)
	}
	return newLoginHandler()
}
