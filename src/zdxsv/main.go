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

	"github.com/golang/glog"

	"zdxsv/pkg/config"
	"zdxsv/pkg/db"
)

var cpu = flag.Int("cpu", 2, "setting GOMAXPROCS")
var profile = flag.Int("profile", 1, "0: no profile, 1: enable http pprof on 127.0.0.1 (port: 16060, or per command), 2: enable blocking profile")

func pprofPort(mode string) int {
	switch mode {
	case "lobby":
		return 16061
	case "battle":
		return 16062
	case "dns":
		return 16063
	case "login":
		return 16064
	case "dnas":
		return 16066
	default:
		return 16060
	}
}

func stripHost(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		glog.FatalDepth(1, "err in splitPort ", addr, err)
	}
	return ":" + fmt.Sprint(port)
}

func printUsage() {
	log.Println("Usage: ", os.Args[0], "[all (default), dns, dnas, login, lobby, battle, relay <session id> <hex token>, initdb, migratedb, dnascheck host:port]")
}

func prepareDB() {
	conn, err := sqlx.Open("sqlite3", config.Conf.DB.Name)
	if err != nil {
		glog.Fatal(err)
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
			log.Println(http.ListenAndServe(addr, nil))
		}()
	}
	if *profile >= 2 {
		runtime.MemProfileRate = 1
		runtime.SetBlockProfileRate(1)
	}
}

func main() {
	flag.Set("logtostderr", "true")
	flag.Parse()
	rand.Seed(time.Now().UnixNano())

	glog.Infoln("zdxsv - Gundam vs Zeta Gundam private game server.")

	args := flag.Args()
	glog.Infoln(args, len(args))

	// No command runs every server in this process (the docker image's default).
	command := "all"
	if len(args) >= 1 {
		command = args[0]
	}

	config.LoadConfig()
	prepareOption(command)

	switch command {
	case "all":
		mainAll()
	case "dns":
		mainDNS()
	case "battle":
		mainBattle()
	case "lobby":
		prepareDB()
		mainLobby()
	case "login":
		prepareDB()
		mainLogin()
	case "relay":
		mainRelayTest(args[1:])
	case "dnas":
		mainDNAS()
	case "dnascheck":
		if len(args) != 2 {
			log.Println("Usage: ", os.Args[0], "dnascheck host:port")
			os.Exit(1)
		}
		mainDNASCheck(args[1])
	case "initdb":
		os.Remove(config.Conf.DB.Name)
		prepareDB()
		db.DefaultDB.Init()
	case "migratedb":
		prepareDB()
		err := db.DefaultDB.Migrate()
		if err != nil {
			glog.Errorln("Migration failed:", err)
		} else {
			glog.Errorln("Migration done")
		}
	default:
		printUsage()
		os.Exit(1)
	}
}
