package login

import (
	"regexp"
	"testing"
	"time"
)

func TestSessionStore(t *testing.T) {
	s := NewSessionStore()
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }

	first, noTemp := s.Start("key1", false)
	second, temp := s.Start("key1", true)
	if first == second || noTemp != "" || !regexp.MustCompile(`^[1-9]{8}$`).MatchString(first) ||
		!regexp.MustCompile(`^[a-zA-Z0-9]{10}$`).MatchString(temp) {
		t.Fatalf("sessions %q %q, temporary keys %q %q", first, second, noTemp, temp)
	}
	for _, c := range []struct {
		id, key string
		ok      bool
	}{
		{first, "key1", true},
		{second, "key1", true},
		{second, temp, true},
		{first, temp, false},
		{first, "key2", false},
		{first, "", false},
		{"00000000", "key1", false},
	} {
		if got, ok := s.Use(c.id, c.key); ok != c.ok || (ok && got != "key1") {
			t.Errorf("Use(%s, %q) = %q, %v", c.id, c.key, got, ok)
		}
	}

	now = now.Add(SessionTTL - time.Minute)
	s.Touch(second)
	now = now.Add(2 * time.Minute)
	if _, ok := s.Use(first, "key1"); ok {
		t.Error("a session unused for SessionTTL opens")
	}
	if _, ok := s.Use(second, temp); !ok {
		t.Error("a touched session ended")
	}

	other, _ := s.Start("key2", false)
	var ids []string
	for i := 0; i < MaxSessionsPerAccount; i++ {
		now = now.Add(time.Second)
		id, _ := s.Start("key1", false)
		ids = append(ids, id)
	}
	if _, ok := s.Use(second, "key1"); ok {
		t.Error("the least recently used session outlived the account's limit")
	}
	for _, id := range ids {
		if _, ok := s.Use(id, "key1"); !ok {
			t.Errorf("session %s of the latest %d ended", id, MaxSessionsPerAccount)
		}
	}
	if _, ok := s.Use(other, "key2"); !ok {
		t.Error("another account's session ended")
	}
	if len(s.sessions) != MaxSessionsPerAccount+1 {
		t.Errorf("%d sessions kept", len(s.sessions))
	}
}
