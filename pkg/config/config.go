package config

import "github.com/BurntSushi/toml"

var Conf Config

type Config struct {
	Login  LoginConfig  `toml:"login"`
	Lobby  LobbyConfig  `toml:"lobby"`
	Battle BattleConfig `toml:"battle"`
	Status StatusConfig `toml:"status"`
	DB     DBConfig     `toml:"db"`
}

type LoginConfig struct {
	Addr       string `toml:"addr"`
	PublicAddr string `toml:"public_addr"`
}

type LobbyConfig struct {
	Addr       string `toml:"addr"`
	RPCAddr    string `toml:"rpc_addr"`
	PublicAddr string `toml:"public_addr"`
}

type BattleConfig struct {
	Addr       string `toml:"addr"`
	RPCAddr    string `toml:"rpc_addr"`
	PublicAddr string `toml:"public_addr"`
}

type StatusConfig struct {
	Addr string `toml:"addr"`
}

type DBConfig struct {
	Name string `toml:"name"`
}

func LoadFile(file string) error {
	_, err := toml.DecodeFile(file, &Conf)
	return err
}
