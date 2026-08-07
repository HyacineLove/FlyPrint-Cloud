package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIdentityTokenTravelsFromIdentityThroughPortalClaimWithoutEnteringCloud(t *testing.T) {
	now := time.Now().UTC()
	var (
		cloudMu         sync.Mutex
		cloudCompletion map[string]any
	)
	cloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/token" {
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" ||
				r.Form.Get("client_id") != "site-portal-client" || r.Form.Get("client_secret") != "cloud-portal-secret-12345678901234567890" {
				http.Error(w, "invalid client credentials", http.StatusUnauthorized)
				return
			}
			writePortalJSON(w, http.StatusOK, map[string]any{
				"access_token": "oauth-access-token", "token_type": "bearer", "expires_in": 300,
			})
			return
		}
		if r.Header.Get("X-FlyPrint-Site-Portal") != "official" ||
			r.Header.Get("Authorization") != "Bearer oauth-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/site-portal/context":
			writePortalJSON(w, http.StatusOK, terminalContext{
				SitePortalCode: "official", NodeID: "edge-1", PrinterID: "printer-1",
				TerminalSessionID: "session-1", ExpiresAt: now.Add(5 * time.Minute),
			})
		case "/api/v1/site-portal/login-completions":
			raw, _ := io.ReadAll(r.Body)
			if bytes.Contains(raw, []byte("private-prp-token")) || bytes.Contains(raw, []byte("access_token")) {
				t.Errorf("private credential entered Cloud request: %s", raw)
			}
			cloudMu.Lock()
			_ = json.Unmarshal(raw, &cloudCompletion)
			cloudMu.Unlock()
			writePortalJSON(w, http.StatusOK, map[string]string{"cloud_user_id": "cloud-user-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer cloudServer.Close()

	identityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2.0/accessToken":
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "authorization_code" ||
				r.Form.Get("client_id") != "identity-client" ||
				r.Form.Get("client_secret") != "identity-client-secret-123456789012" ||
				r.Form.Get("redirect_uri") != "https://portal.example.test/auth/callback" ||
				r.Form.Get("code") != "identity-code" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			writePortalJSON(w, http.StatusOK, map[string]any{
				"access_token": "sso-access-token", "token_type": "bearer", "expires_in": 300,
			})
		case "/oauth2.0/profile":
			if r.Header.Get("Authorization") != "Bearer sso-access-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			writePortalJSON(w, http.StatusOK, map[string]any{
				"id": "external-user-1", "active": true, "client_id": "identity-client",
				"parentIdentityInfo": map[string]string{"code": "JG", "name": "教职工"},
				"attributes":         map[string]string{"XGH": "external-user-1", "XM": "张老师", "objectId": "object-1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer identityServer.Close()

	client := &http.Client{Timeout: time.Second}
	config := testPortalConfig()
	cloud := &cloudClient{
		baseURL: cloudServer.URL, sitePortalCode: "official",
		clientID: "site-portal-client", clientSecret: "cloud-portal-secret-12345678901234567890", client: client,
	}
	identity := &identityClient{
		tokenURL:     identityServer.URL + "/oauth2.0/accessToken",
		userinfoURL:  identityServer.URL + "/oauth2.0/profile",
		clientID:     "identity-client",
		clientSecret: "identity-client-secret-123456789012",
		redirectURI:  "https://portal.example.test/auth/callback",
		client:       client,
	}
	portal := newPortalServer(config, cloud, identity)

	context, err := cloud.validateContext("raw-ticket")
	if err != nil {
		t.Fatal(err)
	}
	state := portal.loginStates.create(loginState{
		TerminalTicket: "raw-ticket", Context: context, ExpiresAt: now.Add(5 * time.Minute),
	})
	callback := httptest.NewRecorder()
	portal.Handler().ServeHTTP(callback, httptest.NewRequest(
		http.MethodGet, "/auth/callback?state="+state+"&code=identity-code", nil,
	))
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("callback status=%d body=%s", callback.Code, callback.Body)
	}

	cloudMu.Lock()
	claimCode, _ := cloudCompletion["claim_code"].(string)
	cloudMu.Unlock()
	if claimCode == "" {
		t.Fatal("Cloud completion did not receive the one-time claim code")
	}
	claimRequest, _ := json.Marshal(redeemClaimInput{
		Code: claimCode, SitePortalCode: "official", NodeID: "edge-1", TerminalSessionID: "session-1",
	})
	redeem := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/claims/redeem", bytes.NewReader(claimRequest))
	request.Header.Set("Content-Type", "application/json")
	portal.Handler().ServeHTTP(redeem, request)
	if redeem.Code != http.StatusOK || !strings.Contains(redeem.Body.String(), "sso-access-token") {
		t.Fatalf("redeem status=%d body=%s", redeem.Code, redeem.Body)
	}
}
