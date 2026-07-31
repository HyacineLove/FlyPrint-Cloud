package main

import (
	"errors"
	"sync"
	"time"
)

type terminalContext struct {
	SitePortalCode    string    `json:"site_portal_code"`
	NodeID            string    `json:"node_id"`
	PrinterID         string    `json:"printer_id"`
	TerminalSessionID string    `json:"terminal_session_id"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type loginState struct {
	TerminalTicket string
	Context        terminalContext
	ExpiresAt      time.Time
}

type loginStateStore struct {
	mu    sync.Mutex
	items map[string]loginState
}

func newLoginStateStore() *loginStateStore {
	return &loginStateStore{items: make(map[string]loginState)}
}

func (s *loginStateStore) create(value loginState) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := randomToken(32)
	s.items[state] = value
	return state
}

func (s *loginStateStore) redeem(state string, now time.Time) (loginState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, exists := s.items[state]
	if !exists {
		return loginState{}, errors.New("login state unavailable")
	}
	delete(s.items, state)
	if !value.ExpiresAt.After(now) {
		return loginState{}, errors.New("login state expired")
	}
	return value, nil
}

func (s *loginStateStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

type identityOpsSession struct {
	Token     string    `json:"session_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type localOpsSession struct {
	IdentityToken string
	ExpiresAt     time.Time
}

type localOpsSessionStore struct {
	mu    sync.Mutex
	items map[string]localOpsSession
}

func newLocalOpsSessionStore() *localOpsSessionStore {
	return &localOpsSessionStore{items: make(map[string]localOpsSession)}
}

func (s *localOpsSessionStore) create(value localOpsSession) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := randomToken(32)
	s.items[token] = value
	return token
}

func (s *localOpsSessionStore) get(token string, now time.Time) (localOpsSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, exists := s.items[token]
	if !exists {
		return localOpsSession{}, false
	}
	if !value.ExpiresAt.After(now) {
		delete(s.items, token)
		return localOpsSession{}, false
	}
	return value, true
}

func (s *localOpsSessionStore) remove(token string) {
	s.mu.Lock()
	delete(s.items, token)
	s.mu.Unlock()
}
