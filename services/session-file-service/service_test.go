package main

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const testPDF = "%PDF-1.4\n%%EOF\n"

var testNonceCounter uint64

func signedRequest(t *testing.T, method, target, secret, contentType string, body []byte, now time.Time) *http.Request {
	t.Helper()
	timestamp := fmt.Sprintf("%d", now.Unix())
	nonce := fmt.Sprintf("%032x", atomic.AddUint64(&testNonceCounter, 1))
	message := timestamp + "\n" + nonce + "\n" + string(body) + "\n" + secret
	sum := md5.Sum([]byte(message))
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Nonce", nonce)
	request.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(sum[:]))
	return request
}

func jsonRequest(t *testing.T, target, secret string, input any, now time.Time) *http.Request {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return signedRequest(t, http.MethodPost, target, secret, "application/json", body, now)
}

func uploadRequest(t *testing.T, target, secret, ownerID, name, mediaType string, content []byte, now time.Time) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("user_id", ownerID)
	header := make(textprotoMIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, name))
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header.Header())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return signedRequest(t, http.MethodPost, target, secret, writer.FormDataContentType(), body.Bytes(), now)
}

// textprotoMIMEHeader keeps the multipart test helper readable without
// coupling production code to a test-only request builder.
type textprotoMIMEHeader map[string][]string

func (h textprotoMIMEHeader) Set(key, value string)       { h[key] = []string{value} }
func (h textprotoMIMEHeader) Header() map[string][]string { return h }

func TestSessionLifecycleUploadListDownloadAndDelete(t *testing.T) {
	now := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)
	store := newMemoryObjectStore()
	service := newSessionFileService(serviceConfig{
		SharedSecret: "0123456789abcdef0123456789abcdef", MaxTTL: time.Hour,
		MaxFileSize: 1 << 20, MaxFiles: 3, MaxTotalBytes: 2 << 20, ClockSkew: 5 * time.Minute,
	}, store)
	service.now = func() time.Time { return now }
	handler := service.handler()
	secret, ownerID := service.config.SharedSecret, "session-owner-1"

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, jsonRequest(t, "/api/v1/sessions/create", secret, map[string]any{
		"user_id": ownerID, "expires_at": now.Add(30 * time.Minute),
	}, now))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}

	upload := httptest.NewRecorder()
	handler.ServeHTTP(upload, uploadRequest(t, "/api/v1/sessions/upload", secret, ownerID, "report.pdf", "application/pdf", []byte(testPDF), now))
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", upload.Code, upload.Body)
	}
	var uploaded struct {
		Result struct {
			FileID string `json:"file_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(upload.Body.Bytes(), &uploaded); err != nil || uploaded.Result.FileID == "" {
		t.Fatalf("upload response=%s err=%v", upload.Body, err)
	}

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, jsonRequest(t, "/api/v1/files/list", secret, map[string]any{
		"user_id": ownerID, "page": 1, "page_size": 20,
	}, now))
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"name":"report.pdf"`)) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body)
	}

	download := httptest.NewRecorder()
	handler.ServeHTTP(download, jsonRequest(t, "/api/v1/files/download", secret, map[string]string{
		"user_id": ownerID, "file_id": uploaded.Result.FileID,
	}, now))
	if download.Code != http.StatusOK || download.Body.String() != testPDF {
		t.Fatalf("download status=%d body=%q", download.Code, download.Body.String())
	}
	if download.Header().Get("Content-Digest") == "" || download.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download headers=%v", download.Header())
	}

	otherCreate := httptest.NewRecorder()
	handler.ServeHTTP(otherCreate, jsonRequest(t, "/api/v1/sessions/create", secret, map[string]any{
		"user_id": "session-owner-2", "expires_at": now.Add(30 * time.Minute),
	}, now))
	crossOwner := httptest.NewRecorder()
	handler.ServeHTTP(crossOwner, jsonRequest(t, "/api/v1/files/download", secret, map[string]string{
		"user_id": "session-owner-2", "file_id": uploaded.Result.FileID,
	}, now))
	if crossOwner.Code != http.StatusNotFound {
		t.Fatalf("cross-owner download status=%d body=%s", crossOwner.Code, crossOwner.Body)
	}

	remove := httptest.NewRecorder()
	handler.ServeHTTP(remove, jsonRequest(t, "/api/v1/sessions/delete", secret, map[string]string{"user_id": ownerID}, now))
	if remove.Code != http.StatusNoContent || store.count() != 1 {
		t.Fatalf("delete status=%d objects=%d body=%s", remove.Code, store.count(), remove.Body)
	}
}

func TestSignedRequestStillRequiresAllowedCallerIP(t *testing.T) {
	now := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)
	_, allowed, _ := net.ParseCIDR("10.0.0.0/8")
	_, trusted, _ := net.ParseCIDR("192.0.2.0/24")
	service := newSessionFileService(serviceConfig{
		SharedSecret: "0123456789abcdef0123456789abcdef", MaxTTL: time.Hour,
		MaxFileSize: 1 << 20, MaxFiles: 3, MaxTotalBytes: 2 << 20, ClockSkew: 5 * time.Minute,
		AllowedCIDRs: []*net.IPNet{allowed}, TrustedProxyCIDRs: []*net.IPNet{trusted},
	}, newMemoryObjectStore())
	service.now = func() time.Time { return now }
	request := jsonRequest(t, "/api/v1/sessions/create", service.config.SharedSecret, map[string]any{
		"user_id": "session-owner-1", "expires_at": now.Add(time.Minute),
	}, now)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.8")
	recorder := httptest.NewRecorder()
	service.handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
}

func TestExpiredSessionIsDeletedFromObjectStorage(t *testing.T) {
	now := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)
	store := newMemoryObjectStore()
	service := newSessionFileService(serviceConfig{
		SharedSecret: "0123456789abcdef0123456789abcdef", MaxTTL: time.Hour,
		MaxFileSize: 1 << 20, MaxFiles: 3, MaxTotalBytes: 2 << 20, ClockSkew: 5 * time.Minute,
	}, store)
	service.now = func() time.Time { return now }
	handler := service.handler()
	secret := service.config.SharedSecret
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, jsonRequest(t, "/api/v1/sessions/create", secret, map[string]any{
		"user_id": "expiring-owner", "expires_at": now.Add(time.Minute),
	}, now))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}
	service.now = func() time.Time { return now.Add(2 * time.Minute) }
	if err := service.pruneExpired(t.Context()); err != nil {
		t.Fatal(err)
	}
	if store.count() != 0 {
		t.Fatalf("expired objects=%d, want 0", store.count())
	}
}

func TestUnsignedRequestIsRejected(t *testing.T) {
	service := newSessionFileService(serviceConfig{
		SharedSecret: "0123456789abcdef0123456789abcdef", MaxTTL: time.Hour,
		MaxFileSize: 1 << 20, MaxFiles: 3, MaxTotalBytes: 2 << 20, ClockSkew: 5 * time.Minute,
	}, newMemoryObjectStore())
	recorder := httptest.NewRecorder()
	service.handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/files/list", bytes.NewReader([]byte(`{}`))))
	if recorder.Code != http.StatusUnauthorized {
		body, _ := io.ReadAll(recorder.Result().Body)
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
}
