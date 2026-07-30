package main

import (
	"errors"
	"sync"
	"time"
)

var (
	errClaimUnavailable     = errors.New("claim unavailable")
	errClaimBindingMismatch = errors.New("claim binding mismatch")
)

type claim struct {
	Code                 string
	SitePortalCode       string
	NodeID               string
	TerminalSessionID    string
	ExternalUserID       string
	DisplayName          string
	AccessToken          string
	AccessTokenExpiresAt time.Time
	PRPBaseURL           string
	ExpiresAt            time.Time
}

type redeemClaimInput struct {
	Code              string `json:"claim_code"`
	SitePortalCode    string `json:"site_portal_code"`
	NodeID            string `json:"node_id"`
	TerminalSessionID string `json:"terminal_session_id"`
}

type claimStore struct {
	mu    sync.Mutex
	items map[string]claim
}

func newClaimStore() *claimStore {
	return &claimStore{items: make(map[string]claim)}
}

func (s *claimStore) create(value claim) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	value.Code = randomToken(32)
	s.items[value.Code] = value
	return value.Code
}

func (s *claimStore) remove(code string) {
	s.mu.Lock()
	delete(s.items, code)
	s.mu.Unlock()
}

func (s *claimStore) redeem(input redeemClaimInput, now time.Time) (claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, exists := s.items[input.Code]
	if !exists {
		return claim{}, errClaimUnavailable
	}
	if !value.ExpiresAt.After(now) || !value.AccessTokenExpiresAt.After(now) {
		delete(s.items, input.Code)
		return claim{}, errClaimUnavailable
	}
	if value.SitePortalCode != input.SitePortalCode || value.NodeID != input.NodeID ||
		value.TerminalSessionID != input.TerminalSessionID {
		return claim{}, errClaimBindingMismatch
	}
	delete(s.items, input.Code)
	return value, nil
}

func (s *claimStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}
