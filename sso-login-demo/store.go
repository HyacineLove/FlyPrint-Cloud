package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	errUserExists   = errors.New("user already exists")
	errUserNotFound = errors.New("user not found")
)

type operatorRecord struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type userRecord struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	PasswordHash string    `json:"password_hash"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type publicUser struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type persistedState struct {
	Operator operatorRecord         `json:"operator"`
	Users    map[string]*userRecord `json:"users"`
}

type identityStore struct {
	mu       sync.RWMutex
	dataFile string
	state    persistedState
}

type createUserInput struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

func newIdentityStore(dataFile, operatorUsername, operatorPassword string) (*identityStore, error) {
	store := &identityStore{
		dataFile: dataFile,
		state: persistedState{
			Users: make(map[string]*userRecord),
		},
	}
	raw, err := os.ReadFile(dataFile)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &store.state); err != nil {
			return nil, fmt.Errorf("decode identity state: %w", err)
		}
		if store.state.Users == nil {
			store.state.Users = make(map[string]*userRecord)
		}
	case errors.Is(err, os.ErrNotExist):
		if strings.TrimSpace(operatorUsername) == "" || len(operatorPassword) < 10 {
			return nil, fmt.Errorf("initial operator credentials are incomplete")
		}
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(operatorPassword), bcrypt.DefaultCost)
		if hashErr != nil {
			return nil, fmt.Errorf("hash initial operator password: %w", hashErr)
		}
		store.state.Operator = operatorRecord{
			Username: strings.TrimSpace(operatorUsername), PasswordHash: string(hash),
		}
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("read identity state: %w", err)
	}
	if store.state.Operator.Username == "" || store.state.Operator.PasswordHash == "" {
		return nil, fmt.Errorf("identity state has no initialized operator")
	}
	return store, nil
}

func (s *identityStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.dataFile), 0700); err != nil {
		return fmt.Errorf("create identity data directory: %w", err)
	}
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode identity state: %w", err)
	}
	temp := s.dataFile + ".tmp"
	if err := os.WriteFile(temp, raw, 0600); err != nil {
		return fmt.Errorf("write identity state: %w", err)
	}
	if err := os.Rename(temp, s.dataFile); err != nil {
		return fmt.Errorf("replace identity state: %w", err)
	}
	return nil
}

func validateUsername(username string) error {
	if len(username) < 3 || len(username) > 64 {
		return fmt.Errorf("username must contain 3 to 64 characters")
	}
	for _, char := range username {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') &&
			!(char >= '0' && char <= '9') && char != '_' && char != '-' && char != '.' {
			return fmt.Errorf("username contains unsupported characters")
		}
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < 10 || len(password) > 128 {
		return fmt.Errorf("password must contain 10 to 128 characters")
	}
	return nil
}

func toPublicUser(user *userRecord) publicUser {
	return publicUser{
		ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
		Enabled: user.Enabled, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
	}
}

func (s *identityStore) authenticateOperator(username, password string) bool {
	s.mu.RLock()
	operator := s.state.Operator
	s.mu.RUnlock()
	return username == operator.Username &&
		bcrypt.CompareHashAndPassword([]byte(operator.PasswordHash), []byte(password)) == nil
}

func (s *identityStore) authenticateUser(username, password string) (*publicUser, bool) {
	s.mu.RLock()
	var candidate *userRecord
	for _, user := range s.state.Users {
		if user.Username == username {
			copy := *user
			candidate = &copy
			break
		}
	}
	s.mu.RUnlock()
	if candidate == nil || !candidate.Enabled ||
		bcrypt.CompareHashAndPassword([]byte(candidate.PasswordHash), []byte(password)) != nil {
		return nil, false
	}
	public := toPublicUser(candidate)
	return &public, true
}

func (s *identityStore) createUser(input createUserInput) (*publicUser, error) {
	input.Username = strings.TrimSpace(input.Username)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if err := validateUsername(input.Username); err != nil {
		return nil, err
	}
	if input.DisplayName == "" || len([]rune(input.DisplayName)) > 120 {
		return nil, fmt.Errorf("display name must contain 1 to 120 characters")
	}
	if err := validatePassword(input.Password); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.state.Users {
		if strings.EqualFold(user.Username, input.Username) {
			return nil, errUserExists
		}
	}
	now := time.Now().UTC()
	user := &userRecord{
		ID: randomOpaqueToken(16), Username: input.Username, DisplayName: input.DisplayName,
		PasswordHash: string(hash), Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	s.state.Users[user.ID] = user
	if err := s.saveLocked(); err != nil {
		delete(s.state.Users, user.ID)
		return nil, err
	}
	public := toPublicUser(user)
	return &public, nil
}

func (s *identityStore) listUsers(search string) []publicUser {
	search = strings.ToLower(strings.TrimSpace(search))
	s.mu.RLock()
	users := make([]publicUser, 0, len(s.state.Users))
	for _, user := range s.state.Users {
		if search != "" && !strings.Contains(strings.ToLower(user.Username), search) &&
			!strings.Contains(strings.ToLower(user.DisplayName), search) {
			continue
		}
		users = append(users, toPublicUser(user))
	}
	s.mu.RUnlock()
	sort.Slice(users, func(i, j int) bool {
		return users[i].CreatedAt.Before(users[j].CreatedAt)
	})
	return users
}

func (s *identityStore) setUserEnabled(id string, enabled bool) (*publicUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.state.Users[id]
	if user == nil {
		return nil, errUserNotFound
	}
	previous := *user
	user.Enabled = enabled
	user.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(); err != nil {
		*user = previous
		return nil, err
	}
	public := toPublicUser(user)
	return &public, nil
}

func (s *identityStore) resetPassword(id, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.state.Users[id]
	if user == nil {
		return errUserNotFound
	}
	previous := *user
	user.PasswordHash = string(hash)
	user.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(); err != nil {
		*user = previous
		return err
	}
	return nil
}

func randomOpaqueToken(byteCount int) string {
	raw := make([]byte, byteCount)
	if _, err := rand.Read(raw); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(raw)
}
