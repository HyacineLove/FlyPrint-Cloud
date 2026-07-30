package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type configuration struct {
	DataFile            string
	OperatorUsername    string
	OperatorPassword    string
	ClientSecret        string
	AllowedRedirectURIs []string
	CodeTTL             time.Duration
	AccessTokenTTL      time.Duration
	OpsSessionTTL       time.Duration
}

func (c configuration) validate() error {
	if c.DataFile == "" || c.OperatorUsername == "" || c.OperatorPassword == "" ||
		len(c.ClientSecret) < 32 || len(c.AllowedRedirectURIs) == 0 {
		return fmt.Errorf("identity service configuration is incomplete")
	}
	for _, raw := range c.AllowedRedirectURIs {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("invalid allowed redirect URI")
		}
	}
	if c.CodeTTL <= 0 || c.AccessTokenTTL <= 0 || c.OpsSessionTTL <= 0 {
		return fmt.Errorf("identity service TTL values must be positive")
	}
	return nil
}

type codeGrant struct {
	ExternalUserID string
	DisplayName    string
}

type expiringCode struct {
	Grant     codeGrant
	ExpiresAt time.Time
}

type codeStore struct {
	mu    sync.Mutex
	items map[string]expiringCode
}

func newCodeStore() *codeStore {
	return &codeStore{items: make(map[string]expiringCode)}
}

func (s *codeStore) issue(grant codeGrant, expiresAt time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	code := randomOpaqueToken(32)
	s.items[code] = expiringCode{Grant: grant, ExpiresAt: expiresAt}
	return code
}

func (s *codeStore) redeem(code string, now time.Time) (codeGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, exists := s.items[code]
	if !exists {
		return codeGrant{}, errors.New("authorization code unavailable")
	}
	delete(s.items, code)
	if !item.ExpiresAt.After(now) {
		return codeGrant{}, errors.New("authorization code expired")
	}
	return item.Grant, nil
}

type opsSessionStore struct {
	mu    sync.Mutex
	items map[string]time.Time
}

func newOpsSessionStore() *opsSessionStore {
	return &opsSessionStore{items: make(map[string]time.Time)}
}

func (s *opsSessionStore) issue(expiresAt time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := randomOpaqueToken(32)
	s.items[token] = expiresAt
	return token
}

func (s *opsSessionStore) valid(token string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt, exists := s.items[token]
	if !exists {
		return false
	}
	if !expiresAt.After(now) {
		delete(s.items, token)
		return false
	}
	return true
}

type server struct {
	config      configuration
	store       *identityStore
	codes       *codeStore
	opsSessions *opsSessionStore
}

func newServer(config configuration) (*server, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	store, err := newIdentityStore(config.DataFile, config.OperatorUsername, config.OperatorPassword)
	if err != nil {
		return nil, err
	}
	return &server{
		config: config, store: store, codes: newCodeStore(), opsSessions: newOpsSessionStore(),
	}, nil
}

func (s *server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeIdentityJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /api/token", s.exchangeCode)
	mux.HandleFunc("POST /api/ops/login", s.opsLogin)
	mux.HandleFunc("GET /api/ops/users", s.listUsers)
	mux.HandleFunc("POST /api/ops/users", s.createUser)
	mux.HandleFunc("PATCH /api/ops/users/{id}/enabled", s.setUserEnabled)
	mux.HandleFunc("POST /api/ops/users/{id}/reset-password", s.resetUserPassword)
	return identitySecurityHeaders(mux)
}

func (s *server) redirectAllowed(candidate string) bool {
	for _, allowed := range s.config.AllowedRedirectURIs {
		if candidate == allowed {
			return true
		}
	}
	return false
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")
	if !s.redirectAllowed(redirectURI) || state == "" {
		http.Error(w, "invalid login request", http.StatusBadRequest)
		return
	}
	body := `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>统一身份登录</title></head><body><main><h1>统一身份登录</h1><form method="post" action="/login"><input type="hidden" name="redirect_uri" value="` +
		template.HTMLEscapeString(redirectURI) + `"><input type="hidden" name="state" value="` +
		template.HTMLEscapeString(state) + `"><label>账号<input name="username" autocomplete="username" required></label><label>密码<input name="password" type="password" autocomplete="current-password" required></label><button type="submit">登录</button></form></main></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, body)
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	redirectURI := r.Form.Get("redirect_uri")
	state := r.Form.Get("state")
	if !s.redirectAllowed(redirectURI) || state == "" {
		http.Error(w, "invalid login request", http.StatusBadRequest)
		return
	}
	user, ok := s.store.authenticateUser(strings.TrimSpace(r.Form.Get("username")), r.Form.Get("password"))
	if !ok {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	code := s.codes.issue(codeGrant{ExternalUserID: user.ID, DisplayName: user.DisplayName}, time.Now().Add(s.config.CodeTTL))
	redirect, _ := url.Parse(redirectURI)
	query := redirect.Query()
	query.Set("state", state)
	query.Set("code", code)
	redirect.RawQuery = query.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, redirect.String(), http.StatusSeeOther)
}

func bearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(value, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (s *server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	if !constantTimeEqual(bearerToken(r), s.config.ClientSecret) {
		writeIdentityJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if err := decodeIdentityJSON(r, &input); err != nil || input.Code == "" {
		writeIdentityJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	grant, err := s.codes.redeem(input.Code, time.Now())
	if err != nil {
		writeIdentityJSON(w, http.StatusConflict, map[string]string{"error": "authorization_code_unavailable"})
		return
	}
	expiresAt := time.Now().Add(s.config.AccessTokenTTL).UTC()
	writeIdentityJSON(w, http.StatusOK, map[string]any{
		"external_user_id": grant.ExternalUserID,
		"display_name":     grant.DisplayName,
		"access_token":     randomOpaqueToken(32),
		"expires_at":       expiresAt,
	})
}

func (s *server) opsLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeIdentityJSON(r, &input); err != nil {
		writeIdentityJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if !s.store.authenticateOperator(strings.TrimSpace(input.Username), input.Password) {
		writeIdentityJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
		return
	}
	token := s.opsSessions.issue(time.Now().Add(s.config.OpsSessionTTL))
	writeIdentityJSON(w, http.StatusOK, map[string]any{
		"session_token": token,
		"expires_at":    time.Now().Add(s.config.OpsSessionTTL).UTC(),
	})
}

func (s *server) requireOps(w http.ResponseWriter, r *http.Request) bool {
	if !s.opsSessions.valid(bearerToken(r), time.Now()) {
		writeIdentityJSON(w, http.StatusUnauthorized, map[string]string{"error": "ops_session_invalid"})
		return false
	}
	return true
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireOps(w, r) {
		return
	}
	writeIdentityJSON(w, http.StatusOK, map[string]any{"users": s.store.listUsers(r.URL.Query().Get("search"))})
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireOps(w, r) {
		return
	}
	var input createUserInput
	if err := decodeIdentityJSON(r, &input); err != nil {
		writeIdentityJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	user, err := s.store.createUser(input)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errUserExists) {
			status = http.StatusConflict
		}
		writeIdentityJSON(w, status, map[string]string{"error": "user_create_failed", "message": err.Error()})
		return
	}
	writeIdentityJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *server) setUserEnabled(w http.ResponseWriter, r *http.Request) {
	if !s.requireOps(w, r) {
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeIdentityJSON(r, &input); err != nil || input.Enabled == nil {
		writeIdentityJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	user, err := s.store.setUserEnabled(r.PathValue("id"), *input.Enabled)
	if err != nil {
		writeIdentityJSON(w, http.StatusNotFound, map[string]string{"error": "user_not_found"})
		return
	}
	writeIdentityJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireOps(w, r) {
		return
	}
	var input struct {
		NewPassword string `json:"new_password"`
	}
	if err := decodeIdentityJSON(r, &input); err != nil {
		writeIdentityJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if err := s.store.resetPassword(r.PathValue("id"), input.NewPassword); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errUserNotFound) {
			status = http.StatusNotFound
		}
		writeIdentityJSON(w, status, map[string]string{"error": "reset_failed"})
		return
	}
	writeIdentityJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func decodeIdentityJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("request body must contain one JSON value")
	}
	return nil
}

func writeIdentityJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func identitySecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
