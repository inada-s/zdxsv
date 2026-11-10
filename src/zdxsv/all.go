package main

import (
	"net/http"

	"zdxsv/pkg/battle"
	"zdxsv/pkg/config"
	"zdxsv/pkg/lobby"

	"zdxsv/pkg/zlog"
)

// mainAll (`zdxsv all`, or `zdxsv` with no command) runs every server in this process:
// dns, dnas (the 443 router with the login pages behind /00000020/), lobby and battle.
// The login pages and the lobby share the login sessions in memory, so they run only
// here. It does not listen on ZDXSV_BATTLE_RPC_ADDR (the lobby drives the battle logic
// directly), and a listener that cannot bind ends the process.
// With ZDXSV_TLS_CERT_FILE set, dnas, lobby and battle also serve their TLS ports.
func mainAll() {
	prepareDB()
	login := prepareLogin()
	dnsServer, err := newDNSServer()
	if err != nil {
		zlog.Fatalln("dns:", err)
	}
	dnasRouter, err := newDNASRouter(login)
	if err != nil {
		zlog.Fatalln("dnas:", err)
	}
	serve := func(name string, f func() error) {
		go func() { zlog.Fatalln(name+":", f()) }()
	}
	zlog.Infoln("dns listen", config.Conf.DNS.Addr)
	serve("dns", dnsServer.ListenAndServe)
	zlog.Infoln("dnas listen", config.Conf.DNAS.Addr, "https backend", config.Conf.DNAS.HTTPSBackend)
	serve("dnas", func() error { return dnasRouter.ListenAndServe(config.Conf.DNAS.Addr) })

	logic := battle.NewLogic()
	battleAddr := stripHost(config.Conf.Battle.Addr)
	zlog.Infoln("battle listen", battleAddr)
	serve("battle udp", func() error { return battle.NewUDPServer(logic).ListenAndServe(battleAddr) })
	serve("battle tcp", func() error { return battle.NewTCPServer(logic).ListenAndServe(battleAddr) })

	app := lobby.NewApp()
	app.SetBattleServer(logic.BattleRpc)
	serveLobby(app)
}

// prepareLogin readies the login pages and returns their handler.
func prepareLogin() http.Handler {
	if _, err := assetsCheck(); err != nil {
		zlog.Fatalln(err)
	}
	return newLoginHandler()
}
