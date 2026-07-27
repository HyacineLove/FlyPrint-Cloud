package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"fly-print-cloud/api/internal/business"
	"fly-print-cloud/api/internal/config"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/security"
	"fly-print-cloud/api/internal/storage"

	"github.com/gin-gonic/gin"
)

func newStorageFileHandler(
	t *testing.T,
	repo *fakeFileRepository,
) (*FileHandler, *fakeStorage) {
	t.Helper()
	store := newFakeStorage()
	return &FileHandler{
		repo:    repo,
		config:  &config.StorageConfig{UploadDir: t.TempDir(), MaxSize: 1024 * 1024},
		storage: store,
	}, store
}

func newPNGUploadRequest(t *testing.T) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "sample.png")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write(samplePNGBytes()); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/upload", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func newUploadTestRouter(handler *FileHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/upload", func(c *gin.Context) {
		c.Set("external_id", "user-1")
		handler.Upload(c)
	})
	return router
}

func TestUploadPolicyEndpointReturnsConfiguredLimits(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	handler := &FileHandler{
		config: &config.StorageConfig{
			MaxSize:          20 * 1024 * 1024,
			MaxDocumentPages: 8,
		},
	}

	router := gin.New()
	router.GET("/api/v1/files/upload-policy", handler.GetUploadPolicy)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/upload-policy", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"max_file_size_bytes":20971520`) {
		t.Fatalf("body = %s, want configured max size", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"max_pages":8`) {
		t.Fatalf("body = %s, want configured max pages", rec.Body.String())
	}
}

func TestUploadPolicyEndpointReturnsDynamicSettings(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	handler := &FileHandler{
		config: &config.StorageConfig{
			MaxSize:          20 * 1024 * 1024,
			MaxDocumentPages: 8,
		},
		settingsProvider: staticSettingsProvider{
			settings: business.Settings{
				UploadMaxSizeBytes:      1024,
				MaxDocumentPages:        2,
				UploadTokenTTLSeconds:   90,
				DownloadTokenTTLSeconds: 120,
				AllowedExtensions:       []string{".pdf"},
			},
		},
	}

	router := gin.New()
	router.GET("/api/v1/files/upload-policy", handler.GetUploadPolicy)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/upload-policy", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"max_file_size_bytes":1024`) {
		t.Fatalf("body = %s, want dynamic max size", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"max_pages":2`) {
		t.Fatalf("body = %s, want dynamic max pages", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"allowed_extensions":[".pdf"]`) {
		t.Fatalf("body = %s, want dynamic allowed extensions", rec.Body.String())
	}
}

func TestValidateUploadRulesUsesConfiguredMaxSize(t *testing.T) {
	t.Helper()

	tmpFile, err := os.CreateTemp(t.TempDir(), "upload-*.txt")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmpFile.Close()

	if _, err := tmpFile.Write([]byte("ab")); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	if _, err := tmpFile.Seek(0, 0); err != nil {
		t.Fatalf("seek temp file: %v", err)
	}

	handler := &FileHandler{
		config: &config.StorageConfig{
			MaxSize: 1,
		},
	}

	fileHeader := &multipart.FileHeader{
		Filename: "resume.txt",
		Size:     2,
	}

	if err := handler.validateUploadRules(fileHeader, tmpFile); err != errUploadTooLarge {
		t.Fatalf("expected errUploadTooLarge, got %v", err)
	}
}

func TestValidateUploadRulesUsesDynamicMaxSize(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "upload-*.png")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmpFile.Close()

	content := samplePNGBytes()
	if _, err := tmpFile.Write(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if _, err := tmpFile.Seek(0, 0); err != nil {
		t.Fatalf("seek temp file: %v", err)
	}

	handler := &FileHandler{
		config: &config.StorageConfig{
			MaxSize:          1024 * 1024,
			MaxDocumentPages: 5,
		},
		settingsProvider: staticSettingsProvider{
			settings: business.Settings{
				UploadMaxSizeBytes:      int64(len(content) - 1),
				MaxDocumentPages:        5,
				UploadTokenTTLSeconds:   90,
				DownloadTokenTTLSeconds: 120,
				AllowedExtensions:       []string{".png"},
			},
		},
	}

	fileHeader := &multipart.FileHeader{
		Filename: "sample.png",
		Size:     int64(len(content)),
	}

	if err := handler.validateUploadRules(fileHeader, tmpFile); err != errUploadTooLarge {
		t.Fatalf("expected errUploadTooLarge, got %v", err)
	}
}

func TestValidateUploadRulesUsesDynamicMaxDocumentPages(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "upload-*.docx")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmpFile.Close()

	if err := writeDOCXWithPageCount(tmpFile, 2); err != nil {
		t.Fatalf("write docx: %v", err)
	}
	if _, err := tmpFile.Seek(0, 0); err != nil {
		t.Fatalf("seek temp file: %v", err)
	}
	stat, err := tmpFile.Stat()
	if err != nil {
		t.Fatalf("stat temp file: %v", err)
	}

	handler := &FileHandler{
		config: &config.StorageConfig{
			MaxSize:          1024 * 1024,
			MaxDocumentPages: 5,
		},
		settingsProvider: staticSettingsProvider{
			settings: business.Settings{
				UploadMaxSizeBytes:      1024 * 1024,
				MaxDocumentPages:        1,
				UploadTokenTTLSeconds:   90,
				DownloadTokenTTLSeconds: 120,
				AllowedExtensions:       []string{".docx"},
			},
		},
	}

	fileHeader := &multipart.FileHeader{
		Filename: "sample.docx",
		Size:     stat.Size(),
	}

	if err := handler.validateUploadRules(fileHeader, tmpFile); err != errUploadTooManyPages {
		t.Fatalf("expected errUploadTooManyPages, got %v", err)
	}
}

func TestValidateUploadRulesUsesDynamicAllowedExtensions(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "upload-*.png")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmpFile.Close()

	if _, err := tmpFile.Write(samplePNGBytes()); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if _, err := tmpFile.Seek(0, 0); err != nil {
		t.Fatalf("seek temp file: %v", err)
	}

	handler := &FileHandler{
		config: &config.StorageConfig{
			MaxSize:          1024 * 1024,
			MaxDocumentPages: 5,
		},
		settingsProvider: staticSettingsProvider{
			settings: business.Settings{
				UploadMaxSizeBytes:      1024 * 1024,
				MaxDocumentPages:        5,
				UploadTokenTTLSeconds:   90,
				DownloadTokenTTLSeconds: 120,
				AllowedExtensions:       []string{".pdf"},
			},
		},
	}

	fileHeader := &multipart.FileHeader{
		Filename: "sample.png",
		Size:     int64(len(samplePNGBytes())),
	}

	if err := handler.validateUploadRules(fileHeader, tmpFile); err != errUploadInvalidType {
		t.Fatalf("expected errUploadInvalidType, got %v", err)
	}
}

func TestVerifyUploadEndpointRejectsDisabledPrinter(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	repo := &stubTokenRepo{records: map[string]*stubTokenRecord{}}
	tm := security.NewTokenManager("secret", 180, 180, repo)
	token, _, err := tm.GenerateUploadToken("node-1", "printer-1")
	if err != nil {
		t.Fatalf("GenerateUploadToken() error = %v", err)
	}

	handler := &FileHandler{
		config:       &config.StorageConfig{MaxSize: 10 * 1024 * 1024, MaxDocumentPages: 5},
		tokenManager: tm,
		edgeNodeRepo: &stubEdgeNodeRepo{nodes: map[string]*models.EdgeNode{
			"node-1": {ID: "node-1", Enabled: true},
		}},
		printerRepo: &stubPrinterRepo{printers: map[string]*models.Printer{
			"printer-1": {ID: "printer-1", EdgeNodeID: "node-1", Enabled: false},
		}},
	}

	router := gin.New()
	router.GET("/api/v1/files/verify-upload-token", handler.VerifyUploadToken)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/verify-upload-token?token="+url.QueryEscape(token)+"&node_id=node-1&printer_id=printer-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"error":"printer_disabled"`) {
		t.Fatalf("body = %s, want printer_disabled", rec.Body.String())
	}
}

func TestFileHandlerStorageUploadUsesStorageBackend(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	repo := &fakeFileRepository{}
	handler, store := newStorageFileHandler(t, repo)
	router := newUploadTestRouter(handler)
	req := newPNGUploadRequest(t)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("Upload() status = %d, want %d, body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if store.putCalls != 1 {
		t.Fatalf("storage.Put() calls = %d, want %d", store.putCalls, 1)
	}
	if repo.created == nil {
		t.Fatalf("repo.Create() was not called")
	}
	expectedHash := fmt.Sprintf("%x", sha256.Sum256(samplePNGBytes()))
	if repo.created.ContentHash != expectedHash {
		t.Fatalf("repo.Create() content_hash = %q, want %q", repo.created.ContentHash, expectedHash)
	}
	if repo.created.FilePath != store.lastPutKey {
		t.Fatalf("repo.Create() file path = %q, want %q", repo.created.FilePath, store.lastPutKey)
	}
	if got := store.objects[store.lastPutKey]; !bytes.Equal(got, samplePNGBytes()) {
		t.Fatalf("stored payload = %v, want %v", got, samplePNGBytes())
	}
}

func TestFileHandlerStorageUploadCleansUpStoredObjectWhenRepoCreateFails(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	repo := &fakeFileRepository{createErr: errors.New("db down")}
	handler, store := newStorageFileHandler(t, repo)
	router := newUploadTestRouter(handler)
	req := newPNGUploadRequest(t)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("Upload() status = %d, want %d, body = %s", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
	}
	if store.deleteCalls != 1 {
		t.Fatalf("storage.Delete() calls = %d, want %d", store.deleteCalls, 1)
	}
	if _, ok := store.objects[store.lastPutKey]; ok {
		t.Fatalf("stored object %q was not cleaned up", store.lastPutKey)
	}
}

func TestFileHandlerStorageDownloadStreamsFromStorage(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	payload := []byte("downloaded from storage")
	repo := &fakeFileRepository{
		fileByID: map[string]*models.File{
			"file-1": {
				ID:           "file-1",
				OriginalName: "report.pdf",
				FilePath:     "objects/report.pdf",
				MimeType:     "application/pdf",
				UploaderID:   "user-1",
			},
		},
	}
	handler, store := newStorageFileHandler(t, repo)
	store.objects["objects/report.pdf"] = payload

	router := gin.New()
	router.GET("/files/:id", func(c *gin.Context) {
		c.Set("external_id", "user-1")
		c.Set("roles", []string{"file:read"})
		handler.Download(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/files/file-1", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("Download() status = %d, want %d, body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if store.getCalls != 1 {
		t.Fatalf("storage.Get() calls = %d, want %d", store.getCalls, 1)
	}
	if got := recorder.Body.String(); got != string(payload) {
		t.Fatalf("Download() body = %q, want %q", got, string(payload))
	}
	if disposition := recorder.Header().Get("Content-Disposition"); !strings.Contains(disposition, "report.pdf") {
		t.Fatalf("Content-Disposition = %q, want filename report.pdf", disposition)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "application/pdf") {
		t.Fatalf("Content-Type = %q, want application/pdf", contentType)
	}
}

func TestFileHandlerStorageDownloadRejectsNonOwner(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	repo := &fakeFileRepository{fileByID: map[string]*models.File{
		"file-1": {ID: "file-1", OriginalName: "report.pdf", FilePath: "objects/report.pdf", MimeType: "application/pdf", UploaderID: "owner-1"},
	}}
	handler, store := newStorageFileHandler(t, repo)
	router := gin.New()
	router.GET("/files/:id", func(c *gin.Context) {
		c.Set("external_id", "other-user")
		c.Set("roles", []string{"file:read"})
		handler.Download(c)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/files/file-1", nil))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("Download() status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if store.getCalls != 0 {
		t.Fatalf("storage.Get() calls = %d, want 0", store.getCalls)
	}
}

type fakeFileRepository struct {
	created   *models.File
	createErr error
	fileByID  map[string]*models.File
}

func (r *fakeFileRepository) Create(file *models.File) error {
	if r.createErr != nil {
		return r.createErr
	}
	cloned := *file
	cloned.ID = "file-1"
	cloned.CreatedAt = time.Unix(1, 0)
	r.created = &cloned
	file.ID = cloned.ID
	file.CreatedAt = cloned.CreatedAt
	return nil
}

func (r *fakeFileRepository) GetByID(id string) (*models.File, error) {
	if r.fileByID == nil {
		return nil, nil
	}
	file, ok := r.fileByID[id]
	if !ok {
		return nil, nil
	}
	cloned := *file
	return &cloned, nil
}

type fakeStorage struct {
	objects       map[string][]byte
	putCalls      int
	getCalls      int
	deleteCalls   int
	lastPutKey    string
	lastDeleteKey string
	putErr        error
	getErr        error
	deleteErr     error
}

type stubEdgeNodeRepo struct {
	nodes map[string]*models.EdgeNode
}

func (r *stubEdgeNodeRepo) GetEdgeNodeByID(id string) (*models.EdgeNode, error) {
	node, ok := r.nodes[id]
	if !ok {
		return nil, nil
	}
	return node, nil
}

type stubPrinterRepo struct {
	printers map[string]*models.Printer
}

func (r *stubPrinterRepo) GetPrinterByID(id string) (*models.Printer, error) {
	printer, ok := r.printers[id]
	if !ok {
		return nil, nil
	}
	return printer, nil
}

type stubTokenRecord struct {
	revoked bool
	used    bool
}

type stubTokenRepo struct {
	records map[string]*stubTokenRecord
}

func (r *stubTokenRepo) PreRegisterToken(tokenHash, _, _, _, _ string, _ time.Time) error {
	if _, ok := r.records[tokenHash]; !ok {
		r.records[tokenHash] = &stubTokenRecord{}
	}
	return nil
}

func (r *stubTokenRepo) MarkTokenAsUsed(tokenHash, _, _, _, _ string, _ time.Time) error {
	record, ok := r.records[tokenHash]
	if !ok {
		record = &stubTokenRecord{}
		r.records[tokenHash] = record
	}
	if record.revoked {
		return errors.New("token has been revoked")
	}
	if record.used {
		return errors.New("token has already been used")
	}
	record.used = true
	return nil
}

func (r *stubTokenRepo) RevokeTokensByNodeAndResource(_, _, _ string) (int64, error) {
	return 0, nil
}

func (r *stubTokenRepo) GetTokenStatus(tokenHash string) (bool, bool, bool, error) {
	record, ok := r.records[tokenHash]
	if !ok {
		return false, false, false, nil
	}
	return record.used, record.revoked, true, nil
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{objects: make(map[string][]byte)}
}

func (s *fakeStorage) Put(_ context.Context, key string, reader io.Reader, opts storage.PutOptions) (*storage.ObjectInfo, error) {
	if s.putErr != nil {
		return nil, s.putErr
	}
	s.putCalls++
	s.lastPutKey = key
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	s.objects[key] = data
	return &storage.ObjectInfo{Key: key, Size: int64(len(data)), ContentType: opts.ContentType}, nil
}

func (s *fakeStorage) Get(_ context.Context, key string) (io.ReadCloser, *storage.ObjectInfo, error) {
	if s.getErr != nil {
		return nil, nil, s.getErr
	}
	s.getCalls++
	data, ok := s.objects[key]
	if !ok {
		return nil, nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), &storage.ObjectInfo{Key: key, Size: int64(len(data))}, nil
}

func (s *fakeStorage) Delete(_ context.Context, key string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleteCalls++
	s.lastDeleteKey = key
	delete(s.objects, key)
	return nil
}

func (s *fakeStorage) Stat(context.Context, string) (*storage.ObjectInfo, error) {
	return nil, errors.New("not implemented")
}

func (s *fakeStorage) GeneratePresignedGet(context.Context, string, time.Duration, string) (string, error) {
	return "", storage.ErrNotSupported
}

func samplePNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE,
	}
}

func writeDOCXWithPageCount(file *os.File, pages int) error {
	writer := zip.NewWriter(file)
	appXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties">
  <Pages>%d</Pages>
</Properties>`, pages)
	entry, err := writer.Create("docProps/app.xml")
	if err != nil {
		return err
	}
	if _, err := entry.Write([]byte(appXML)); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

type staticSettingsProvider struct {
	settings business.Settings
	err      error
}

func (p staticSettingsProvider) Current() (business.Settings, error) {
	if p.err != nil {
		return business.Settings{}, p.err
	}
	return p.settings, nil
}
