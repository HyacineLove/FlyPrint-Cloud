package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testIdentityConfig(t *testing.T) configuration {
	t.Helper()
	return configuration{
		DataFile:            filepath.Join(t.TempDir(), "state.json"),
		OperatorUsername:    "ops",
		OperatorPassword:    "OperatorPass123!",
		ClientSecret:        "12345678901234567890123456789012",
		AllowedRedirectURIs: []string{"https://portal.example.test/auth/callback"},
		CodeTTL:             time.Minute,
		AccessTokenTTL:      5 * time.Minute,
		OpsSessionTTL:       time.Hour,
	}
}

func postIdentityJSON(t *testing.T, handler http.Handler, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestInitialOperatorIsCreatedOnlyOnce(t *testing.T) {
	config := testIdentityConfig(t)
	first, err := newServer(config)
	if err != nil {
		t.Fatal(err)
	}
	if !first.store.authenticateOperator("ops", "OperatorPass123!") {
		t.Fatal("initial operator password was not accepted")
	}

	config.OperatorPassword = "ChangedEnvironmentPass123!"
	second, err := newServer(config)
	if err != nil {
		t.Fatal(err)
	}
	if !second.store.authenticateOperator("ops", "OperatorPass123!") {
		t.Fatal("persisted operator password should remain valid")
	}
	if second.store.authenticateOperator("ops", "ChangedEnvironmentPass123!") {
		t.Fatal("restart must not replace an initialized operator password")
	}
}

func TestPublicRegistrationRouteDoesNotExist(t *testing.T) {
	server, err := newServer(testIdentityConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	recorder := postIdentityJSON(t, server.Handler(), "/register", map[string]string{
		"username": "new-user",
	}, "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
}

func TestAuthorizationCodeCanBeExchangedOnlyOnce(t *testing.T) {
	server, err := newServer(testIdentityConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	code := server.codes.issue(codeGrant{
		ExternalUserID: "user-1", DisplayName: "用户一",
	}, time.Now().Add(time.Minute))

	first := postIdentityJSON(t, server.Handler(), "/api/token", map[string]string{"code": code},
		"12345678901234567890123456789012")
	second := postIdentityJSON(t, server.Handler(), "/api/token", map[string]string{"code": code},
		"12345678901234567890123456789012")
	if first.Code != http.StatusOK || second.Code != http.StatusConflict {
		t.Fatalf("statuses=%d,%d first=%s second=%s", first.Code, second.Code, first.Body, second.Body)
	}
	var response map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["external_user_id"] != "user-1" || response["access_token"] == "" {
		t.Fatalf("unexpected token response: %#v", response)
	}
}

func TestOpsCreateAndResetResponsesNeverContainPasswordOrHash(t *testing.T) {
	server, err := newServer(testIdentityConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	login := postIdentityJSON(t, server.Handler(), "/api/ops/login", map[string]string{
		"username": "ops", "password": "OperatorPass123!",
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("ops login status=%d body=%s", login.Code, login.Body)
	}
	var loginResponse map[string]string
	if err := json.Unmarshal(login.Body.Bytes(), &loginResponse); err != nil {
		t.Fatal(err)
	}

	create := postIdentityJSON(t, server.Handler(), "/api/ops/users", map[string]string{
		"username": "teacher", "display_name": "张老师", "password": "TeacherPass123!",
	}, loginResponse["session_token"])
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	user := created["user"].(map[string]any)
	reset := postIdentityJSON(t, server.Handler(), "/api/ops/users/"+user["id"].(string)+"/reset-password",
		map[string]string{"new_password": "ReplacementPass123!"}, loginResponse["session_token"])
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", reset.Code, reset.Body)
	}
	for _, raw := range [][]byte{create.Body.Bytes(), reset.Body.Bytes()} {
		lower := bytes.ToLower(raw)
		for _, forbidden := range [][]byte{[]byte("password"), []byte("hash"), []byte("TeacherPass123"), []byte("ReplacementPass123")} {
			if bytes.Contains(lower, bytes.ToLower(forbidden)) {
				t.Fatalf("sensitive value %q leaked in %s", forbidden, raw)
			}
		}
	}
}

func TestOpsDeleteRemovesUserAndPreventsLogin(t *testing.T) {
	server, err := newServer(testIdentityConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	login := postIdentityJSON(t, server.Handler(), "/api/ops/login", map[string]string{
		"username": "ops", "password": "OperatorPass123!",
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("ops login status=%d body=%s", login.Code, login.Body)
	}
	var loginResponse map[string]string
	if err := json.Unmarshal(login.Body.Bytes(), &loginResponse); err != nil {
		t.Fatal(err)
	}

	create := postIdentityJSON(t, server.Handler(), "/api/ops/users", map[string]string{
		"username": "teacher", "display_name": "张老师", "password": "TeacherPass123!",
	}, loginResponse["session_token"])
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	user := created["user"].(map[string]any)

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/ops/users/"+user["id"].(string), nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+loginResponse["session_token"])
	deleted := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body)
	}
	lower := bytes.ToLower(deleted.Body.Bytes())
	for _, forbidden := range [][]byte{
		[]byte("password"), []byte("hash"), []byte("token"), []byte("TeacherPass123!"),
	} {
		if bytes.Contains(lower, bytes.ToLower(forbidden)) {
			t.Fatalf("sensitive value %q leaked in %s", forbidden, deleted.Body.Bytes())
		}
	}

	form := url.Values{
		"username":     {"teacher"},
		"password":     {"TeacherPass123!"},
		"redirect_uri": {"https://portal.example.test/auth/callback"},
		"state":        {"state-1"},
	}
	loginRequest := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	userLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(userLogin, loginRequest)
	if userLogin.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user login status=%d body=%s", userLogin.Code, userLogin.Body)
	}
}
