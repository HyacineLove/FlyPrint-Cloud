package main

import (
	"sync"
	"time"
)

type browserSession struct {
	ExternalUserID       string
	DisplayName          string
	PRPBaseURL           string
	AccessToken          string
	AccessTokenExpiresAt time.Time
	ExpiresAt            time.Time
}

type browserSessionStore struct {
	mu       sync.Mutex
	sessions map[string]browserSession
}

func newBrowserSessionStore() *browserSessionStore {
	return &browserSessionStore{sessions: make(map[string]browserSession)}
}

func (s *browserSessionStore) create(session browserSession) string {
	key := randomToken(32)
	s.mu.Lock()
	s.sessions[key] = session
	s.mu.Unlock()
	return key
}

func (s *browserSessionStore) get(key string, now time.Time) (browserSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, exists := s.sessions[key]
	if !exists {
		return browserSession{}, false
	}
	if !session.ExpiresAt.After(now) {
		delete(s.sessions, key)
		return browserSession{}, false
	}
	return session, true
}

func (s *browserSessionStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}
