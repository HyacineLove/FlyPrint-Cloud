package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeCloudBoundary struct {
	context       terminalContext
	contextErr    error
	completeUser  string
	completeErr   error
	completion    loginCompletion
	completeCalls int
}

func (f *fakeCloudBoundary) validateContext(_ string) (terminalContext, error) {
	return f.context, f.contextErr
}

func (f *fakeCloudBoundary) completeLogin(input loginCompletion) (string, error) {
	f.completeCalls++
	f.completion = input
	return f.completeUser, f.completeErr
}

type fakeIdentityBoundary struct {
	result         identityResult
	err            error
	opsResponse    []byte
	opsStatus      int
	opsErr         error
	opsMethod      string
	opsPath        string
	opsToken       string
	opsRequestBody []byte
}

func (f *fakeIdentityBoundary) exchangeCode(_ string) (identityResult, error) {
	return f.result, f.err
}

func (f *fakeIdentityBoundary) opsLogin(_, _ string) (identityOpsSession, error) {
	return identityOpsSession{}, errors.New("not used")
}

func (f *fakeIdentityBoundary) opsRequest(method, path, token string, body []byte) ([]byte, int, error) {
	f.opsMethod = method
	f.opsPath = path
	f.opsToken = token
	f.opsRequestBody = append([]byte(nil), body...)
	return f.opsResponse, f.opsStatus, f.opsErr
}

func testPortalConfig() configuration {
	return configuration{
		Code:                   "official",
		DisplayName:            "FlyPrint",
		IdentityBrowserBaseURL: "https://identity.example.test",
		IdentityCallbackURL:    "https://portal.example.test/auth/callback",
		PRPBaseURL:             "https://prp.example.test",
		LoginStateTTL:          5 * time.Minute,
		ClaimTTL:               5 * time.Minute,
		OpsSessionTTL:          time.Hour,
	}
}

func TestSuccessfulLoginCreatesClaimThenReportsCloud(t *testing.T) {
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
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	if cloud.completeCalls != 1 || cloud.completion.ExternalUserID != "external-user-1" {
		t.Fatalf("unexpected Cloud completion: %#v", cloud.completion)
	}
	serialized, err := json.Marshal(cloud.completion)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte("private-prp-token")) || bytes.Contains(serialized, []byte("access_token")) {
		t.Fatalf("PRP credential entered Cloud request: %s", serialized)
	}
	if server.claims.count() != 1 {
		t.Fatalf("claim count=%d", server.claims.count())
	}

	claimCode := cloud.completion.ClaimCode
	body, _ := json.Marshal(redeemClaimInput{
		Code: claimCode, SitePortalCode: "official", NodeID: "edge-1", TerminalSessionID: "session-1",
	})
	claimRecorder := httptest.NewRecorder()
	claimRequest := httptest.NewRequest(http.MethodPost, "/api/claims/redeem", bytes.NewReader(body))
	claimRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(claimRecorder, claimRequest)
	if claimRecorder.Code != http.StatusOK || !strings.Contains(claimRecorder.Body.String(), "private-prp-token") {
		t.Fatalf("claim status=%d body=%s", claimRecorder.Code, claimRecorder.Body)
	}
}

func TestCloudRejectionDeletesUnpublishedClaim(t *testing.T) {
	now := time.Now().UTC()
	cloud := &fakeCloudBoundary{completeErr: errors.New("rejected")}
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
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	if server.claims.count() != 0 {
		t.Fatalf("rejected claim count=%d", server.claims.count())
	}
}

func TestEntryRejectsTerminalContextThatCloudDoesNotValidate(t *testing.T) {
	server := newPortalServer(testPortalConfig(),
		&fakeCloudBoundary{contextErr: errors.New("invalid terminal")},
		&fakeIdentityBoundary{},
	)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/entry?terminal_ticket=bad", nil))
	if recorder.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	if server.loginStates.count() != 0 {
		t.Fatalf("invalid entry created login state: count=%d", server.loginStates.count())
	}
}

func TestOpsDeleteUserProxiesToIdentityService(t *testing.T) {
	identity := &fakeIdentityBoundary{
		opsResponse: []byte(`{"success":true}`),
		opsStatus:   http.StatusOK,
	}
	server := newPortalServer(testPortalConfig(), &fakeCloudBoundary{}, identity)
	localToken := server.opsSessions.create(localOpsSession{
		IdentityToken: "identity-ops-token",
		ExpiresAt:     time.Now().Add(time.Hour),
	})
	request := httptest.NewRequest(http.MethodDelete, "/api/ops/users/user-1", nil)
	request.AddCookie(&http.Cookie{Name: opsCookieName, Value: localToken})
	recorder := httptest.NewRecorder()

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"success":true}` {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	if identity.opsMethod != http.MethodDelete ||
		identity.opsPath != "/api/ops/users/user-1" ||
		identity.opsToken != "identity-ops-token" ||
		len(identity.opsRequestBody) != 0 {
		t.Fatalf("unexpected identity request: method=%q path=%q token=%q body=%q",
			identity.opsMethod, identity.opsPath, identity.opsToken, identity.opsRequestBody)
	}
}
