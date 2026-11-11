package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
)

// serveLbs runs app and its listeners until SIGINT/SIGTERM, then shuts the lobby down.
func serveLbs(app *Lbs) {
	if c := Conf.Lobby; c.RelayRegions != "" {
		if c.RelayControlAddr == "" || c.RelayStartCommand == "" {
			logger.Fatal("ZDXSV_LOBBY_RELAY_REGIONS needs ZDXSV_LOBBY_RELAY_CONTROL_ADDR and ZDXSV_LOBBY_RELAY_START_COMMAND")
		}
		var regions []string
		for _, r := range strings.Split(c.RelayRegions, ",") {
			if r = strings.TrimSpace(r); r != "" {
				regions = append(regions, r)
			}
		}
		app.SetRelayProvisioning(regions, CommandProvisioner{Command: c.RelayStartCommand})
	}
	authServer := newAuthServer()
	discord := newDiscord()
	if authServer != nil {
		app.SetCarryCodeIssuer(authServer.IssueCarryCode)
	}
	go app.Serve()
	sv := NewLbsServer(app)
	go sv.ListenAndServe(stripHost(Conf.Lobby.Addr))
	if l := tlsListen("lobby", Conf.TLS.LobbyAddr); l != nil {
		go sv.Serve(l)
	}
	go sv.ServeUDPStunServer(stripHost(Conf.Lobby.StunAddr))
	go sv.ServeUDPStun(stripHost(Conf.Lobby.ZproxyStunAddr))
	go Spectators.Run(nil)
	if c := Conf.Lobby; c.RelayAddr != "" {
		public := c.RelayPublicAddr
		if public == "" {
			host, _, _ := net.SplitHostPort(c.PublicAddr)
			public = net.JoinHostPort(host, strings.TrimPrefix(stripHost(c.RelayAddr), ":"))
		}
		go func() {
			if err := ServeRelay(c.RelayAddr, public, c.RelayPublicAddr6); err != nil {
				logger.Error("relay failure", zap.Error(err))
			}
		}()
	}
	if c := Conf.Lobby; c.RelayControlAddr != "" {
		if c.RelaySecret == "" {
			logger.Fatal("ZDXSV_LOBBY_RELAY_CONTROL_ADDR needs ZDXSV_RELAY_SECRET")
		}
		l, err := net.Listen("tcp", c.RelayControlAddr)
		if err != nil {
			logger.Fatal("relay control listen failed", zap.Error(err))
		}
		logger.Info("start relay control", zap.Any("addr", c.RelayControlAddr))
		go func() {
			logger.Error("relay control stopped", zap.Error(ServeRelayControl(l, string(c.RelaySecret))))
		}()
	}
	// The HTTP API binds the host given in its address,
	// unlike the game ports, whose host part is only advertised.
	if c := Conf.Lobby; c.APIAddr != "" {
		go func() {
			logger.Info("start public api", zap.Any("addr", c.APIAddr))
			api := &APIHandler{Status: app.Status, RegisterProxy: app.RegisterProxy, BattleInfo: app.BattleInfo}
			if authServer != nil {
				api.Auth = authServer
			}
			if discord != nil {
				api.Discord = discord
			}
			if c.ReplayBucket != "" {
				store, err := NewGCSReplayStore(context.Background(), c.ReplayBucket)
				if err != nil {
					logger.Fatal("replay bucket client", zap.Any("bucket", c.ReplayBucket), zap.Error(err))
				}
				api.ReplayStore = store
			} else if c.ReplayDir != "" {
				api.ReplayStore = &DirReplayStore{Dir: c.ReplayDir, URL: "http://" + c.APIAddr + "/lbs/"}
			}
			if err := http.ListenAndServe(c.APIAddr, api); err != nil {
				logger.Error("public api stopped", zap.Error(err))
			}
		}()
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	s := <-c
	fmt.Println("Got signal:", s)
	app.Quit()
}

func newAuthServer() *AuthServer {
	c := Conf.Lobby
	if c.AuthDev {
		if c.DiscordClientID != "" || c.APIAddr == "" {
			logger.Fatal("ZDXSV_AUTH_DEV needs ZDXSV_LOBBY_API_ADDR and no ZDXSV_DISCORD_CLIENT_ID")
		}
		public := c.AuthPublicURL
		if public == "" {
			public = "http://" + c.APIAddr
		}
		logger.Warn("ZDXSV_AUTH_DEV set: any user ID signs in; local development only", zap.String("url", public+"/auth/"))
		return &AuthServer{Provider: DevProvider(), PublicURL: public, Store: DefaultDB}
	}
	if c.DiscordClientID == "" {
		return nil
	}
	if c.APIAddr == "" || c.AuthPublicURL == "" || c.DiscordClientSecret == "" {
		logger.Fatal("ZDXSV_DISCORD_CLIENT_ID needs ZDXSV_LOBBY_API_ADDR, ZDXSV_AUTH_PUBLIC_URL and ZDXSV_DISCORD_CLIENT_SECRET")
	}
	p := DiscordProvider(c.DiscordClientID, string(c.DiscordClientSecret))
	if base := strings.TrimSuffix(c.AuthProviderURL, "/"); base != "" {
		logger.Warn("ZDXSV_AUTH_PROVIDER_URL set: sign-in goes there instead of Discord", zap.Any("url", base))
		for _, u := range []*string{&p.AuthURL, &p.TokenURL, &p.UserURL} {
			*u = strings.Replace(*u, "https://discord.com", base, 1)
		}
	}
	logger.Info("start auth", zap.String("url", c.AuthPublicURL+"/auth/"))
	return &AuthServer{Provider: p, PublicURL: c.AuthPublicURL, Store: DefaultDB}
}

// mainRelayTest: `zdxsv relay <session id> <hex token>` runs only the GGPO relay on
// ZDXSV_LOBBY_RELAY_ADDR (default :8223) with one session, for rigs without a lobby.
func mainRelayTest(args []string) {
	if len(args) != 2 {
		logger.Fatal("usage: zdxsv relay <session id> <hex token>")
	}
	id, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil {
		logger.Fatal("bad session id", zap.Error(err))
	}
	token, err := strconv.ParseUint(args[1], 16, 64)
	if err != nil {
		logger.Fatal("bad token", zap.Error(err))
	}
	addr := Conf.Lobby.RelayAddr
	if addr == "" {
		addr = ":8223"
	}
	logger.Fatal("test relay stopped", zap.Error(ServeTestRelay(addr, uint32(id), token)))
}

// mainRelay: `zdxsv relay` runs a regional relay (config.RelayConfig) registered with the lobby
// until SIGINT/SIGTERM or the lobby shuts it down.
func mainRelay() {
	c := Conf.Relay
	if c.Region == "" || c.PublicAddr == "" || c.LobbyAddr == "" || Conf.Lobby.RelaySecret == "" {
		logger.Fatal("zdxsv relay needs ZDXSV_RELAY_REGION, ZDXSV_RELAY_PUBLIC_ADDR, ZDXSV_RELAY_LOBBY_ADDR and ZDXSV_RELAY_SECRET")
	}
	udpAddr, err := net.ResolveUDPAddr("udp", c.Addr)
	if err != nil {
		logger.Fatal("bad relay addr", zap.Error(err))
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		logger.Fatal("relay listen failed", zap.Error(err))
	}
	defer conn.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()
	err = RunRegionalRelay(ctx, conn, c.LobbyAddr, RelayStatus{
		Region: c.Region, PublicAddr: c.PublicAddr, PublicAddr6: c.PublicAddr6, Secret: string(Conf.Lobby.RelaySecret),
	})
	logger.Info("relay stopped", zap.Error(err))
}

func newDiscord() *Discord {
	c := Conf.Lobby
	if c.DiscordPublicKey == "" {
		return nil
	}
	key, err := hex.DecodeString(c.DiscordPublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		logger.Fatal("ZDXSV_DISCORD_PUBLIC_KEY is not an ed25519 public key in hex")
	}
	if c.APIAddr == "" || c.MasterSpreadsheetID == "" {
		logger.Fatal("ZDXSV_DISCORD_PUBLIC_KEY needs ZDXSV_LOBBY_API_ADDR and ZDXSV_MASTER_SPREADSHEET_ID")
	}
	client, err := google.DefaultClient(context.Background(), "https://www.googleapis.com/auth/spreadsheets.readonly")
	if err != nil {
		logger.Fatal("google credentials for the spreadsheet", zap.Error(err))
	}
	allow := map[string]bool{}
	for _, id := range strings.Split(c.DiscordCommandAllow, ",") {
		if id = strings.TrimSpace(id); id != "" {
			allow[id] = true
		}
	}
	sheet := &Spreadsheet{ID: c.MasterSpreadsheetID, Client: client}
	return &Discord{
		PublicKey: ed25519.PublicKey(key),
		Allow:     allow,
		Commands:  MasterCommands(sheet, &MasterUpdater{Sheet: sheet}),
	}
}
