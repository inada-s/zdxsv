package main

import (
	"github.com/caarlos0/env/v6"
	"go.uber.org/zap"
)

var Conf Config

func LoadConfig() {
	var c Config
	if err := env.Parse(&c.DNS); err != nil {
		logger.Fatal("config", zap.Error(err))
	}
	if err := env.Parse(&c.DNAS); err != nil {
		logger.Fatal("config", zap.Error(err))
	}
	if err := env.Parse(&c.Login); err != nil {
		logger.Fatal("config", zap.Error(err))
	}
	if err := env.Parse(&c.Lobby); err != nil {
		logger.Fatal("config", zap.Error(err))
	}
	if err := env.Parse(&c.Battle); err != nil {
		logger.Fatal("config", zap.Error(err))
	}
	if err := env.Parse(&c.TLS); err != nil {
		logger.Fatal("config", zap.Error(err))
	}
	if err := env.Parse(&c.Relay); err != nil {
		logger.Fatal("config", zap.Error(err))
	}
	if err := env.Parse(&c.DB); err != nil {
		logger.Fatal("config", zap.Error(err))
	}
	logger.Info("config", zap.Any("config", c))
	Conf = c
}

type Config struct {
	DNS    DNSConfig
	DNAS   DNASConfig
	Login  LoginConfig
	Lobby  LbsConfig
	Battle McsConfig
	DB     DBConfig
	TLS    TLSConfig
	Relay  RelayConfig
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
	PublicAddr string `env:"ZDXSV_LOGIN_PUBLIC_ADDR"`
}

type LbsConfig struct {
	Addr string `env:"ZDXSV_LOBBY_ADDR"`
	// zproxy: STUN on UDP.
	ZproxyStunAddr string `env:"ZDXSV_LOBBY_ZPROXY_STUN_ADDR" envDefault:":8201"`
	PublicAddr     string `env:"ZDXSV_LOBBY_PUBLIC_ADDR"`
	// Emulators' UDP: STUN on StunAddr, the connectivity test on its port + 1.
	StunAddr string `env:"ZDXSV_LOBBY_STUN_ADDR" envDefault:":8221"`
	// GGPO relay server (UDP) for lobby GGPO battles, off when empty. Offered to players at
	// RelayPublicAddr (default: PublicAddr's host, RelayAddr's port) and RelayPublicAddr6.
	RelayAddr        string `env:"ZDXSV_LOBBY_RELAY_ADDR"`
	RelayPublicAddr  string `env:"ZDXSV_LOBBY_RELAY_PUBLIC_ADDR"`
	RelayPublicAddr6 string `env:"ZDXSV_LOBBY_RELAY_PUBLIC_ADDR6"`
	// TCP address where regional relays (`zdxsv relay`) register, off when empty; they must
	// send RelaySecret.
	RelayControlAddr string `env:"ZDXSV_LOBBY_RELAY_CONTROL_ADDR"`
	RelaySecret      Secret `env:"ZDXSV_RELAY_SECRET"`
	// On-demand regional relays, off when RelayRegions (comma separated "<region>=<ms>" keys) is
	// empty: RelayStartCommand is run with a region as its argument to start a relay there.
	RelayRegions      string `env:"ZDXSV_LOBBY_RELAY_REGIONS"`
	RelayStartCommand string `env:"ZDXSV_LOBBY_RELAY_START_COMMAND"`
	// Public HTTP API (lobby.APIHandler, /lbs/replay, /lbs/live, /lbs/status), off when empty.
	// Unlike the game ports, the host part is bound (127.0.0.1:8203 stays on loopback).
	APIAddr string `env:"ZDXSV_LOBBY_API_ADDR"`
	// Where /lbs/replay_upload stores replays: a public GCS bucket, else a directory served by the
	// API (local tests, urls http://<APIAddr>/lbs/replays/); upload off when both are empty.
	ReplayBucket string `env:"ZDXSV_LOBBY_REPLAY_BUCKET"`
	ReplayDir    string `env:"ZDXSV_LOBBY_REPLAY_DIR"`
	// Emulator sign-in through Discord (auth_*.go, /auth/* on APIAddr), off when DiscordClientID is
	// empty. AuthPublicURL is where players reach /auth/ (https://host). AuthProviderURL replaces
	// https://discord.com, for tests only.
	DiscordClientID     string `env:"ZDXSV_DISCORD_CLIENT_ID"`
	DiscordClientSecret Secret `env:"ZDXSV_DISCORD_CLIENT_SECRET"`
	AuthPublicURL       string `env:"ZDXSV_AUTH_PUBLIC_URL"`
	AuthProviderURL     string `env:"ZDXSV_AUTH_PROVIDER_URL"`
	// Local development sign-in (DevProvider): any user ID typed in the browser signs in.
	// Refused together with DiscordClientID; AuthPublicURL defaults to http://<APIAddr>.
	AuthDev bool `env:"ZDXSV_AUTH_DEV"`
	// Discord slash commands (chatops_*.go, POST /discord/interactions on APIAddr), off when
	// DiscordPublicKey (the application's public key, hex) is empty. DiscordCommandAllow: the
	// comma separated user and role ids that may run them. /op master-up reads
	// MasterSpreadsheetID with the Application Default Credentials into the m_ tables.
	DiscordPublicKey    string `env:"ZDXSV_DISCORD_PUBLIC_KEY"`
	DiscordCommandAllow string `env:"ZDXSV_DISCORD_COMMAND_ALLOW"`
	MasterSpreadsheetID string `env:"ZDXSV_MASTER_SPREADSHEET_ID"`
}

type McsConfig struct {
	Addr       string `env:"ZDXSV_BATTLE_ADDR"`
	PublicAddr string `env:"ZDXSV_BATTLE_PUBLIC_ADDR"`
}

type DBConfig struct {
	Name string `env:"ZDXSV_DB_NAME"`
}

// TLSConfig is the emulators' TLS lobby port, beside the plain one (the client maps
// 8200 to it). CertFile holds the server certificate, then the CA
// certificate (PEM); KeyFile the server key. Both empty: no TLS port.
type TLSConfig struct {
	CertFile  string `env:"ZDXSV_TLS_CERT_FILE"`
	KeyFile   string `env:"ZDXSV_TLS_KEY_FILE"`
	LobbyAddr string `env:"ZDXSV_LOBBY_TLS_ADDR" envDefault:":8220"`
}

// RelayConfig is a regional relay (`zdxsv relay`): it relays on Addr, offered to players at
// PublicAddr / PublicAddr6, for the players near Region (a "<region>=<ms>" platform info key),
// registered with the lobby's LobbyConfig.RelayControlAddr at LobbyAddr with Lobby.RelaySecret.
type RelayConfig struct {
	Region      string `env:"ZDXSV_RELAY_REGION"`
	Addr        string `env:"ZDXSV_RELAY_ADDR" envDefault:":8223"`
	PublicAddr  string `env:"ZDXSV_RELAY_PUBLIC_ADDR"`
	PublicAddr6 string `env:"ZDXSV_RELAY_PUBLIC_ADDR6"`
	LobbyAddr   string `env:"ZDXSV_RELAY_LOBBY_ADDR"`
}

// Secret is kept out of the logged config.
type Secret string

func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return "***"
}
