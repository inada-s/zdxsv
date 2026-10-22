package config

import (
	"log"

	"github.com/caarlos0/env/v6"
	"github.com/golang/glog"
)

// Conf stores global config.
var Conf Config

// LoadConfig loads environmental variable into Conf.
func LoadConfig() {
	var c Config
	if err := env.Parse(&c.DNS); err != nil {
		log.Fatal(err)
	}
	if err := env.Parse(&c.DNAS); err != nil {
		log.Fatal(err)
	}
	if err := env.Parse(&c.Login); err != nil {
		log.Fatal(err)
	}
	if err := env.Parse(&c.Lobby); err != nil {
		log.Fatal(err)
	}
	if err := env.Parse(&c.Battle); err != nil {
		log.Fatal(err)
	}
	if err := env.Parse(&c.DB); err != nil {
		log.Fatal(err)
	}
	glog.Infof("%+v", c)
	Conf = c
}

// Config stores zdxsv config.
type Config struct {
	DNS    DNSConfig
	DNAS   DNASConfig
	Login  LoginConfig
	Lobby  LobbyConfig
	Battle BattleConfig
	DB     DBConfig
}

// DNSConfig stores settings for the DNS server.
type DNSConfig struct {
	// Addr is the UDP address to listen on. A host part binds that interface only
	// (e.g. 192.168.1.3:53 when something else already holds 0.0.0.0:53).
	Addr string `env:"ZDXSV_DNS_ADDR" envDefault:":53"`
}

// DNASConfig stores settings for DNAS server.
type DNASConfig struct {
	PublicAddr string `env:"ZDXSV_DNAS_PUBLIC_ADDR"`
	Addr       string `env:"ZDXSV_DNAS_ADDR" envDefault:":443"`
	Region     string `env:"ZDXSV_DNAS_REGION" envDefault:"jp"` // certificate: jp, us, eu
}

// LoginConfig stores settings for login server.
type LoginConfig struct {
	Addr       string `env:"ZDXSV_LOGIN_ADDR"`
	PublicAddr string `env:"ZDXSV_LOGIN_PUBLIC_ADDR"`
}

// LobbyConfig stores settings for lobby server.
type LobbyConfig struct {
	Addr       string `env:"ZDXSV_LOBBY_ADDR"`
	RPCAddr    string `env:"ZDXSV_LOBBY_RPC_ADDR"`
	PublicAddr string `env:"ZDXSV_LOBBY_PUBLIC_ADDR"`
	// GGPO relay server (UDP) for lobby GGPO battles, off when empty. Offered to players at
	// RelayPublicAddr (default: PublicAddr's host, RelayAddr's port) and RelayPublicAddr6.
	RelayAddr        string `env:"ZDXSV_LOBBY_RELAY_ADDR"`
	RelayPublicAddr  string `env:"ZDXSV_LOBBY_RELAY_PUBLIC_ADDR"`
	RelayPublicAddr6 string `env:"ZDXSV_LOBBY_RELAY_PUBLIC_ADDR6"`
	// Private HTTP API (lobby.OpsHandler, /ops/replay_uploaded for infra/uploader), off when empty.
	// Uploaded replay urls must start with ReplayURLPrefix.
	OpsAddr         string `env:"ZDXSV_LOBBY_OPS_ADDR"`
	ReplayURLPrefix string `env:"ZDXSV_LOBBY_REPLAY_URL_PREFIX" envDefault:"https://storage.googleapis.com/zdxsv/"`
	// Public HTTP API (lobby.APIHandler, /lbs/replay, /lbs/live, /lbs/status), off when empty.
	// Unlike the game ports, the host part is bound (127.0.0.1:8203 stays on loopback).
	APIAddr string `env:"ZDXSV_LOBBY_API_ADDR"`
}

// BattleConfig stores settings for battle server.
type BattleConfig struct {
	Addr       string `env:"ZDXSV_BATTLE_ADDR"`
	RPCAddr    string `env:"ZDXSV_BATTLE_RPC_ADDR"`
	PublicAddr string `env:"ZDXSV_BATTLE_PUBLIC_ADDR"`
}

// DBConfig stores database settings.
type DBConfig struct {
	Name string `env:"ZDXSV_DB_NAME"`
}
