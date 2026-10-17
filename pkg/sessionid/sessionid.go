// Package sessionid decodes the session id the game sends to the lobby and battle servers.
package sessionid

import (
	"fmt"
	"strconv"
)

// Decode turns the game's value (up to 10 digits) into the 8 digit session id:
// value - 100001, zero padded to 10 digits, without digits 0 and 5.
func Decode(value string) (string, error) {
	if len(value) == 0 || len(value) > 10 {
		return "", fmt.Errorf("session id value %q: want 1 to 10 digits", value)
	}
	v, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return "", fmt.Errorf("session id value %q: %w", value, err)
	}
	if v < 100001 {
		return "", fmt.Errorf("session id value %q: below 100001", value)
	}
	s := fmt.Sprintf("%010d", v-100001)
	return s[1:5] + s[6:], nil
}
