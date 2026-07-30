package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginCallbackSetsOpaqueBrowserSessionCookie(t *testing.T) {
	now := time.Now().UTC()
	cloud := &fakeCloudBoundary{completeUser: "cloud-user-1"}
	identity := &fakeIdentityBoundary{result: identityResult{
		ExternalUserID: "external-user-1", DisplayName: "张老师",
		AccessToken: "private-prp-token", ExpiresAt: now.Add(5 * time.Minute),
	}}
	server := newPortalServer(testPortalConfig(), cloud, identity)
	state := server.loginStates.create(loginState{
		TerminalTicket: "raw-ticket",
		Context: terminalContext{
			SitePortalCode: "official", NodeID: "edge-1", TerminalSessionID: "session-1",
			ExpiresAt: now.Add(5 * time.Minute),
		},
		ExpiresAt: now.Add(5 * time.Minute),
	})

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(
		http.MethodGet, "/auth/callback?state="+state+"&code=identity-code", nil,
	))
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/files" {
		t.Fatalf("status=%d location=%q", recorder.Code, recorder.Header().Get("Location"))
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != userCookieName || !cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" ||
		cookie.Value == "private-prp-token" {
		t.Fatalf("cookie=%#v", cookie)
	}
	session, ok := server.userSessions.get(cookie.Value, now)
	if !ok || session.ExternalUserID != "external-user-1" ||
		session.DisplayName != "张老师" ||
		session.PRPBaseURL != testPortalConfig().PRPBaseURL ||
		session.AccessToken != "private-prp-token" {
		t.Fatalf("session=%#v ok=%v", session, ok)
	}
}

func TestBrowserSessionExpiryRemovesPRPCredential(t *testing.T) {
	store := newBrowserSessionStore()
	expiresAt := time.Unix(1200, 0).UTC()
	key := store.create(browserSession{
		ExternalUserID: "user-1", DisplayName: "User",
		PRPBaseURL: "https://prp.example.test", AccessToken: "private-prp-token",
		AccessTokenExpiresAt: time.Unix(1300, 0).UTC(), ExpiresAt: expiresAt,
	})

	if _, ok := store.get(key, expiresAt); ok {
		t.Fatal("expired browser session remained available")
	}
	if _, ok := store.get(key, time.Unix(1100, 0).UTC()); ok {
		t.Fatal("expired browser session credential was retained")
	}
	if store.count() != 0 {
		t.Fatalf("expired browser session count=%d", store.count())
	}
}
