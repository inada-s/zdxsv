package main

import (
	"os"
	"os/signal"
	"zdxsv/pkg/lobby"
)

func mainLobby() {
	app := lobby.NewApp()
	go app.Serve()
	sv := lobby.NewServer(app)
	go sv.ListenAndServe(stripHost(conf.Lobby.Addr))
	go sv.ServeUDPStunServer(stripHost(conf.Lobby.RPCAddr))

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	<-c
	app.Quit()
}
