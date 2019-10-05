package main

import (
	"database/sql"
	"flag"
	"log"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"time"

	"github.com/golang/glog"

	"zdxsv/pkg/config"
	"zdxsv/pkg/db"
)

var cpu = flag.Int("cpu", 2, "setting GOMAXPROCS")
var profile = flag.Int("profile", 0, "0: no profile, 1: enable http pprof, 2: enable blocking profile")
var conf config.Config

func printUsage() {
	log.Println("Usage: ", os.Args[0], "[login, lobby, battle]", "config.toml")
}

func prepareDB() {
	conn, err := sql.Open("sqlite3", conf.DB.Name)
	if err != nil {
		glog.Fatalln(err)
	}
	db.DefaultDB = db.SQLiteDB{conn}
}

func prepareOption() {
	runtime.GOMAXPROCS(*cpu)
	if *profile >= 1 {
		go func() {
			log.Println(http.ListenAndServe(":6060", nil))
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

	prepareOption()

	args := flag.Args()
	glog.Infoln(args, len(args))

	if len(args) < 1 {
		printUsage()
		os.Exit(1)
	}

	if len(args) >= 2 {
		err := config.LoadFile(args[1])
		if err == nil {
			glog.Fatal(err)
		}
		conf = config.Conf
	}

	switch args[0] {
	case "battle":
		mainBattle()
	case "lobby":
		prepareDB()
		mainLobby()
	case "login":
		prepareDB()
		mainLogin()
	case "status":
		mainStatus()
	case "initdb":
		os.Remove(conf.DB.Name)
		prepareDB()
		db.DefaultDB.Init()
	default:
		printUsage()
		os.Exit(1)
	}
}
