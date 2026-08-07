package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testConfiguration(authURL, tokenURL, userinfoURL string) configuration {
	return configuration{
		AuthorizationURL: authURL,
		TokenURL:         tokenURL,
		UserinfoURL:      userinfoURL,
		ClientID:         "uat-site-portal",
		ClientSecret:     strings.Repeat("s", 32),
		Scope:            "openid profile",
		RedirectURI:      "http://127.0.0.1:8090/callback",
		ListenAddress:    "127.0.0.1:8090",
		StateTTL:         5 * time.Minute,
		HTTPTimeout:      5 * time.Second,
	}
}

func TestLoginRedirectContainsNoClientSecret(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	server, err := newServer(testConfiguration(upstream.URL+"/authorize", upstream.URL+"/token", ""), upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/login", nil))
	if recorder.Code != http.StatusFound {
		t.Fatalf("status=%d", recorder.Code)
	}
	location, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Query().Get("state") == "" ||
		location.Query().Get("client_id") != "uat-site-portal" ||
		location.Query().Get("response_type") != "code" ||
		location.Query().Get("redirect_uri") != "http://127.0.0.1:8090/callback" ||
		strings.Contains(location.String(), strings.Repeat("s", 32)) {
		t.Fatalf("unsafe authorization URL: %s", location.String())
	}
}

func TestIndexUsesRelativeLoginLinkForSubpathDeployment(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	server, err := newServer(testConfiguration(upstream.URL+"/authorize", upstream.URL+"/token", ""), upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/fly-print-site-portal/", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `href="login"`) {
		t.Fatalf("subpath-compatible index missing relative login link: status=%d body=%s", recorder.Code, recorder.Body)
	}
}

func TestCallbackExchangesCodeAndOnlyReturnsTokenConfirmation(t *testing.T) {
	var tokenRequest, userinfoRequest *http.Request
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequest = r
			if err := r.ParseForm(); err != nil ||
				r.Form.Get("grant_type") != "authorization_code" ||
				r.Form.Get("client_id") != "uat-site-portal" ||
				r.Form.Get("client_secret") != strings.Repeat("s", 32) ||
				r.Form.Get("redirect_uri") != "http://127.0.0.1:8090/callback" ||
				r.Form.Get("code") != "one-time-code" {
				http.Error(w, "invalid token request", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"access_token\":\"opaque-test-token\",\"token_type\":\"Bearer\",\"expires_in\":300}"))
		case "/userinfo":
			userinfoRequest = r
			if r.Header.Get("Authorization") != "Bearer opaque-test-token" {
				http.Error(w, "invalid bearer", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"sub\":\"uat-user-1\",\"preferred_username\":\"site-portal-test\",\"email\":\"site-portal-test@example.com\",\"given_name\":\"Site\",\"family_name\":\"Portal Test\",\"name\":\"Site Portal Test\",\"active\":true,\"client_id\":\"uat-site-portal\"}"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	config := testConfiguration(upstream.URL+"/authorize", upstream.URL+"/token", upstream.URL+"/userinfo")
	server, err := newServer(config, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}

	login := httptest.NewRecorder()
	server.Handler().ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login", nil))
	location, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := location.Query().Get("state")
	callback := httptest.NewRecorder()
	server.Handler().ServeHTTP(callback, httptest.NewRequest(
		http.MethodGet, "/callback?state="+url.QueryEscape(state)+"&code=one-time-code", nil,
	))
	if callback.Code != http.StatusOK || tokenRequest == nil || userinfoRequest == nil {
		t.Fatalf("status=%d body=%s token_request=%v userinfo_request=%v", callback.Code, callback.Body, tokenRequest != nil, userinfoRequest != nil)
	}
	if strings.Contains(callback.Body.String(), "opaque-test-token") ||
		strings.Contains(callback.Body.String(), strings.Repeat("s", 32)) ||
		strings.Contains(callback.Body.String(), "one-time-code") {
		t.Fatalf("sensitive value leaked: %s", callback.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(callback.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["token_received"] != true || result["userinfo_checked"] != true ||
		result["subject_present"] != true || result["profile_active"] != true {
		t.Fatalf("unexpected verification result: %#v", result)
	}

	replay := httptest.NewRecorder()
	server.Handler().ServeHTTP(replay, httptest.NewRequest(
		http.MethodGet, "/callback?state="+url.QueryEscape(state)+"&code=one-time-code", nil,
	))
	if replay.Code != http.StatusGone {
		t.Fatalf("state replay status=%d body=%s", replay.Code, replay.Body)
	}

	htmlLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(htmlLogin, httptest.NewRequest(http.MethodGet, "/login", nil))
	htmlLocation, err := url.Parse(htmlLogin.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	htmlCallbackRequest := httptest.NewRequest(
		http.MethodGet, "/callback?state="+url.QueryEscape(htmlLocation.Query().Get("state"))+"&code=one-time-code", nil,
	)
	htmlCallbackRequest.Header.Set("Accept", "text/html")
	htmlCallback := httptest.NewRecorder()
	server.Handler().ServeHTTP(htmlCallback, htmlCallbackRequest)
	if htmlCallback.Code != http.StatusOK ||
		!strings.Contains(htmlCallback.Body.String(), "site-portal-test@example.com") ||
		!strings.Contains(htmlCallback.Body.String(), "Site Portal Test") ||
		strings.Contains(htmlCallback.Body.String(), "opaque-test-token") ||
		strings.Contains(htmlCallback.Body.String(), "one-time-code") {
		t.Fatalf("unexpected HTML verification page: status=%d body=%s", htmlCallback.Code, htmlCallback.Body)
	}
}

func TestVerificationPageEscapesProfileClaims(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeVerificationPage(recorder, verificationResult{
		LoginSuccess: true,
		Profile:      userProfileView{Email: "<script>alert(1)</script>"},
	})
	if strings.Contains(recorder.Body.String(), "<script>alert(1)</script>") ||
		!strings.Contains(recorder.Body.String(), "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("profile claim was not HTML escaped: %s", recorder.Body)
	}
}

func TestConfigurationRejectsNonHTTPSSSOEndpoint(t *testing.T) {
	config := testConfiguration("http://sso-uat.example.test/authorize", "https://sso-uat.example.test/token", "")
	if err := config.validate(); err == nil {
		t.Fatal("expected non-HTTPS SSO endpoint to be rejected")
	}
}
