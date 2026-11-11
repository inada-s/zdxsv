package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"zdxsv/pkg/config"
	"zdxsv/pkg/db"
	"zdxsv/pkg/zlog"
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
	o := zlog.Options{Prod: *prodlog, Verbosity: *verbosity, Path: *logfile}
	if *prodlog {
		o.Fields = []zap.Field{
			zap.String("zdxsv_version", releaseVersion),
			zap.String("zdxsv_revision", releaseCommit),
		}
	}
	if err := zlog.Setup(o); err != nil {
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
		zlog.Fatal("splitPort failed", zap.String("addr", addr), zap.Error(err))
	}
	return ":" + fmt.Sprint(port)
}

func printUsage() {
	log.Println("Usage: ", os.Args[0], "[all (default), dns, relay [<session id> <hex token>], initdb, migratedb, dnascheck host:port [pin...], tlscheck host:port pin..., lobbyrpccheck host:port, version]")
}

func prepareDB() {
	conn, err := sqlx.Open("sqlite3", config.Conf.DB.Name)
	if err != nil {
		zlog.Fatal("startup failed", zap.Error(err))
	}
	db.DefaultDB = db.SQLiteDB{
		DB:          conn,
		SQLiteCache: db.NewSQLiteCache(),
	}
}

func prepareOption(command string) {
	runtime.GOMAXPROCS(*cpu)
	if *profile >= 1 {
		go func() {
			port := pprofPort(command)
			addr := fmt.Sprintf("127.0.0.1:%v", port)
			zlog.Error("pprof server stopped", zap.Error(http.ListenAndServe(addr, nil)))
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
	defer zlog.Sync()
	rand.Seed(time.Now().UnixNano())

	zlog.Info("zdxsv - Gundam vs Zeta Gundam private game server", zap.String("version", releaseVersion), zap.String("commit", releaseCommit), zap.String("date", releaseDate))

	args := flag.Args()
	zlog.Info("args", zap.Strings("args", args))

	// No command runs every server in this process (the systemd service runs it so).
	command := "all"
	if len(args) >= 1 {
		command = args[0]
	}
	if command == "version" {
		fmt.Println("zdxsv", releaseVersion, releaseCommit, releaseDate)
		return
	}

	config.LoadConfig()
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
	case "lobbyrpccheck":
		if len(args) < 2 {
			log.Println("Usage: ", os.Args[0], "lobbyrpccheck host:port")
			os.Exit(1)
		}
		mainLobbyRPCCheck(args[1])
	case "initdb":
		os.Remove(config.Conf.DB.Name)
		prepareDB()
		db.DefaultDB.Init()
	case "migratedb":
		prepareDB()
		err := db.DefaultDB.Migrate()
		if err != nil {
			zlog.Error("migration failed", zap.Error(err))
		} else {
			zlog.Info("migration done")
		}
	default:
		printUsage()
		os.Exit(1)
	}
}
