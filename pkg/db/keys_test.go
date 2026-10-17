package db

import (
	"math/rand"
	"regexp"
	"testing"
)

func TestGeneratedKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		gen  func() string
		re   *regexp.Regexp
	}{
		{"login key", genLoginKey, regexp.MustCompile(`^[a-zA-Z0-9]{10}$`)},
		{"user id", genUserID, regexp.MustCompile(`^[A-Z2-9]{6}$`)},
		{"session id", genSessionID, regexp.MustCompile(`^[1-9]{8}$`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := map[string]bool{}
			for i := 0; i < 1000; i++ {
				k := tc.gen()
				if !tc.re.MatchString(k) {
					t.Fatalf("%q does not match %v", k, tc.re)
				}
				seen[k] = true
			}
			if len(seen) < 990 {
				t.Errorf("%d distinct of 1000", len(seen))
			}

			// Keys are credentials: they must not follow the math/rand seed.
			rand.Seed(1)
			a := tc.gen()
			rand.Seed(1)
			if b := tc.gen(); a == b {
				t.Errorf("same key %q after the same math/rand seed", a)
			}
		})
	}
}
