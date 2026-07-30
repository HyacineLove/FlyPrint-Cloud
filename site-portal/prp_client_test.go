package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPRPClientUsesAuthorizationHeaderAndReturnsOnlyUploadContext(t *testing.T) {
	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/upload-contexts" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer private-prp-token" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		if strings.Contains(r.URL.String(), "private-prp-token") {
			t.Fatalf("credential entered URL: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"upload_context": "one-use-context",
			"expires_at":     expiresAt,
			"upload_url":     "https://prp.example.test/api/v1/files",
		})
	}))
	defer upstream.Close()

	client := &prpClient{baseURL: upstream.URL, client: upstream.Client()}
	result, err := client.createUploadContext("private-prp-token")
	if err != nil {
		t.Fatal(err)
	}
	if result.UploadContext != "one-use-context" ||
		!result.ExpiresAt.Equal(expiresAt) ||
		result.UploadURL != "https://prp.example.test/api/v1/files" {
		t.Fatalf("result=%#v", result)
	}
}
