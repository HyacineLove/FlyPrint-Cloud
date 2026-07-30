package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfiguration(t *testing.T) configuration {
	t.Helper()
	dataDir := t.TempDir()
	return configuration{
		DataDir:              dataDir,
		DatabaseFile:         filepath.Join(dataDir, "prp.db"),
		TokenSecret:          testTokenSecret,
		TokenIssuer:          "flyprint-sso-demo",
		TokenAudience:        "flyprint-prp-demo",
		SitePortalCode:       "official",
		AllowedUploadOrigins: []string{"https://portal.example.test"},
		PublicBaseURL:        "https://prp.example.test",
		MaxFileSizeBytes:     50 * 1024 * 1024,
		FileTTL:              7 * 24 * time.Hour,
		UploadContextTTL:     5 * time.Minute,
	}
}

func TestHealthReturnsOK(t *testing.T) {
	server, err := newServer(testConfiguration(t), testTokenVerifier())
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
}

func TestUploadCORSReflectsOnlyConfiguredOrigin(t *testing.T) {
	server, err := newServer(testConfiguration(t), testTokenVerifier())
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	handler := server.Handler()

	allowedRequest := httptest.NewRequest(http.MethodOptions, "/api/v1/files", nil)
	allowedRequest.Header.Set("Origin", "https://portal.example.test")
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusNoContent ||
		allowed.Header().Get("Access-Control-Allow-Origin") != "https://portal.example.test" ||
		allowed.Header().Get("Access-Control-Allow-Origin") == "*" {
		t.Fatalf("allowed status=%d headers=%v", allowed.Code, allowed.Header())
	}

	disallowedRequest := httptest.NewRequest(http.MethodOptions, "/api/v1/files", nil)
	disallowedRequest.Header.Set("Origin", "https://other.example.test")
	disallowed := httptest.NewRecorder()
	handler.ServeHTTP(disallowed, disallowedRequest)
	if disallowed.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin reflected: %v", disallowed.Header())
	}

	nearMatchRequest := httptest.NewRequest(http.MethodOptions, "/api/v1/files", nil)
	nearMatchRequest.Header.Set("Origin", "https://portal.example.test/")
	nearMatch := httptest.NewRecorder()
	handler.ServeHTTP(nearMatch, nearMatchRequest)
	if nearMatch.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("non-exact origin reflected: %v", nearMatch.Header())
	}
}

func TestListRejectsInvalidPagination(t *testing.T) {
	server, err := newServer(testConfiguration(t), testTokenVerifier())
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	server.now = func() time.Time { return time.Unix(1100, 0).UTC() }

	recorder := performJSONRequest(
		t,
		server.Handler(),
		http.MethodGet,
		"/api/v1/files?page=0&page_size=51",
		buildLiteralToken(t, validLiteralClaims()),
		nil,
	)
	if recorder.Code != http.StatusBadRequest ||
		!strings.Contains(recorder.Body.String(), `"code":"invalid_pagination"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
