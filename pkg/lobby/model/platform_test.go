package model

import "testing"

func TestPlatformFromInfo(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"emulator=pcsx2\nversion=v2.8.2\nos=windows\ncpu=x86/64\n", PlatformEmuX8664},
		{"emulator=pcsx2\ncpu=arm64\n", "emu-arm64"},
		{"os=windows\n", PlatformConsole},
		{"", PlatformConsole},
	}
	for _, c := range cases {
		if got := PlatformFromInfo(ParsePlatformInfo(c.body)); got != c.want {
			t.Errorf("PlatformFromInfo(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}
