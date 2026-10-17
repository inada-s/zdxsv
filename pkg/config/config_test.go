package config

import (
	"os"
	"strings"
	"testing"
)

// unsetZdxsvEnv clears every ZDXSV_ variable for the test and restores it after.
func unsetZdxsvEnv(t *testing.T) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "ZDXSV_") {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	unsetZdxsvEnv(t)
	t.Cleanup(func() { Conf = Config{} })

	LoadConfig()

	want := Config{
		DNAS:  DNASConfig{Addr: ":443", Region: "jp"},
		Lobby: LobbyConfig{ReplayURLPrefix: "https://storage.googleapis.com/zdxsv/"},
	}
	if Conf != want {
		t.Errorf("Conf = %+v, want %+v", Conf, want)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	unsetZdxsvEnv(t)
	t.Cleanup(func() { Conf = Config{} })
	env := map[string]string{
		"ZDXSV_DNAS_PUBLIC_ADDR":         "1.2.3.4:443",
		"ZDXSV_DNAS_ADDR":                ":10443",
		"ZDXSV_DNAS_REGION":              "us",
		"ZDXSV_LOGIN_ADDR":               ":9876",
		"ZDXSV_LOGIN_PUBLIC_ADDR":        "1.2.3.4:9876",
		"ZDXSV_LOBBY_ADDR":               ":8200",
		"ZDXSV_LOBBY_RPC_ADDR":           ":8201",
		"ZDXSV_LOBBY_PUBLIC_ADDR":        "1.2.3.4:8200",
		"ZDXSV_LOBBY_RELAY_ADDR":         ":8202",
		"ZDXSV_LOBBY_RELAY_PUBLIC_ADDR":  "5.6.7.8:8202",
		"ZDXSV_LOBBY_RELAY_PUBLIC_ADDR6": "[::1]:8202",
		"ZDXSV_LOBBY_OPS_ADDR":           "127.0.0.1:8203",
		"ZDXSV_LOBBY_REPLAY_URL_PREFIX":  "https://example.com/r/",
		"ZDXSV_LOBBY_API_ADDR":           ":8204",
		"ZDXSV_BATTLE_ADDR":              ":8210",
		"ZDXSV_BATTLE_RPC_ADDR":          ":8211",
		"ZDXSV_BATTLE_PUBLIC_ADDR":       "1.2.3.4:8210",
		"ZDXSV_STATUS_ADDR":              ":9100",
		"ZDXSV_DB_NAME":                  "/tmp/zdxsv.db",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}

	LoadConfig()

	want := Config{
		DNAS:  DNASConfig{PublicAddr: "1.2.3.4:443", Addr: ":10443", Region: "us"},
		Login: LoginConfig{Addr: ":9876", PublicAddr: "1.2.3.4:9876"},
		Lobby: LobbyConfig{
			Addr:             ":8200",
			RPCAddr:          ":8201",
			PublicAddr:       "1.2.3.4:8200",
			RelayAddr:        ":8202",
			RelayPublicAddr:  "5.6.7.8:8202",
			RelayPublicAddr6: "[::1]:8202",
			OpsAddr:          "127.0.0.1:8203",
			ReplayURLPrefix:  "https://example.com/r/",
			APIAddr:          ":8204",
		},
		Battle: BattleConfig{Addr: ":8210", RPCAddr: ":8211", PublicAddr: "1.2.3.4:8210"},
		Status: StatusConfig{Addr: ":9100"},
		DB:     DBConfig{Name: "/tmp/zdxsv.db"},
	}
	if Conf != want {
		t.Errorf("Conf = %+v\nwant   %+v", Conf, want)
	}
}
