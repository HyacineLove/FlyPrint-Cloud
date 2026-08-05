package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testServer(t *testing.T) *server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>admin-mock</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &server{adminBuild: dir, state: newMockState()}
}

func TestMockFrontendRoutes(t *testing.T) {
	s := testServer(t)
	tests := []struct {
		name string
		path string
		want string
	}{
		{"health", "/health", `"status":"ok"`},
		{"auth", "/auth/me", `"access_token":"mock-admin-token"`},
		{"nodes", "/api/v1/admin/edge-nodes?page=1&page_size=20", `"node-demo-001"`},
		{"jobs", "/api/v1/admin/print-jobs?page=1&page_size=20", `"job-demo-001"`},
		{"landing", "/__mock", "Site Portal"},
		{"site portal", "/site-portal/files", "提交打印"},
		{"sso", "/sso/login?redirect_uri=/site-portal/files", "统一身份认证"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), tc.want) {
				t.Fatalf("body does not contain %q: %s", tc.want, res.Body.String())
			}
		})
	}
}

func TestMockAdminMutationsStayInMemory(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/edge-nodes/node-demo-001/alias", strings.NewReader(`{"alias":"验收终端"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}

	get := httptest.NewRecorder()
	s.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/admin/edge-nodes?page=1&page_size=20", nil))
	var payload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload.Data.Items[0]["alias"]; got != "验收终端" {
		t.Fatalf("alias = %v", got)
	}
}
