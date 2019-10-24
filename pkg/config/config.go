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
	if err := env.Parse(&c.Status); err != nil {
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
	DNAS   DNASConfig
	Login  LoginConfig
	Lobby  LobbyConfig
	Battle BattleConfig
	Status StatusConfig
	DB     DBConfig
}

// DNASConfig stores settings for DNAS server.
type DNASConfig struct {
	PublicAddr string `env:"ZDXSV_DNAS_PUBLIC_ADDR"`
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
}

// BattleConfig stores settings for battle server.
type BattleConfig struct {
	Addr       string `env:"ZDXSV_BATTLE_ADDR"`
	RPCAddr    string `env:"ZDXSV_BATTLE_RPC_ADDR"`
	PublicAddr string `env:"ZDXSV_BATTLE_PUBLIC_ADDR"`
}

// StatusConfig stores settings for status server.
type StatusConfig struct {
	Addr string `env:"ZDXSV_STATUS_ADDR"`
}

// DBConfig stores database settings.
type DBConfig struct {
	Name string `env:"ZDXSV_DB_NAME"`
}
