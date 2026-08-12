package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"fly-print-cloud/api/internal/config"

	"github.com/gin-gonic/gin"
)

func TestExternalOAuthCallbackRequiresLoginState(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer issuer.Close()

	handler := NewOAuth2Handler(&config.OAuth2Config{
		Mode:         "keycloak",
		ClientID:     "cloud",
		ClientSecret: "secret",
		AuthURL:      issuer.URL + "/authorize",
		TokenURL:     issuer.URL + "/token",
	}, &config.AdminConfig{ConsoleURL: "https://cloud.example.test"}, nil, nil, true)
	router := gin.New()
	router.GET("/auth/login", handler.Login)
	router.GET("/auth/callback", handler.Callback)

	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login status = %d, want %d", login.Code, http.StatusFound)
	}

	stateCookie := findCookie(login.Result().Cookies(), "flyprint_oauth_state")
	if stateCookie == nil || stateCookie.Value == "" {
		t.Fatal("login must set a short-lived OAuth state cookie")
	}
	if !stateCookie.HttpOnly || !stateCookie.Secure {
		t.Fatalf("OAuth state cookie must be HttpOnly and Secure, got %#v", stateCookie)
	}

	callback := httptest.NewRequest(http.MethodGet, "/auth/callback?code=attacker-code&state=wrong-state", nil)
	callback.AddCookie(stateCookie)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, callback)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("callback with mismatched state = %d, want %d", response.Code, http.StatusBadRequest)
	}

	redirect, err := url.Parse(login.Header().Get("Location"))
	if err != nil || redirect.Query().Get("state") == "" {
		t.Fatalf("login redirect must carry a state: %q", login.Header().Get("Location"))
	}
}

func TestExternalOAuthSessionCookiesFollowDeploymentSecurityMode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","token_type":"Bearer","expires_in":300,"refresh_token":"refresh","id_token":"id"}`))
	}))
	defer issuer.Close()

	for _, cookieSecure := range []bool{false, true} {
		t.Run(map[bool]string{false: "temporary_HTTP", true: "HTTPS"}[cookieSecure], func(t *testing.T) {
			handler := NewOAuth2Handler(&config.OAuth2Config{
				Mode:         "keycloak",
				ClientID:     "cloud",
				ClientSecret: "secret",
				AuthURL:      issuer.URL + "/authorize",
				TokenURL:     issuer.URL + "/token",
			}, &config.AdminConfig{ConsoleURL: "https://cloud.example.test"}, nil, nil, cookieSecure)
			router := gin.New()
			router.GET("/auth/callback", handler.Callback)

			request := httptest.NewRequest(http.MethodGet, "/auth/callback?code=valid-code&state=state", nil)
			request.AddCookie(&http.Cookie{Name: oauthStateCookieName, Value: "state"})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusFound {
				t.Fatalf("callback status = %d, want %d, body = %s", response.Code, http.StatusFound, response.Body.String())
			}
			accessCookie := findCookie(response.Result().Cookies(), "access_token")
			if accessCookie == nil || accessCookie.Secure != cookieSecure || !accessCookie.HttpOnly {
				t.Fatalf("access cookie = %#v, want secure=%t and HttpOnly", accessCookie, cookieSecure)
			}
		})
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
