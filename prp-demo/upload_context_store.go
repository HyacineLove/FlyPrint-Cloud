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
	errUploadContextLimit   = errors.New("upload context limit reached")
)

// maxUploadContexts 限制未消费上传上下文的最大数量，防止内存 DoS。
const maxUploadContexts = 1000

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

// pruneLocked 惰性清理已过期上下文，调用方须持锁。
func (s *uploadContextStore) pruneLocked(now time.Time) {
	for raw, context := range s.contexts {
		if !context.ExpiresAt.After(now) {
			delete(s.contexts, raw)
		}
	}
}

func (s *uploadContextStore) create(subject string, expiresAt time.Time) (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	raw := hex.EncodeToString(random)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	if len(s.contexts) >= maxUploadContexts {
		return "", errUploadContextLimit
	}
	s.contexts[raw] = uploadContext{Subject: subject, ExpiresAt: expiresAt}
	return raw, nil
}

func (s *uploadContextStore) consume(raw string, now time.Time) (uploadContext, error) {
	s.mu.Lock()
	context, exists := s.contexts[raw]
	delete(s.contexts, raw)
	s.pruneLocked(now)
	s.mu.Unlock()

	if !exists {
		return uploadContext{}, errUploadContextInvalid
	}
	if !context.ExpiresAt.After(now) {
		return uploadContext{}, errUploadContextExpired
	}
	return context, nil
}
