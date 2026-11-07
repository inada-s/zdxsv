// Package session keeps the login sessions in memory. The login pages start one at each
// login of an account, before a user is chosen; the lobby checks it at every connection
// of that client, the one after a battle included. An account holds a session per client,
// so its users can play at once.
package session

import (
	"sync"
	"time"

	"zdxsv/pkg/db"
)

const (
	// TTL is how long a session lasts after its last use.
	TTL = 24 * time.Hour
	// MaxPerAccount bounds the sessions of an account; the least recently used goes first.
	MaxPerAccount = 16
)

// Default is the store the login pages and the lobby share.
var Default = NewStore()

type session struct {
	loginKey string
	tempKey  string
	used     time.Time
}

type Store struct {
	mu       sync.Mutex
	sessions map[string]*session
	now      func() time.Time
}

func NewStore() *Store {
	return &Store{sessions: map[string]*session{}, now: time.Now}
}

// Start begins a session of the account and returns its id. With temp it also returns a
// key of the session's own that the client logs in with in place of the account's key.
func (s *Store) Start(loginKey string, temp bool) (id, tempKey string) {
	if temp {
		tempKey = db.GenLoginKey()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var own []string
	for sid, ss := range s.sessions {
		if now.Sub(ss.used) >= TTL {
			delete(s.sessions, sid)
		} else if ss.loginKey == loginKey {
			own = append(own, sid)
		}
	}
	for ; len(own) >= MaxPerAccount; own = own[:len(own)-1] {
		oldest := 0
		for i, sid := range own {
			if s.sessions[sid].used.Before(s.sessions[own[oldest]].used) {
				oldest = i
			}
		}
		delete(s.sessions, own[oldest])
		own[oldest] = own[len(own)-1]
	}
	for id == "" || s.sessions[id] != nil {
		id = db.GenSessionID()
	}
	s.sessions[id] = &session{loginKey: loginKey, tempKey: tempKey, used: now}
	return id, tempKey
}

// Use returns the account of session id when key is the account's login key or the
// session's temporary key, and keeps the session for another TTL.
func (s *Store) Use(id, key string) (loginKey string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[id]
	now := s.now()
	if ss == nil || now.Sub(ss.used) >= TTL || key == "" || (key != ss.loginKey && key != ss.tempKey) {
		return "", false
	}
	ss.used = now
	return ss.loginKey, true
}

// Touch keeps session id for another TTL.
func (s *Store) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.sessions[id]; ss != nil && s.now().Sub(ss.used) < TTL {
		ss.used = s.now()
	}
}
