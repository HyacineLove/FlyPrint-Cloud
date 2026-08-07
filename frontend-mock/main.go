// Command frontend-mock serves the Cloud frontends with an in-memory API.
// It is intentionally development-only: no database, queues, files or production
// authentication are involved. Bind is loopback-only by default.
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type mockState struct {
	mu          sync.RWMutex
	nodes       []map[string]any
	printers    []map[string]any
	jobs        []map[string]any
	users       []map[string]any
	contacts    []map[string]any
	sitePortals []map[string]any
	clients     []map[string]any
	settings    map[string]any
}

type server struct {
	adminBuild string
	state      *mockState
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func newMockState() *mockState {
	t := time.Now().UTC()
	stamp := func(delta time.Duration) string { return t.Add(delta).Format(time.RFC3339) }
	return &mockState{
		nodes: []map[string]any{
			{"id": "node-demo-001", "name": "大厅自助终端", "alias": "大厅 01", "location": "一楼大厅", "connection_status": "online", "health_status": "healthy", "health_message": "心跳正常", "enabled": true, "last_heartbeat": stamp(-20 * time.Second), "version": "1.0.52", "registration_state": "active", "site_portal_codes": []string{"official"}, "default_code": "official", "ops_contact_count": 2, "printer_count": 1, "job_count": 4},
			{"id": "node-demo-002", "name": "二楼服务台", "alias": "二楼 01", "location": "二楼服务台", "connection_status": "unstable", "health_status": "degraded", "health_message": "最近心跳延迟", "enabled": true, "last_heartbeat": stamp(-2 * time.Minute), "version": "1.0.49", "registration_state": "active", "site_portal_codes": []string{"official"}, "default_code": "official", "ops_contact_count": 1, "printer_count": 1, "job_count": 2},
		},
		printers: []map[string]any{
			{"id": "printer-demo-001", "name": "HP LaserJet M404", "display_name": "大厅黑白打印机", "model": "HP LaserJet Pro M404", "printer_status": "idle", "enabled": true, "edge_node_id": "node-demo-001", "job_count": 4},
			{"id": "printer-demo-002", "name": "Canon iR-ADV", "display_name": "二楼彩色打印机", "model": "Canon imageRUNNER ADVANCE", "printer_status": "printing", "enabled": true, "edge_node_id": "node-demo-002", "job_count": 2},
		},
		jobs: []map[string]any{
			{"id": "job-demo-001", "name": "会议材料.pdf", "user_email": "alice@example.com", "user_name": "Alice", "edge_node_id": "node-demo-001", "node_name": "大厅 01", "printer_id": "printer-demo-001", "printer_name": "大厅黑白打印机", "copies": 2, "page_count": 6, "paper_size": "A4", "color_mode": "monochrome", "duplex_mode": "longedge", "quota_reserved": 12, "quota_consumed": 12, "status": "completed", "created_at": stamp(-12 * time.Minute), "end_time": stamp(-10 * time.Minute), "site_portal_code": "official"},
			{"id": "job-demo-002", "name": "报销单.pdf", "user_email": "bob@example.com", "user_name": "Bob", "edge_node_id": "node-demo-002", "node_name": "二楼 01", "printer_id": "printer-demo-002", "printer_name": "二楼彩色打印机", "copies": 1, "page_count": 3, "paper_size": "A4", "color_mode": "color", "duplex_mode": "longedge", "quota_reserved": 6, "quota_consumed": "-", "status": "processing", "created_at": stamp(-2 * time.Minute), "site_portal_code": "official"},
			{"id": "job-demo-003", "name": "申请表.pdf", "user_email": "carol@example.com", "user_name": "Carol", "edge_node_id": "node-demo-001", "node_name": "大厅 01", "printer_id": "printer-demo-001", "printer_name": "大厅黑白打印机", "copies": 1, "page_count": 2, "paper_size": "A4", "color_mode": "monochrome", "duplex_mode": "simplex", "quota_reserved": 2, "quota_consumed": "-", "status": "pending", "created_at": stamp(-30 * time.Second), "site_portal_code": "official"},
			{"id": "job-demo-004", "name": "合同扫描件.pdf", "user_email": "dave@example.com", "user_name": "Dave", "edge_node_id": "node-demo-002", "node_name": "二楼 01", "printer_id": "printer-demo-002", "printer_name": "二楼彩色打印机", "copies": 1, "page_count": 8, "paper_size": "A4", "color_mode": "color", "duplex_mode": "longedge", "quota_reserved": 16, "quota_consumed": 0, "status": "failed", "error_message": "打印机缺纸", "created_at": stamp(-50 * time.Minute), "end_time": stamp(-48 * time.Minute), "site_portal_code": "official"},
		},
		users: []map[string]any{
			{"id": "user-demo-001", "username": "alice", "email": "alice@example.com", "role": "operator", "status": "active", "last_login": stamp(-2 * time.Hour), "created_at": stamp(-120 * 24 * time.Hour), "print_quota_balance": 86},
			{"id": "user-demo-002", "username": "bob", "email": "bob@example.com", "role": "viewer", "status": "active", "last_login": stamp(-24 * time.Hour), "created_at": stamp(-60 * 24 * time.Hour), "print_quota_balance": 24},
			{"id": "user-demo-003", "username": "carol", "email": "carol@example.com", "role": "admin", "status": "inactive", "last_login": stamp(-30 * 24 * time.Hour), "created_at": stamp(-365 * 24 * time.Hour), "print_quota_balance": 0},
		},
		contacts: []map[string]any{
			{"id": "contact-demo-001", "name": "张工", "phone": "138****8001", "enabled": true, "node_ids": []string{"node-demo-001"}},
			{"id": "contact-demo-002", "name": "李工", "phone": "139****8002", "enabled": true, "node_ids": []string{"node-demo-001", "node-demo-002"}},
		},
		sitePortals: []map[string]any{
			{"code": "official", "display_name": "官方站点入口（演示）", "entry_url": "http://127.0.0.1:8099/site-portal/entry", "claim_base_url": "http://127.0.0.1:8099/site-portal", "enabled": true, "oauth_client_id": "flyprint-demo-client", "oauth_client_enabled": true, "edge_node_count": 2},
		},
		clients: []map[string]any{
			{"id": "oauth-demo-001", "client_id": "flyprint-demo-client", "client_type": "site_portal", "site_portal_code": "official", "allowed_scopes": []string{"site-portal:access"}, "description": "演示 Site Portal 客户端", "enabled": true},
		},
		settings: map[string]any{
			"upload_max_size_bytes":      10485760,
			"max_document_pages":         20,
			"upload_token_ttl_seconds":   180,
			"download_token_ttl_seconds": 180,
			"allowed_extensions":         []string{".pdf", ".docx", ".png", ".jpg"},
			"max_contacts_per_node":      5,
		},
	}
}

func locateAdminBuild() string {
	if configured := os.Getenv("FRONTEND_MOCK_ADMIN_BUILD"); configured != "" {
		return configured
	}
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, "admin", "build"),
		filepath.Join(cwd, "..", "admin", "build"),
		filepath.Join(cwd, "fly-print-cloud", "admin", "build"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil {
			return candidate
		}
	}
	return candidates[0]
}

func envelope(data any) map[string]any {
	return map[string]any{"code": 200, "data": data, "message": "success"}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func readJSON(r *http.Request) map[string]any {
	var value map[string]any
	if r.Body == nil {
		return value
	}
	_ = json.NewDecoder(r.Body).Decode(&value)
	return value
}

func page(items []map[string]any, query url.Values) (any, map[string]any) {
	p, _ := strconv.Atoi(query.Get("page"))
	if p < 1 {
		p = 1
	}
	ps, _ := strconv.Atoi(query.Get("page_size"))
	if ps < 1 {
		ps = 20
	}
	start := (p - 1) * ps
	if start > len(items) {
		start = len(items)
	}
	end := start + ps
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], map[string]any{"page": p, "page_size": ps, "total": len(items)}
}

func pagedData(items []map[string]any, query url.Values) map[string]any {
	values, pagination := page(items, query)
	return map[string]any{"items": values, "pagination": pagination}
}

func (s *server) auth(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/auth/mode":
		writeJSON(w, http.StatusOK, map[string]any{"mode": "builtin"})
	case "/auth/me":
		writeJSON(w, http.StatusOK, envelope(map[string]any{"user_id": "mock-admin", "username": "admin", "preferred_username": "admin", "email": "admin@flyprint.local", "access_token": "mock-admin-token", "expires_in": 86400}))
	case "/auth/token":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"access_token": "mock-admin-token", "token_type": "bearer", "expires_in": 86400})
	case "/auth/register":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"access_token": "mock-admin-token", "token_type": "bearer", "expires_in": 86400})
	case "/auth/logout":
		writeJSON(w, http.StatusOK, envelope(nil))
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "mock auth route not found"})
	}
}

func (s *server) api(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	if strings.HasPrefix(path, "files") {
		s.files(w, r, path)
		return
	}
	if !strings.HasPrefix(path, "admin/") {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "mock API route not found"})
		return
	}
	s.adminAPI(w, r, strings.TrimPrefix(path, "admin/"))
}

func (s *server) files(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case path == "files/upload-policy" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, envelope(map[string]any{"max_file_size_bytes": 10485760, "max_pages": 20, "allowed_extensions": []string{".pdf", ".docx", ".png", ".jpg"}, "allowed_mime_types": []string{"application/pdf", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "image/png", "image/jpeg"}}))
	case path == "files/verify-upload-token" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"code": 200, "valid": true, "data": map[string]any{"expires_at": time.Now().Add(10 * time.Minute).Unix(), "node_id": "node-demo-001", "printer_id": "printer-demo-001"}, "message": "success"})
	case path == "files/preflight" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusOK, envelope(map[string]any{"valid": true, "page_count": 2, "file_size": 12800}))
	case path == "files" && r.Method == http.MethodPost:
		_, _ = io.Copy(io.Discard, r.Body)
		writeJSON(w, http.StatusCreated, envelope(map[string]any{"job_id": "job-mock-upload", "status": "pending", "message": "mock upload accepted"}))
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "mock file route not found"})
	}
}

func (s *server) adminAPI(w http.ResponseWriter, r *http.Request, path string) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	switch {
	case path == "dashboard/trends":
		writeJSON(w, http.StatusOK, envelope(map[string]any{"buckets": trendBuckets(r.URL.Query().Get("period"))}))
	case path == "dashboard/maintenance":
		writeJSON(w, http.StatusOK, envelope(map[string]any{"items": maintenanceItems(), "total": 2, "page": 1, "page_size": 20, "summary": map[string]any{"fault_nodes": 0, "online_nodes": 2, "total_nodes": len(s.state.nodes), "fault_printers": 1, "online_printers": 2, "total_printers": len(s.state.printers)}}))
	case path == "alerts/history":
		writeJSON(w, http.StatusOK, envelope(map[string]any{"items": maintenanceItems(), "total": 2, "page": 1, "page_size": 10}))
	case path == "edge-nodes" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, envelope(pagedData(s.state.nodes, r.URL.Query())))
	case path == "edge-nodes/activations" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusCreated, envelope(map[string]any{"activation_code": "MOCK-2026-8099", "expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339)}))
	case strings.HasPrefix(path, "edge-nodes/"):
		s.edgeNodeMutation(w, r, strings.TrimPrefix(path, "edge-nodes/"))
	case path == "printers" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, envelope(pagedData(s.state.printers, r.URL.Query())))
	case strings.HasPrefix(path, "printers/"):
		s.simpleMutation(w, r, &s.state.printers, strings.TrimPrefix(path, "printers/"))
	case path == "print-jobs" && r.Method == http.MethodGet:
		values, pagination := page(s.state.jobs, r.URL.Query())
		writeJSON(w, http.StatusOK, map[string]any{"jobs": values, "pagination": pagination})
	case path == "ops-contacts" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, envelope(pagedData(s.state.contacts, r.URL.Query())))
	case strings.HasPrefix(path, "ops-contacts"):
		s.collectionMutation(w, r, &s.state.contacts, strings.TrimPrefix(path, "ops-contacts"), "contact")
	case path == "users" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, envelope(pagedData(s.state.users, r.URL.Query())))
	case strings.HasPrefix(path, "users"):
		s.collectionMutation(w, r, &s.state.users, strings.TrimPrefix(path, "users"), "user")
	case path == "site-portals" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, envelope(s.state.sitePortals))
	case strings.HasPrefix(path, "site-portals"):
		s.collectionMutation(w, r, &s.state.sitePortals, strings.TrimPrefix(path, "site-portals"), "site_portal")
	case path == "site-portals" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, envelope(s.state.sitePortals))
	case strings.HasPrefix(path, "site-portals"):
		s.sitePortalMutation(w, r, strings.TrimPrefix(path, "site-portals"))
	case path == "oauth2-clients" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, envelope(s.state.clients))
	case strings.HasPrefix(path, "oauth2-clients"):
		s.collectionMutation(w, r, &s.state.clients, strings.TrimPrefix(path, "oauth2-clients"), "client")
	case path == "business-settings" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, envelope(s.state.settings))
	case path == "business-settings" && r.Method == http.MethodPut:
		for key, value := range readJSON(r) {
			s.state.settings[key] = value
		}
		writeJSON(w, http.StatusOK, envelope(s.state.settings))
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "mock admin route not found"})
	}
}

func (s *server) sitePortalMutation(w http.ResponseWriter, r *http.Request, suffix string) {
	suffix = strings.Trim(suffix, "/")
	if suffix == "" && r.Method == http.MethodPost {
		payload := readJSON(r)
		code, _ := payload["code"].(string)
		if code == "" {
			code = fmt.Sprintf("portal-%d", time.Now().Unix())
		}
		payload["code"] = code
		payload["enabled"] = true
		payload["oauth_client_id"] = "flyprint-" + code
		s.state.sitePortals = append(s.state.sitePortals, payload)
		writeJSON(w, http.StatusCreated, envelope(map[string]any{"code": code, "client_id": "flyprint-" + code, "client_secret": "mock-site-secret"}))
		return
	}
	parts := strings.Split(suffix, "/")
	code := parts[0]
	for _, portal := range s.state.sitePortals {
		if portal["code"] != code {
			continue
		}
		if len(parts) == 2 && parts[1] == "enabled" {
			portal["enabled"] = readJSON(r)["enabled"]
			writeJSON(w, http.StatusOK, envelope(portal))
			return
		}
		if len(parts) == 2 && parts[1] == "rotate-secret" {
			writeJSON(w, http.StatusOK, envelope(map[string]any{"client_id": portal["oauth_client_id"], "client_secret": "mock-site-secret-rotated"}))
			return
		}
		writeJSON(w, http.StatusOK, envelope(portal))
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "mock site portal not found"})
}

func (s *server) sitePortalConfig(node map[string]any) map[string]any {
	codes, _ := node["site_portal_codes"].([]string)
	portals := make([]map[string]any, 0, len(codes))
	for _, code := range codes {
		for _, portal := range s.state.sitePortals {
			if portal["code"] == code {
				portals = append(portals, portal)
				break
			}
		}
	}
	return map[string]any{
		"edge_node_id": node["id"],
		"portals":      portals,
		"default_code": node["default_code"],
	}
}

func (s *server) edgeNodeMutation(w http.ResponseWriter, r *http.Request, suffix string) {
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	if len(parts) < 1 {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "node not found"})
		return
	}
	id := parts[0]
	for _, node := range s.state.nodes {
		if node["id"] != id {
			continue
		}
		switch {
		case len(parts) == 2 && parts[1] == "alias":
			node["alias"] = readJSON(r)["alias"]
		case len(parts) == 2 && parts[1] == "enabled":
			node["enabled"] = readJSON(r)["enabled"]
		case len(parts) == 2 && parts[1] == "site-portals":
			if r.Method == http.MethodGet {
				writeJSON(w, http.StatusOK, envelope(s.sitePortalConfig(node)))
				return
			}
			payload := readJSON(r)
			codes := make([]string, 0)
			for _, raw := range payload["portal_codes"].([]any) {
				if code, ok := raw.(string); ok {
					codes = append(codes, code)
				}
			}
			node["site_portal_codes"] = codes
			node["default_code"] = payload["default_code"]
			writeJSON(w, http.StatusOK, envelope(s.sitePortalConfig(node)))
			return
		case r.Method == http.MethodDelete:
			deleteByID(&s.state.nodes, id)
		default:
			writeJSON(w, http.StatusOK, envelope(node))
			return
		}
		writeJSON(w, http.StatusOK, envelope(node))
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "mock node not found"})
}

func (s *server) simpleMutation(w http.ResponseWriter, r *http.Request, items *[]map[string]any, suffix string) {
	id := strings.Trim(strings.Split(suffix, "/")[0], " ")
	for _, item := range *items {
		if item["id"] != id {
			continue
		}
		if r.Method == http.MethodDelete {
			deleteByID(items, id)
			writeJSON(w, http.StatusOK, envelope(nil))
			return
		}
		for key, value := range readJSON(r) {
			item[key] = value
		}
		writeJSON(w, http.StatusOK, envelope(item))
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "mock resource not found"})
}

func (s *server) collectionMutation(w http.ResponseWriter, r *http.Request, items *[]map[string]any, suffix, kind string) {
	suffix = strings.Trim(suffix, "/")
	if suffix == "" && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
		payload := readJSON(r)
		payload["id"] = fmt.Sprintf("%s-mock-%d", kind, time.Now().UnixNano())
		if kind == "user" {
			payload["status"] = "active"
			payload["print_quota_balance"] = 0
		}
		*items = append(*items, payload)
		writeJSON(w, http.StatusCreated, envelope(payload))
		return
	}
	id := strings.Split(suffix, "/")[0]
	for _, item := range *items {
		if item["id"] != id && item["code"] != id && item["client_id"] != id {
			continue
		}
		if r.Method == http.MethodDelete {
			deleteByID(items, id)
			writeJSON(w, http.StatusOK, envelope(nil))
			return
		}
		payload := readJSON(r)
		for key, value := range payload {
			item[key] = value
		}
		if strings.HasSuffix(suffix, "/enabled") {
			item["enabled"] = payload["enabled"]
		}
		writeJSON(w, http.StatusOK, envelope(item))
		return
	}
	writeJSON(w, http.StatusOK, envelope(map[string]any{"id": id, "enabled": true}))
}

func deleteByID(items *[]map[string]any, id string) {
	filtered := (*items)[:0]
	for _, item := range *items {
		if item["id"] != id {
			filtered = append(filtered, item)
		}
	}
	*items = filtered
}

func trendBuckets(period string) []map[string]any {
	count := 8
	if period == "month" {
		count = 12
	}
	if period == "year" {
		count = 6
	}
	items := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		items = append(items, map[string]any{"label": fmt.Sprintf("%s-%02d", period, i+1), "completed": 4 + i%4, "failed": i % 3})
	}
	return items
}

func maintenanceItems() []map[string]any {
	t := now()
	return []map[string]any{
		{"id": "alert-demo-001", "resource_type": "printer", "resource_id": "printer-demo-002", "node_id": "node-demo-002", "node_name": "二楼 01", "printer_id": "printer-demo-002", "printer_name": "二楼彩色打印机", "title": "打印中任务等待设备确认", "status": "open", "first_seen_at": t, "last_seen_at": t},
		{"id": "alert-demo-002", "resource_type": "node", "resource_id": "node-demo-002", "node_id": "node-demo-002", "node_name": "二楼 01", "title": "节点心跳延迟", "status": "resolved", "first_seen_at": t, "last_seen_at": t, "resolved_at": t, "duration_seconds": 92},
	}
}

func (s *server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "fly-print-frontend-mock"})
	})
	mux.HandleFunc("/__mock", func(w http.ResponseWriter, _ *http.Request) { writeMockLanding(w) })
	mux.HandleFunc("/site-portal/", sitePortal)
	mux.HandleFunc("/sso/", sso)
	mux.HandleFunc("/auth/", s.auth)
	mux.HandleFunc("/api/v1/", s.api)
	mux.HandleFunc("/", s.adminPage)
	return withHeaders(mux)
}

func withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		next.ServeHTTP(w, r)
	})
}

func (s *server) adminPage(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/static/") {
		s.serveAsset(w, r)
		return
	}
	if strings.Contains(filepath.Base(r.URL.Path), ".") && r.URL.Path != "/asset-manifest.json" {
		s.serveAsset(w, r)
		return
	}
	index := filepath.Join(s.adminBuild, "index.html")
	if _, err := os.Stat(index); err != nil {
		writePage(w, http.StatusServiceUnavailable, "Mock backend waiting for Admin build", `<p>先构建 Admin：<code>cd admin && npm run build</code>，再重新打开此地址。</p>`)
		return
	}
	http.ServeFile(w, r, index)
}

func (s *server) serveAsset(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(filepath.Clean(r.URL.Path), string(filepath.Separator))
	file := filepath.Join(s.adminBuild, filepath.FromSlash(path))
	rel, err := filepath.Rel(s.adminBuild, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(file); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, file)
}

const mockCSS = `
:root{font-family:Inter,"Microsoft YaHei",system-ui,sans-serif;color:#172033;background:#f4f7fb}*{box-sizing:border-box}body{margin:0;background:#f4f7fb}.mock-shell{min-height:100vh;padding:32px;background:linear-gradient(135deg,#f4f7fb 0%,#eaf2ff 100%)}.mock-card{max-width:1080px;margin:0 auto;background:#fff;border:1px solid #e1e9f4;border-radius:20px;box-shadow:0 18px 50px rgba(15,42,76,.11);padding:32px}.mock-brand{display:flex;align-items:center;gap:14px;color:#0b1f3a;font-size:24px;font-weight:750}.mock-mark{width:42px;height:42px;border-radius:13px;background:linear-gradient(135deg,#1268e8,#3d8bfd);color:#fff;display:grid;place-items:center;font-weight:800}.mock-muted{color:#697386}.mock-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:16px;margin-top:24px}.mock-link{display:block;padding:20px;border:1px solid #e1e9f4;border-radius:16px;color:#172033;text-decoration:none;transition:.2s}.mock-link:hover{border-color:#3d8bfd;box-shadow:0 8px 24px rgba(18,104,232,.12);transform:translateY(-2px)}.mock-button{display:inline-block;background:#1268e8;color:#fff;border:0;border-radius:10px;padding:11px 18px;text-decoration:none;cursor:pointer}.mock-button.secondary{background:#edf4ff;color:#1268e8}.mock-input{width:100%;border:1px solid #d6e1ef;border-radius:10px;padding:12px;font:inherit;margin:6px 0 14px}.mock-status{padding:12px 14px;border-radius:10px;background:#effaf2;color:#287d3c}.mock-table{width:100%;border-collapse:collapse;margin-top:20px}.mock-table th,.mock-table td{text-align:left;padding:13px;border-bottom:1px solid #edf1f6}.mock-pill{display:inline-block;padding:4px 9px;border-radius:99px;background:#eaf2ff;color:#1268e8;font-size:12px}@media(max-width:640px){.mock-shell{padding:16px}.mock-card{padding:22px}}
`

func writePage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>%s</style></head><body>%s</body></html>`, html.EscapeString(title), mockCSS, body)
}

func writeMockLanding(w http.ResponseWriter) {
	body := `<main class="mock-shell"><section class="mock-card"><div class="mock-brand"><span class="mock-mark">FP</span>FlyPrint 前端视觉验收</div><p class="mock-muted">这是独立的内存 mock 服务，只用于查看前端页面和交互，不连接真实 Cloud、Edge、Site Portal 或数据库。</p><div class="mock-grid"><a class="mock-link" href="/login"><b>Cloud Admin</b><br><span class="mock-muted">管理端登录及仪表盘、节点、打印机、任务、用户、业务设置</span></a><a class="mock-link" href="/site-portal/entry?terminal_ticket=mock-ticket"><b>Site Portal 入口</b><br><span class="mock-muted">站点入口、登录跳转和终端上下文</span></a><a class="mock-link" href="/site-portal/files"><b>Site Portal 文件页</b><br><span class="mock-muted">文件选择、上传状态和打印参数</span></a><a class="mock-link" href="/site-portal/ops"><b>Site Portal 运维页</b><br><span class="mock-muted">运维联系人和终端状态</span></a><a class="mock-link" href="/sso/login?redirect_uri=/site-portal/files"><b>SSO 登录页</b><br><span class="mock-muted">统一登录表单及回跳体验</span></a></div><p style="margin-top:28px"><span class="mock-pill">账号：admin@flyprint.local</span> <span class="mock-pill">密码：任意非空内容</span></p></section></main>`
	writePage(w, http.StatusOK, "FlyPrint 前端视觉验收", body)
}

func sitePortal(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/site-portal/entry" || r.URL.Path == "/site-portal/":
		body := `<main class="mock-shell"><section class="mock-card"><div class="mock-brand"><span class="mock-mark">FP</span>FlyPrint <span class="mock-pill">Site Portal</span></div><h1>登录后即可继续打印</h1><p class="mock-muted">当前终端：大厅自助终端 · 大厅黑白打印机</p><div class="mock-status">终端连接正常，打印机空闲，可开始选择文件。</div><p style="margin-top:24px"><a class="mock-button" href="/sso/login?redirect_uri=/site-portal/files">进入统一登录</a> <a class="mock-button secondary" href="/__mock">返回入口</a></p></section></main>`
		writePage(w, http.StatusOK, "Site Portal 入口", body)
	case r.URL.Path == "/site-portal/auth/callback":
		http.Redirect(w, r, "/site-portal/files", http.StatusFound)
	case r.URL.Path == "/site-portal/files":
		body := `<main class="mock-shell"><section class="mock-card"><div class="mock-brand"><span class="mock-mark">FP</span>文件打印 <span class="mock-pill">Site Portal</span></div><p class="mock-muted">当前用户：alice@example.com · 终端：大厅 01 · 额度余额：86 点</p><label>选择文件<input class="mock-input" id="file" type="file" accept=".pdf,.docx,.png,.jpg"></label><div class="mock-grid"><div><label>打印份数<input class="mock-input" type="number" value="1" min="1"></label></div><div><label>色彩<select class="mock-input"><option>黑白</option><option>彩色</option></select></label></div><div><label>单双面<select class="mock-input"><option>单面</option><option>双面（长边）</option></select></label></div></div><p id="status" class="mock-status">请选择文件后开始预览。</p><button class="mock-button" id="print" type="button">提交打印</button> <a class="mock-button secondary" href="/__mock">返回入口</a><script>const f=document.getElementById('file'),s=document.getElementById('status'),b=document.getElementById('print');f.onchange=()=>s.textContent=f.files[0]?'已选择：'+f.files[0].name+'（mock 预览通过）':'请选择文件后开始预览。';b.onclick=()=>s.textContent='mock 任务已提交：等待中 → 打印中 → 已完成';</script></section></main>`
		writePage(w, http.StatusOK, "Site Portal 文件打印", body)
	case r.URL.Path == "/site-portal/ops":
		body := `<main class="mock-shell"><section class="mock-card"><div class="mock-brand"><span class="mock-mark">FP</span>运维台 <span class="mock-pill">Site Portal</span></div><p class="mock-muted">仅展示页面样式和状态，不会发送真实通知。</p><table class="mock-table"><thead><tr><th>联系人</th><th>电话</th><th>绑定终端</th><th>状态</th></tr></thead><tbody><tr><td>张工</td><td>138****8001</td><td>大厅 01</td><td><span class="mock-pill">启用</span></td></tr><tr><td>李工</td><td>139****8002</td><td>大厅 01、二楼 01</td><td><span class="mock-pill">启用</span></td></tr></tbody></table><p style="margin-top:24px"><a class="mock-button secondary" href="/__mock">返回入口</a></p></section></main>`
		writePage(w, http.StatusOK, "Site Portal 运维台", body)
	default:
		writePage(w, http.StatusNotFound, "Site Portal", `<main class="mock-shell"><section class="mock-card"><h1>Site Portal 页面不存在</h1><a class="mock-button" href="/__mock">返回入口</a></section></main>`)
	}
}

func sso(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/sso/login" && r.URL.Path != "/sso/" {
		writePage(w, http.StatusNotFound, "SSO", `<main class="mock-shell"><section class="mock-card"><h1>SSO 页面不存在</h1><a class="mock-button" href="/__mock">返回入口</a></section></main>`)
		return
	}
	redirect := r.URL.Query().Get("redirect_uri")
	if redirect == "" || !strings.HasPrefix(redirect, "/") {
		redirect = "/site-portal/files"
	}
	body := fmt.Sprintf(`<main class="mock-shell"><section class="mock-card" style="max-width:480px"><div class="mock-brand"><span class="mock-mark">FP</span>统一身份认证</div><p class="mock-muted">SSO mock 登录 · 不校验真实密码</p><form id="login"><label>邮箱<input class="mock-input" name="email" type="email" value="alice@example.com" required></label><label>密码<input class="mock-input" name="password" type="password" value="mock-password" required></label><button class="mock-button" type="submit">登录并继续</button> <a class="mock-button secondary" href="/__mock">取消</a></form><p id="message" class="mock-status" style="margin-top:18px">演示账号：alice@example.com / 任意非空密码</p><script>document.getElementById('login').onsubmit=(e)=>{e.preventDefault();document.getElementById('message').textContent='登录成功，正在返回站点…';setTimeout(()=>location.href=%q,250)};</script></section></main>`, redirect)
	writePage(w, http.StatusOK, "SSO 登录", body)
}

func main() {
	addr := os.Getenv("FRONTEND_MOCK_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8099"
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") && !strings.HasPrefix(addr, "[::1]:") {
		log.Fatalf("frontend-mock 只允许监听回环地址，当前配置为 %s", addr)
	}
	s := &server{adminBuild: locateAdminBuild(), state: newMockState()}
	log.Printf("frontend-mock listening at http://%s (Admin build: %s)", addr, s.adminBuild)
	log.Printf("open http://%s/__mock for the frontend links", addr)
	log.Fatal(http.ListenAndServe(addr, s.Handler()))
}
