package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"zdxsv/pkg/auth"
	"zdxsv/pkg/config"
	"zdxsv/pkg/db"
	"zdxsv/pkg/lobby"

	"github.com/golang/glog"
)

func mainLobby() {
	serveLobby(lobby.NewApp())
}

// serveLobby runs app and its listeners until SIGINT/SIGTERM, then shuts the lobby down.
func serveLobby(app *lobby.App) {
	if c := config.Conf.Lobby; c.RelayRegions != "" {
		if c.RelayControlAddr == "" || c.RelayStartCommand == "" {
			glog.Fatalln("ZDXSV_LOBBY_RELAY_REGIONS needs ZDXSV_LOBBY_RELAY_CONTROL_ADDR and ZDXSV_LOBBY_RELAY_START_COMMAND")
		}
		var regions []string
		for _, r := range strings.Split(c.RelayRegions, ",") {
			if r = strings.TrimSpace(r); r != "" {
				regions = append(regions, r)
			}
		}
		app.SetRelayProvisioning(regions, lobby.CommandProvisioner{Command: c.RelayStartCommand})
	}
	authServer := newAuthServer()
	if authServer != nil {
		app.SetCarryCodeIssuer(authServer.IssueCarryCode)
	}
	go app.Serve()
	sv := lobby.NewServer(app)
	go sv.ListenAndServe(stripHost(config.Conf.Lobby.Addr))
	if l := tlsListen("lobby", config.Conf.TLS.LobbyAddr); l != nil {
		go sv.Serve(l)
	}
	go sv.ServeUDPStunServer(stripHost(config.Conf.Lobby.RPCAddr))
	go lobby.Spectators.Run()
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
	if c := config.Conf.Lobby; c.RelayControlAddr != "" {
		if c.RelaySecret == "" {
			glog.Fatalln("ZDXSV_LOBBY_RELAY_CONTROL_ADDR needs ZDXSV_RELAY_SECRET")
		}
		l, err := net.Listen("tcp", c.RelayControlAddr)
		if err != nil {
			glog.Fatalln("relay control:", err)
		}
		glog.Infoln("Start relay control", c.RelayControlAddr)
		go func() {
			glog.Errorln("relay control:", lobby.ServeRelayControl(l, string(c.RelaySecret)))
		}()
	}
	// The HTTP APIs bind the host given in their address (127.0.0.1:8203 stays on loopback),
	// unlike the game ports, whose host part is only advertised.
	if c := config.Conf.Lobby; c.OpsAddr != "" {
		go func() {
			glog.Infoln("Start ops api", c.OpsAddr)
			if err := http.ListenAndServe(c.OpsAddr, &lobby.OpsHandler{ReplayURLPrefix: c.ReplayURLPrefix}); err != nil {
				glog.Errorln("ops api:", err)
			}
		}()
	}
	if c := config.Conf.Lobby; c.APIAddr != "" {
		go func() {
			glog.Infoln("Start public api", c.APIAddr)
			api := &lobby.APIHandler{Status: app.Status}
			if authServer != nil {
				api.Auth = authServer
			}
			if c.ReplayBucket != "" {
				store, err := lobby.NewGCSReplayStore(context.Background(), c.ReplayBucket)
				if err != nil {
					glog.Fatalln("replay bucket client:", c.ReplayBucket, err)
				}
				api.ReplayStore = store
			} else if c.ReplayDir != "" {
				api.ReplayStore = &lobby.DirReplayStore{Dir: c.ReplayDir, URL: "http://" + c.APIAddr + "/lbs/"}
			}
			if err := http.ListenAndServe(c.APIAddr, api); err != nil {
				glog.Errorln("public api:", err)
			}
		}()
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	s := <-c
	fmt.Println("Got signal:", s)
	app.Quit()
}

func newAuthServer() *auth.Server {
	c := config.Conf.Lobby
	if c.DiscordClientID == "" {
		return nil
	}
	if c.APIAddr == "" || c.AuthPublicURL == "" || c.DiscordClientSecret == "" {
		glog.Fatalln("ZDXSV_DISCORD_CLIENT_ID needs ZDXSV_LOBBY_API_ADDR, ZDXSV_AUTH_PUBLIC_URL and ZDXSV_DISCORD_CLIENT_SECRET")
	}
	p := auth.Discord(c.DiscordClientID, string(c.DiscordClientSecret))
	if base := strings.TrimSuffix(c.AuthProviderURL, "/"); base != "" {
		glog.Warningln("ZDXSV_AUTH_PROVIDER_URL set: sign-in goes to", base, "instead of Discord")
		for _, u := range []*string{&p.AuthURL, &p.TokenURL, &p.UserURL} {
			*u = strings.Replace(*u, "https://discord.com", base, 1)
		}
	}
	glog.Infoln("Start auth", c.AuthPublicURL+"/auth/")
	return &auth.Server{Provider: p, PublicURL: c.AuthPublicURL, Store: db.DefaultDB}
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

// mainRelay: `zdxsv relay` runs a regional relay (config.RelayConfig) registered with the lobby
// until SIGINT/SIGTERM or the lobby shuts it down.
func mainRelay() {
	c := config.Conf.Relay
	if c.Region == "" || c.PublicAddr == "" || c.LobbyAddr == "" || config.Conf.Lobby.RelaySecret == "" {
		glog.Fatalln("zdxsv relay needs ZDXSV_RELAY_REGION, ZDXSV_RELAY_PUBLIC_ADDR, ZDXSV_RELAY_LOBBY_ADDR and ZDXSV_RELAY_SECRET")
	}
	udpAddr, err := net.ResolveUDPAddr("udp", c.Addr)
	if err != nil {
		glog.Fatalln("relay addr:", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		glog.Fatalln("relay listen:", err)
	}
	defer conn.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()
	err = lobby.RunRegionalRelay(ctx, conn, c.LobbyAddr, lobby.RelayStatus{
		Region: c.Region, PublicAddr: c.PublicAddr, PublicAddr6: c.PublicAddr6, Secret: string(config.Conf.Lobby.RelaySecret),
	})
	glog.Infoln("relay stopped:", err)
}
