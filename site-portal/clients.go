package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type loginCompletion struct {
	TerminalTicket string    `json:"terminal_ticket"`
	ExternalUserID string    `json:"external_user_id"`
	DisplayName    string    `json:"display_name"`
	ClaimCode      string    `json:"claim_code"`
	ClaimExpiresAt time.Time `json:"claim_expires_at"`
}

type cloudBoundary interface {
	validateContext(terminalTicket string) (terminalContext, error)
	completeLogin(input loginCompletion) (string, error)
}

type identityResult struct {
	ExternalUserID string    `json:"external_user_id"`
	DisplayName    string    `json:"display_name"`
	AccessToken    string    `json:"access_token"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type identityBoundary interface {
	exchangeCode(code string) (identityResult, error)
	opsLogin(username, password string) (identityOpsSession, error)
	opsRequest(method, path, token string, body []byte) ([]byte, int, error)
}

type cloudClient struct {
	baseURL, sitePortalCode, clientID, clientSecret string
	client                                          *http.Client
	tokenMu                                         sync.Mutex
	accessToken                                     string
	tokenExpiresAt                                  time.Time
}

type cloudTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

func (c *cloudClient) bearerToken() (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.accessToken != "" && time.Now().Add(30*time.Second).Before(c.tokenExpiresAt) {
		return c.accessToken, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"scope":         {"site-portal:access"},
	}
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/auth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Cloud OAuth token request failed: HTTP %d", response.StatusCode)
	}
	var token cloudTokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token); err != nil {
		return "", fmt.Errorf("decode Cloud OAuth token response: %w", err)
	}
	if token.AccessToken == "" || token.ExpiresIn <= 0 {
		return "", fmt.Errorf("Cloud OAuth token response is incomplete")
	}
	c.accessToken = token.AccessToken
	c.tokenExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	return c.accessToken, nil
}

func (c *cloudClient) request(path string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	bearer, err := c.bearerToken()
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-FlyPrint-Site-Portal", c.sitePortalCode)
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Cloud rejected request: HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("decode Cloud response: %w", err)
	}
	return nil
}

func (c *cloudClient) validateContext(terminalTicket string) (terminalContext, error) {
	var output terminalContext
	err := c.request("/api/v1/site-portal/context", map[string]string{
		"terminal_ticket": terminalTicket,
	}, &output)
	return output, err
}

func (c *cloudClient) completeLogin(input loginCompletion) (string, error) {
	var output struct {
		CloudUserID string `json:"cloud_user_id"`
	}
	if err := c.request("/api/v1/site-portal/login-completions", input, &output); err != nil {
		return "", err
	}
	if output.CloudUserID == "" {
		return "", fmt.Errorf("Cloud login completion response has no user")
	}
	return output.CloudUserID, nil
}

type identityClient struct {
	apiBaseURL, clientSecret string
	client                   *http.Client
}

func (c *identityClient) exchangeCode(code string) (identityResult, error) {
	raw, _ := json.Marshal(map[string]string{"code": code})
	request, err := http.NewRequest(http.MethodPost, c.apiBaseURL+"/api/token", bytes.NewReader(raw))
	if err != nil {
		return identityResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.clientSecret)
	response, err := c.client.Do(request)
	if err != nil {
		return identityResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return identityResult{}, fmt.Errorf("identity code exchange failed: HTTP %d", response.StatusCode)
	}
	var result identityResult
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return identityResult{}, err
	}
	if result.ExternalUserID == "" || result.DisplayName == "" || result.AccessToken == "" ||
		!result.ExpiresAt.After(time.Now()) {
		return identityResult{}, fmt.Errorf("identity code exchange response is incomplete")
	}
	return result, nil
}

func (c *identityClient) opsLogin(username, password string) (identityOpsSession, error) {
	raw, _ := json.Marshal(map[string]string{"username": username, "password": password})
	response, status, err := c.opsRequest(http.MethodPost, "/api/ops/login", "", raw)
	if err != nil {
		return identityOpsSession{}, err
	}
	if status != http.StatusOK {
		return identityOpsSession{}, fmt.Errorf("operator login failed")
	}
	var session identityOpsSession
	if err := json.Unmarshal(response, &session); err != nil {
		return identityOpsSession{}, err
	}
	if session.Token == "" || !session.ExpiresAt.After(time.Now()) {
		return identityOpsSession{}, fmt.Errorf("operator login response is incomplete")
	}
	return session, nil
}

func (c *identityClient) opsRequest(method, path, token string, body []byte) ([]byte, int, error) {
	request, err := http.NewRequest(method, c.apiBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(token) != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return raw, response.StatusCode, err
}
