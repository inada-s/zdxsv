package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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
	if c := config.Conf.Lobby; c.OpsAddr != "" {
		go func() {
			glog.Infoln("Start ops api", c.OpsAddr)
			if err := http.ListenAndServe(stripHost(c.OpsAddr), &lobby.OpsHandler{ReplayURLPrefix: c.ReplayURLPrefix}); err != nil {
				glog.Errorln("ops api:", err)
			}
		}()
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	s := <-c
	fmt.Println("Got signal:", s)
	app.Quit()
}

// mainRelayTest: `zdxsv relay <session id> <hex token>` runs only the GGPO relay on
// ZDXSV_LOBBY_RELAY_ADDR (default :8203) with one session, for rigs without a lobby.
func mainRelayTest(args []string) {
	if len(args) != 2 {
		glog.Fatalln("usage: zdxsv relay <session id> <hex token>")
	}
	id, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil {
		glog.Fatalln("session id:", err)
	}
	token, err := strconv.ParseUint(args[1], 16, 64)
	if err != nil {
		glog.Fatalln("token:", err)
	}
	addr := config.Conf.Lobby.RelayAddr
	if addr == "" {
		addr = ":8203"
	}
	glog.Fatalln(lobby.ServeTestRelay(addr, uint32(id), token))
}
