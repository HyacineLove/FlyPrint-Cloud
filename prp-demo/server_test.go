package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
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
	}
}

func TestHealthReturnsOK(t *testing.T) {
	server := newServer(testConfiguration(t), testTokenVerifier())
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
}
