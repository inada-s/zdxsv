package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	stackdriver "github.com/tommy351/zap-stackdriver"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/oauth2/google"
)

var (
	// Set by the release build (.goreleaser.yml ldflags).
	releaseVersion = "local"
	releaseCommit  = "local"
	releaseDate    = "local"
)

var cpu = flag.Int("cpu", 2, "setting GOMAXPROCS")
var prodlog = flag.Bool("prodlog", false, "log stackdriver JSON lines")
var logfile = flag.String("logfile", "", "append logs to this file instead of stderr")
var verbosity = flag.Int("v", 0, "log verbosity, 2 and above logs debug messages")

// Accepted so that existing command lines keep starting.
var _ = flag.Bool("logtostderr", true, "ignored")
var _ = flag.Bool("alsologtostderr", false, "ignored")

func prepareLogger() {
	o := LogOptions{Prod: *prodlog, Verbosity: *verbosity, Path: *logfile}
	if *prodlog {
		o.Fields = []zap.Field{
			zap.String("zdxsv_version", releaseVersion),
			zap.String("zdxsv_revision", releaseCommit),
		}
	}
	if err := setupLogger(o); err != nil {
		log.Fatalln("failed to set up the logger:", err)
	}
}

var profile = flag.Int("profile", 1, "0: no profile, 1: enable http pprof on 127.0.0.1 (port: 16060, or per command), 2: enable blocking profile")

func pprofPort(mode string) int {
	switch mode {
	case "dns":
		return 16063
	default:
		return 16060
	}
}

func stripHost(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		logger.Fatal("splitPort failed", zap.String("addr", addr), zap.Error(err))
	}
	return ":" + fmt.Sprint(port)
}

func printUsage() {
	log.Println("Usage: ", os.Args[0], "[all (default), dns, relay [<session id> <hex token>], initdb, migratedb, revoketokens <provider> <user id>, dnascheck host:port [pin...], tlscheck host:port pin..., version]")
}

func prepareDB() {
	conn, err := sqlx.Open("sqlite3", Conf.DB.Name+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		logger.Fatal("startup failed", zap.Error(err))
	}
	DefaultDB = SQLiteDB{
		DB:          conn,
		SQLiteCache: NewSQLiteCache(),
	}
}

func prepareOption(command string) {
	runtime.GOMAXPROCS(*cpu)
	if *profile >= 1 {
		go func() {
			port := pprofPort(command)
			addr := fmt.Sprintf("127.0.0.1:%v", port)
			logger.Error("pprof server stopped", zap.Error(http.ListenAndServe(addr, nil)))
		}()
	}
	if *profile >= 2 {
		runtime.MemProfileRate = 1
		runtime.SetBlockProfileRate(1)
	}
}

func main() {
	flag.Parse()
	prepareLogger()
	defer logger.Sync()
	rand.Seed(time.Now().UnixNano())

	logger.Info("zdxsv - Gundam vs Zeta Gundam private game server", zap.String("version", releaseVersion), zap.String("commit", releaseCommit), zap.String("date", releaseDate))

	args := flag.Args()
	logger.Info("args", zap.Strings("args", args))

	// No command runs every server in this process (the systemd service runs it so).
	command := "all"
	if len(args) >= 1 {
		command = args[0]
	}
	if command == "version" {
		fmt.Println("zdxsv", releaseVersion, releaseCommit, releaseDate)
		return
	}

	LoadConfig()
	prepareOption(command)

	switch command {
	case "all":
		mainAll()
	case "dns":
		mainDNS()
	case "relay":
		if len(args) == 1 {
			mainRelay()
		} else {
			mainRelayTest(args[1:])
		}
	case "dnascheck":
		if len(args) < 2 {
			log.Println("Usage: ", os.Args[0], "dnascheck host:port [pin...]")
			os.Exit(1)
		}
		mainDNASCheck(args[1], args[2:])
	case "tlscheck":
		if len(args) < 3 {
			log.Println("Usage: ", os.Args[0], "tlscheck host:port pin...")
			os.Exit(1)
		}
		mainTLSCheck(args[1], args[2:])
	case "initdb":
		if _, err := os.Stat(Conf.DB.Name); !errors.Is(err, os.ErrNotExist) {
			logger.Fatal("initdb: the database file exists; remove it first to start over", zap.String("name", Conf.DB.Name), zap.Error(err))
		}
		prepareDB()
		if err := DefaultDB.Init(); err != nil {
			logger.Fatal("initdb failed", zap.Error(err))
		}
	case "migratedb":
		prepareDB()
		if err := DefaultDB.Migrate(); err != nil {
			logger.Fatal("migration failed", zap.Error(err))
		}
		logger.Info("migration done")
	case "revoketokens":
		if len(args) != 3 {
			log.Println("Usage: ", os.Args[0], "revoketokens <provider> <user id>")
			os.Exit(1)
		}
		prepareDB()
		n, err := DefaultDB.RevokeProviderTokens(args[1], args[2])
		if err != nil {
			logger.Fatal("revoke failed", zap.Error(err))
		}
		fmt.Println("revoked", n)
	default:
		printUsage()
		os.Exit(1)
	}
}

// mainAll (`zdxsv all`, or `zdxsv` with no command) runs every server in this process:
// dns, dnas (the 443 router with the login pages behind /00000020/), lobby and battle.
// The login pages and the lobby share the login sessions in memory, so they run only
// here; Lbs drives Mcs directly. A listener that cannot bind ends
// the process.
// With ZDXSV_TLS_CERT_FILE set, dnas, lobby and battle also serve their TLS ports.
func mainAll() {
	prepareDB()
	if err := DefaultDB.(SQLiteDB).CheckSchema(); err != nil {
		logger.Fatal("startup failed", zap.Error(err))
	}
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

// serveLbs runs app and its listeners until SIGINT/SIGTERM, then shuts the lobby down.
func serveLbs(app *Lbs) {
	if c := Conf.Lobby; c.RelayRegions != "" {
		if !GceAllocEnabled() || c.RelaySecret == "" {
			logger.Fatal("ZDXSV_LOBBY_RELAY_REGIONS needs ZDXSV_GCE_ONDEMAND, ZDXSV_GCP_PROJECT_ID and ZDXSV_RELAY_SECRET")
		}
		var regions []string
		for _, r := range strings.Split(c.RelayRegions, ",") {
			if r = strings.TrimSpace(r); r != "" {
				regions = append(regions, r)
			}
		}
		app.SetRelayProvisioning(regions, GceRelayProvisioner{})
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
	// The HTTP API binds the host given in its address,
	// unlike the game ports, whose host part is only advertised.
	if c := Conf.Lobby; c.APIAddr != "" {
		go func() {
			logger.Info("start public api", zap.Any("addr", c.APIAddr))
			api := &APIHandler{Status: app.Status, RegisterProxy: app.RegisterProxy, BattleInfo: app.BattleInfo}
			api.LiveUploadEnded = func(code string) { app.Locked(func(a *Lbs) { a.LiveUploadEnded(code) }) }
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
			srv := &http.Server{Addr: c.APIAddr, Handler: api, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
			if err := srv.ListenAndServe(); err != nil {
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
	if c.Region == "" || c.PublicAddr == "" || Conf.Lobby.PublicAddr == "" || Conf.Lobby.RelaySecret == "" {
		logger.Fatal("zdxsv relay needs ZDXSV_RELAY_REGION, ZDXSV_RELAY_PUBLIC_ADDR, ZDXSV_LOBBY_PUBLIC_ADDR and ZDXSV_RELAY_SECRET")
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
	err = RunRegionalRelay(ctx, conn, Conf.Lobby.PublicAddr, RelayStatus{
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

var (
	logger = newDefault()
)

type LogOptions struct {
	// Prod writes stackdriver JSON lines; otherwise a console format.
	Prod bool
	// 0 and 1 log from info up, 2 and above also debug.
	Verbosity int
	// A file to append to; empty means stderr.
	Path   string
	Fields []zap.Field
}

func newDefault() *zap.Logger {
	enc := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	core := zapcore.NewCore(enc, zapcore.Lock(os.Stderr), zap.InfoLevel)
	return zap.New(core, zap.AddCaller())
}

func setupLogger(o LogOptions) error {
	level := zap.InfoLevel
	if 2 <= o.Verbosity {
		level = zap.DebugLevel
	}
	var enc zapcore.Encoder
	if o.Prod {
		enc = zapcore.NewJSONEncoder(stackdriver.EncoderConfig)
	} else {
		enc = zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	}
	path := o.Path
	if path == "" {
		path = "stderr"
	}
	sink, _, err := zap.Open(path)
	if err != nil {
		return err
	}
	core := zapcore.NewCore(enc, sink, level)
	l := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel), zap.ErrorOutput(zapcore.Lock(os.Stderr)))
	if len(o.Fields) != 0 {
		l = l.With(o.Fields...)
	}
	logger = l
	return nil
}

func logDebugEnabled() bool { return logger.Core().Enabled(zap.DebugLevel) }

// floodLog is for log sites a remote peer can hit at line rate: each message
// gets at most floodLogPerSecond lines a second; the next written line carries
// how many were dropped.
var floodLog = &logLimiter{counts: map[string]*logCount{}}

const floodLogPerSecond = 10

type logLimiter struct {
	mu     sync.Mutex
	counts map[string]*logCount
}

type logCount struct {
	sec     int64
	n       int
	dropped int
}

func (l *logLimiter) Error(msg string, fields ...zap.Field) {
	l.log(zapcore.ErrorLevel, msg, fields)
}

func (l *logLimiter) Warn(msg string, fields ...zap.Field) {
	l.log(zapcore.WarnLevel, msg, fields)
}

func (l *logLimiter) Info(msg string, fields ...zap.Field) {
	l.log(zapcore.InfoLevel, msg, fields)
}

func (l *logLimiter) log(lvl zapcore.Level, msg string, fields []zap.Field) {
	dropped, ok := l.take(msg, time.Now().Unix())
	if !ok {
		return
	}
	if dropped > 0 {
		fields = append(fields, zap.Int("dropped", dropped))
	}
	if ce := logger.WithOptions(zap.AddCallerSkip(2)).Check(lvl, msg); ce != nil {
		ce.Write(fields...)
	}
}

func (l *logLimiter) take(msg string, sec int64) (dropped int, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.counts[msg]
	if c == nil {
		c = &logCount{}
		l.counts[msg] = c
	}
	if c.sec != sec {
		c.sec, c.n = sec, 0
	}
	if c.n >= floodLogPerSecond {
		c.dropped++
		return 0, false
	}
	c.n++
	dropped, c.dropped = c.dropped, 0
	return dropped, true
}

// accept returns the next connection on l. Errors other than a closed
// listener (such as running out of file descriptors) are retried with backoff.
func accept(l net.Listener, name string) (net.Conn, error) {
	var delay time.Duration
	for {
		c, err := l.Accept()
		if err == nil || errors.Is(err, net.ErrClosed) {
			return c, err
		}
		delay = min(max(2*delay, 5*time.Millisecond), time.Second)
		logger.Error("accept failed", zap.String("name", name), zap.Error(err), zap.Duration("retry_in", delay))
		time.Sleep(delay)
	}
}
