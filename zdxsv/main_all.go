package main

import (
	"net/http"

	"go.uber.org/zap"
)

// mainAll (`zdxsv all`, or `zdxsv` with no command) runs every server in this process:
// dns, dnas (the 443 router with the login pages behind /00000020/), lobby and battle.
// The login pages and the lobby share the login sessions in memory, so they run only
// here; Lbs drives Mcs directly. A listener that cannot bind ends
// the process.
// With ZDXSV_TLS_CERT_FILE set, dnas, lobby and battle also serve their TLS ports.
func mainAll() {
	prepareDB()
	login := prepareLogin()
	dnsServer, err := newDNSServer()
	if err != nil {
		logger.Fatal("dns", zap.Error(err))
	}
	dnasRouter, err := newDNASRouter(login)
	if err != nil {
		logger.Fatal("dnas", zap.Error(err))
	}
	serve := func(name string, f func() error) {
		go func() { logger.Fatal("server stopped", zap.String("name", name), zap.Error(f())) }()
	}
	logger.Info("dns listen", zap.String("addr", Conf.DNS.Addr))
	serve("dns", dnsServer.ListenAndServe)
	logger.Info("dnas listen", zap.String("addr", Conf.DNAS.Addr), zap.String("https_backend", Conf.DNAS.HTTPSBackend))
	serve("dnas", func() error { return dnasRouter.ListenAndServe(Conf.DNAS.Addr) })

	mcs := NewMcs()
	battleAddr := stripHost(Conf.Battle.Addr)
	logger.Info("battle listen", zap.Any("addr", battleAddr))
	serve("battle udp", func() error { return NewUDPServer(mcs).ListenAndServe(battleAddr) })
	serve("battle tcp", func() error { return NewTCPServer(mcs).ListenAndServe(battleAddr) })

	app := NewLbs()
	app.SetBattleServer(mcs)
	serveLbs(app)
}

// prepareLogin readies the login pages and returns their handler.
func prepareLogin() http.Handler {
	if _, err := assetsCheck(); err != nil {
		logger.Fatal("all", zap.Error(err))
	}
	return newLoginHandler()
}
