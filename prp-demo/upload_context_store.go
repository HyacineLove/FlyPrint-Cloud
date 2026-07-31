package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	errUploadContextInvalid = errors.New("upload context invalid")
	errUploadContextExpired = errors.New("upload context expired")
)

type uploadContext struct {
	Subject   string
	ExpiresAt time.Time
}

type uploadContextStore struct {
	mu       sync.Mutex
	contexts map[string]uploadContext
}

func newUploadContextStore() *uploadContextStore {
	return &uploadContextStore{contexts: make(map[string]uploadContext)}
}

func (s *uploadContextStore) create(subject string, expiresAt time.Time) (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	raw := hex.EncodeToString(random)

	s.mu.Lock()
	s.contexts[raw] = uploadContext{Subject: subject, ExpiresAt: expiresAt}
	s.mu.Unlock()
	return raw, nil
}

func (s *uploadContextStore) consume(raw string, now time.Time) (uploadContext, error) {
	s.mu.Lock()
	context, exists := s.contexts[raw]
	delete(s.contexts, raw)
	s.mu.Unlock()

	if !exists {
		return uploadContext{}, errUploadContextInvalid
	}
	if !context.ExpiresAt.After(now) {
		return uploadContext{}, errUploadContextExpired
	}
	return context, nil
}
