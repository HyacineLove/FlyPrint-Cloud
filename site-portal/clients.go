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

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

type identityProfile struct {
	ID                 string `json:"id"`
	Active             bool   `json:"active"`
	ClientID           string `json:"client_id"`
	ParentIdentityInfo struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"parentIdentityInfo"`
	Attributes struct {
		DepartmentCode string `json:"BMBM"`
		DepartmentName string `json:"BMMC"`
		StudentOrStaff string `json:"XGH"`
		DisplayName    string `json:"XM"`
		ObjectID       string `json:"objectId"`
	} `json:"attributes"`
}

// oidcUserInfo is the standard OpenID Connect UserInfo shape used by
// Keycloak. The optional fields are accepted only as additional consistency
// checks; the UserInfo endpoint remains the authority for the bearer token.
type oidcUserInfo struct {
	Subject           string `json:"sub"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	GivenName         string `json:"given_name"`
	FamilyName        string `json:"family_name"`
	Email             string `json:"email"`
	Active            *bool  `json:"active"`
	ClientID          string `json:"client_id"`
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
	tokenURL, userinfoURL  string
	clientID, clientSecret string
	redirectURI            string
	profileFormat          string
	opsAPIBaseURL          string
	client                 *http.Client
}

func (c *identityClient) exchangeCode(code string) (identityResult, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"redirect_uri":  {c.redirectURI},
		"code":          {code},
	}
	request, err := http.NewRequest(http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return identityResult{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return identityResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return identityResult{}, fmt.Errorf("OAuth token exchange failed: HTTP %d", response.StatusCode)
	}
	var token oauthTokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token); err != nil {
		return identityResult{}, err
	}
	if !strings.EqualFold(token.TokenType, "bearer") || token.AccessToken == "" || token.ExpiresIn <= 0 {
		return identityResult{}, fmt.Errorf("OAuth token response is incomplete")
	}

	profileRequest, err := http.NewRequest(http.MethodGet, c.userinfoURL, nil)
	if err != nil {
		return identityResult{}, err
	}
	profileRequest.Header.Set("Authorization", "Bearer "+token.AccessToken)
	profileRequest.Header.Set("Accept", "application/json")
	profileResponse, err := c.client.Do(profileRequest)
	if err != nil {
		return identityResult{}, err
	}
	defer profileResponse.Body.Close()
	if profileResponse.StatusCode != http.StatusOK {
		return identityResult{}, fmt.Errorf("OAuth userinfo request failed: HTTP %d", profileResponse.StatusCode)
	}
	profileBody, err := io.ReadAll(io.LimitReader(profileResponse.Body, 1<<20))
	if err != nil {
		return identityResult{}, err
	}

	switch strings.ToLower(strings.TrimSpace(c.profileFormat)) {
	case "", "legacy":
		var profile identityProfile
		if err := json.Unmarshal(profileBody, &profile); err != nil {
			return identityResult{}, err
		}
		if !profile.Active || strings.TrimSpace(profile.ID) == "" ||
			strings.TrimSpace(profile.Attributes.DisplayName) == "" ||
			strings.TrimSpace(profile.ClientID) != c.clientID {
			return identityResult{}, fmt.Errorf("OAuth userinfo response is incomplete")
		}
		return identityResult{
			ExternalUserID: profile.ID,
			DisplayName:    profile.Attributes.DisplayName,
			AccessToken:    token.AccessToken,
			ExpiresAt:      time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second),
		}, nil
	case "oidc":
		var profile oidcUserInfo
		if err := json.Unmarshal(profileBody, &profile); err != nil {
			return identityResult{}, err
		}
		if profile.Active != nil && !*profile.Active {
			return identityResult{}, fmt.Errorf("OAuth userinfo response is inactive")
		}
		if strings.TrimSpace(profile.ClientID) != "" && strings.TrimSpace(profile.ClientID) != c.clientID {
			return identityResult{}, fmt.Errorf("OAuth userinfo response client does not match")
		}
		displayName := oidcDisplayName(profile)
		if strings.TrimSpace(profile.Subject) == "" || displayName == "" {
			return identityResult{}, fmt.Errorf("OAuth userinfo response is incomplete")
		}
		return identityResult{
			ExternalUserID: strings.TrimSpace(profile.Subject),
			DisplayName:    displayName,
			AccessToken:    token.AccessToken,
			ExpiresAt:      time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second),
		}, nil
	default:
		return identityResult{}, fmt.Errorf("unsupported OAuth userinfo profile format")
	}
}

func oidcDisplayName(profile oidcUserInfo) string {
	for _, candidate := range []string{
		profile.Name,
		strings.TrimSpace(strings.Join([]string{profile.GivenName, profile.FamilyName}, " ")),
		profile.PreferredUsername,
		profile.Email,
	} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return ""
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
	if strings.TrimSpace(c.opsAPIBaseURL) == "" {
		return nil, 0, fmt.Errorf("identity operator API is not configured")
	}
	request, err := http.NewRequest(method, c.opsAPIBaseURL+path, bytes.NewReader(body))
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
