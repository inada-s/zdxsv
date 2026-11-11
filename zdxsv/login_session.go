package main

import (
	"sync"
	"time"
)

const (
	// SessionTTL is how long a session lasts after its last use.
	SessionTTL = 24 * time.Hour
	// MaxSessionsPerAccount bounds the sessions of an account; the least recently used goes first.
	MaxSessionsPerAccount = 16
)

// Sessions is the store the login pages and the lobby share.
var Sessions = NewSessionStore()

type loginSession struct {
	loginKey string
	tempKey  string
	used     time.Time
}

// SessionStore keeps the login sessions in memory. The login pages start one at each
// login of an account, before a user is chosen; the lobby checks it at every connection
// of that client, the one after a battle included. An account holds a session per client,
// so its users can play at once.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*loginSession
	now      func() time.Time
}

func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: map[string]*loginSession{}, now: time.Now}
}

// Start begins a session of the account and returns its id. With temp it also returns a
// key of the session's own that the client logs in with in place of the account's key.
func (s *SessionStore) Start(loginKey string, temp bool) (id, tempKey string) {
	if temp {
		tempKey = GenLoginKey()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var own []string
	for sid, ss := range s.sessions {
		if now.Sub(ss.used) >= SessionTTL {
			delete(s.sessions, sid)
		} else if ss.loginKey == loginKey {
			own = append(own, sid)
		}
	}
	for ; len(own) >= MaxSessionsPerAccount; own = own[:len(own)-1] {
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
		id = GenSessionID()
	}
	s.sessions[id] = &loginSession{loginKey: loginKey, tempKey: tempKey, used: now}
	return id, tempKey
}

// Use returns the account of session id when key is the account's login key or the
// session's temporary key, and keeps the session for another SessionTTL.
func (s *SessionStore) Use(id, key string) (loginKey string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[id]
	now := s.now()
	if ss == nil || now.Sub(ss.used) >= SessionTTL || key == "" || (key != ss.loginKey && key != ss.tempKey) {
		return "", false
	}
	ss.used = now
	return ss.loginKey, true
}

// Touch keeps session id for another SessionTTL.
func (s *SessionStore) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.sessions[id]; ss != nil && s.now().Sub(ss.used) < SessionTTL {
		ss.used = s.now()
	}
}
