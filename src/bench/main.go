package main

import (
	"flag"
	"runtime"
	"sync"
	"time"

	"zdxsv/pkg/battle/bot"

	"github.com/golang/glog"
)

var cpu *int = flag.Int("cpu", 1, "setting GOMAXPROCS")
var _time *string = flag.String("time", "10s", "attack time")
var tcp_players *int = flag.Int("tcp", 2, "tcp client num")
var udp_players *int = flag.Int("udp", 2, "udp client num")
var remote *string = flag.String("remote", "127.0.0.1:8210", "battle server ip and port as ip:port")
var rpcsv *string = flag.String("rpcsv", "127.0.0.1:3080", "battle server ip and port as ip:port")

func main() {
	flag.Set("logtostderr", "true")
	flag.Parse()
	runtime.GOMAXPROCS(*cpu)
	players := *tcp_players + *udp_players
	sessionIDs, err := bot.PrepareServer(players, *rpcsv)
	if err != nil {
		glog.Fatalln(err)
	}
	glog.Infoln("Start Bots")
	var wg sync.WaitGroup
	fin := make(chan interface{})
	for i := 0; i < players; i++ {
		var b bot.Bot
		if i < *tcp_players {
			b = bot.NewTCPBot(i, sessionIDs[i], players, *remote)
		} else {
			b = bot.NewUDPBot(i, sessionIDs[i], players, *remote)
		}
		wg.Add(1)
		go func() {
			err := b.Run(fin)
			b.Summary()
			if err != nil {
				glog.Errorln(err)
			}
			wg.Done()
		}()
		time.Sleep(time.Millisecond)
	}
	t, _ := time.ParseDuration(*_time)
	time.Sleep(t)
	close(fin)
	wg.Wait()
}
