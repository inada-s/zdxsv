package main

import "flag"

var (
	conf config

	updatecheck = flag.Bool("updatecheck", true, "Check latest version and download it")
	// updatecheck = flag.Bool("updatecheck", false, "Check latest version and download it")
	userid       = flag.String("userid", "_AUTO_", "The game UserID to request register this proxy")
	lobbyrpcaddr = flag.String("lobbyrpcaddr", "zdxsv.net:8201", "Lobby RPC server address")
	// lobbyrpcaddr = flag.String("lobbyrpcaddr", "35.187.206.118:8201", "Lobby RPC server address")
	tcpport    = flag.Int("tcpport", 8250, "The TCP port for Listen and Serve PS2 connection")
	udpport    = flag.Int("udpport", 8250, "The UDP port for communicate with the battle server and other peers")
	enableupnp = flag.Bool("upnp", true, "Attempt to open udpport with UPnP")
	profile    = flag.Int("profile", 0, "0: no profile, 1: enable http pprof, 2: enable blocking profile")
	verbose    = flag.Bool("verbose", false, "verbose logging")
)

type config struct {
	CheckUpdate    bool
	EnableUPnP     bool
	RegisterUserID string
	LobbyRPCAddr   string
	TCPListenPort  uint16
	UDPListenPort  uint16
	ProfileLevel   int
	Verbose        bool
}

func init() {
	flag.Parse()

	conf = config{
		CheckUpdate:    *updatecheck,
		EnableUPnP:     *enableupnp,
		RegisterUserID: *userid,
		LobbyRPCAddr:   *lobbyrpcaddr,
		TCPListenPort:  uint16(*tcpport),
		UDPListenPort:  uint16(*udpport),
		ProfileLevel:   *profile,
		Verbose:        *verbose,
	}
}
