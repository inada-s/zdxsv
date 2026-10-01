package model

import "strings"

// Platform keeps emulator and real PS2 players apart, as gdxsv does:
// lobbies, rooms and battles are per platform.
// Every peer is a console until it sends a platform info message.
const (
	PlatformConsole  = "console"
	PlatformEmuX8664 = "emu-x86/64"
)

// ParsePlatformInfo parses "key=value" lines.
func ParsePlatformInfo(body string) map[string]string {
	info := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		kv := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(kv) == 2 && kv[0] != "" {
			info[kv[0]] = kv[1]
		}
	}
	return info
}

// PlatformFromInfo returns the platform of a peer that sent platform info.
func PlatformFromInfo(info map[string]string) string {
	if info["emulator"] == "" {
		return PlatformConsole
	}
	if info["cpu"] == "x86/64" {
		return PlatformEmuX8664
	}
	return "emu-" + info["cpu"]
}
