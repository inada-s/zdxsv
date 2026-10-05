package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"zdxsv/pkg/config"
	"zdxsv/pkg/lobby"

	"github.com/golang/glog"
)

func mainLobby() {
	app := lobby.NewApp()
	go app.Serve()
	sv := lobby.NewServer(app)
	go sv.ListenAndServe(stripHost(config.Conf.Lobby.Addr))
	go sv.ServeUDPStunServer(stripHost(config.Conf.Lobby.RPCAddr))
	if c := config.Conf.Lobby; c.RelayAddr != "" {
		public := c.RelayPublicAddr
		if public == "" {
			host, _, _ := net.SplitHostPort(c.PublicAddr)
			public = net.JoinHostPort(host, strings.TrimPrefix(stripHost(c.RelayAddr), ":"))
		}
		go func() {
			if err := lobby.ServeRelay(c.RelayAddr, public, c.RelayPublicAddr6); err != nil {
				glog.Errorln("relay:", err)
			}
		}()
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	s := <-c
	fmt.Println("Got signal:", s)
	app.Quit()
}
