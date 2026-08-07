package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIdentityClientAcceptsStandardOIDCUserInfo(t *testing.T) {
	identityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil ||
				r.Form.Get("grant_type") != "authorization_code" ||
				r.Form.Get("client_id") != "fly" ||
				r.Form.Get("client_secret") != "sso-client-secret" ||
				r.Form.Get("redirect_uri") != "https://portal.example.test/auth/callback" ||
				r.Form.Get("code") != "authorization-code" {
				http.Error(w, "invalid token request", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "opaque-access-token", "token_type": "Bearer", "expires_in": 300,
			})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer opaque-access-token" {
				http.Error(w, "invalid access token", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub": "keycloak-user-1", "preferred_username": "site-portal-test",
				"email": "site-portal-test@example.test", "given_name": "Site", "family_name": "Portal",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer identityServer.Close()

	client := &identityClient{
		tokenURL: identityServer.URL + "/token", userinfoURL: identityServer.URL + "/userinfo",
		clientID: "fly", clientSecret: "sso-client-secret", redirectURI: "https://portal.example.test/auth/callback",
		profileFormat: "oidc", client: identityServer.Client(),
	}
	result, err := client.exchangeCode("authorization-code")
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalUserID != "keycloak-user-1" || result.DisplayName != "Site Portal" ||
		result.AccessToken != "opaque-access-token" || !result.ExpiresAt.After(time.Now()) {
		t.Fatalf("unexpected identity result: %#v", result)
	}
	if strings.Contains(result.DisplayName, "site-portal-test@example.test") {
		t.Fatalf("email should not be preferred over name: %#v", result)
	}
}
