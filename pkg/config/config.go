package config

import (
	"log"

	"github.com/caarlos0/env/v6"
	"github.com/golang/glog"
)

var Conf Config

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
	if err := env.Parse(&c.TLS); err != nil {
		log.Fatal(err)
	}
	if err := env.Parse(&c.DB); err != nil {
		log.Fatal(err)
	}
	glog.Infof("%+v", c)
	Conf = c
}

type Config struct {
	DNS    DNSConfig
	DNAS   DNASConfig
	Login  LoginConfig
	Lobby  LobbyConfig
	Battle BattleConfig
	DB     DBConfig
	TLS    TLSConfig
}

type DNSConfig struct {
	// Addr is the UDP address to listen on. A host part binds that interface only
	// (e.g. 192.168.1.3:53 when something else already holds 0.0.0.0:53).
	Addr string `env:"ZDXSV_DNS_ADDR" envDefault:":53"`
}

type DNASConfig struct {
	PublicAddr string `env:"ZDXSV_DNAS_PUBLIC_ADDR"`
	Addr       string `env:"ZDXSV_DNAS_ADDR" envDefault:":443"`
	Region     string `env:"ZDXSV_DNAS_REGION" envDefault:"jp"` // certificate: jp, us, eu
	// Where the non-console TLS connections on Addr go (browsers opening https://<host>),
	// with a PROXY protocol v1 header, e.g. the host's caddy. Empty: they get a TLS alert.
	HTTPSBackend string `env:"ZDXSV_DNAS_HTTPS_BACKEND"`
}

type LoginConfig struct {
	Addr       string `env:"ZDXSV_LOGIN_ADDR"`
	PublicAddr string `env:"ZDXSV_LOGIN_PUBLIC_ADDR"`
}

type LobbyConfig struct {
	Addr       string `env:"ZDXSV_LOBBY_ADDR"`
	RPCAddr    string `env:"ZDXSV_LOBBY_RPC_ADDR"`
	PublicAddr string `env:"ZDXSV_LOBBY_PUBLIC_ADDR"`
	// GGPO relay server (UDP) for lobby GGPO battles, off when empty. Offered to players at
	// RelayPublicAddr (default: PublicAddr's host, RelayAddr's port) and RelayPublicAddr6.
	RelayAddr        string `env:"ZDXSV_LOBBY_RELAY_ADDR"`
	RelayPublicAddr  string `env:"ZDXSV_LOBBY_RELAY_PUBLIC_ADDR"`
	RelayPublicAddr6 string `env:"ZDXSV_LOBBY_RELAY_PUBLIC_ADDR6"`
	// Private HTTP API (lobby.OpsHandler, /ops/replay_uploaded for an external uploader), off when empty.
	// Uploaded replay urls must start with ReplayURLPrefix.
	OpsAddr         string `env:"ZDXSV_LOBBY_OPS_ADDR"`
	ReplayURLPrefix string `env:"ZDXSV_LOBBY_REPLAY_URL_PREFIX" envDefault:"https://storage.googleapis.com/zdxsv/"`
	// Public HTTP API (lobby.APIHandler, /lbs/replay, /lbs/live, /lbs/status), off when empty.
	// Unlike the game ports, the host part is bound (127.0.0.1:8203 stays on loopback).
	APIAddr string `env:"ZDXSV_LOBBY_API_ADDR"`
	// Where /lbs/replay_upload stores replays: a public GCS bucket, else a directory served by the
	// API (local tests, urls http://<APIAddr>/lbs/replays/); upload off when both are empty.
	ReplayBucket string `env:"ZDXSV_LOBBY_REPLAY_BUCKET"`
	ReplayDir    string `env:"ZDXSV_LOBBY_REPLAY_DIR"`
}

type BattleConfig struct {
	Addr       string `env:"ZDXSV_BATTLE_ADDR"`
	RPCAddr    string `env:"ZDXSV_BATTLE_RPC_ADDR"`
	PublicAddr string `env:"ZDXSV_BATTLE_PUBLIC_ADDR"`
}

type DBConfig struct {
	Name string `env:"ZDXSV_DB_NAME"`
}

// TLSConfig is the emulators' TLS ports, one beside each plain TCP port (the client maps
// 443, 8200 and 8210 to them). CertFile holds the server certificate, then the CA
// certificate (PEM); KeyFile the server key. Both empty: no TLS ports.
type TLSConfig struct {
	CertFile   string `env:"ZDXSV_TLS_CERT_FILE"`
	KeyFile    string `env:"ZDXSV_TLS_KEY_FILE"`
	DNASAddr   string `env:"ZDXSV_DNAS_TLS_ADDR" envDefault:":8220"`
	LobbyAddr  string `env:"ZDXSV_LOBBY_TLS_ADDR" envDefault:":8221"`
	BattleAddr string `env:"ZDXSV_BATTLE_TLS_ADDR" envDefault:":8222"`
}
