package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type server struct {
	config         configuration
	verifier       tokenVerifier
	uploadContexts *uploadContextStore
	files          *fileStore
	now            func() time.Time
	cleanupStop    chan struct{}
	cleanupDone    chan struct{}
}

func newServer(config configuration, verifier tokenVerifier) (*server, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	files, err := openFileStore(config)
	if err != nil {
		return nil, err
	}
	result := &server{
		config:         config,
		verifier:       verifier,
		uploadContexts: newUploadContextStore(),
		files:          files,
		now:            time.Now,
		cleanupStop:    make(chan struct{}),
		cleanupDone:    make(chan struct{}),
	}
	go result.runCleanup()
	return result, nil
}

func (s *server) close() error {
	close(s.cleanupStop)
	<-s.cleanupDone
	return s.files.close()
}

func (s *server) runCleanup() {
	defer close(s.cleanupDone)
	ticker := time.NewTicker(s.config.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = s.files.cleanup(context.Background(), s.now().UTC())
		case <-s.cleanupStop:
			return
		}
	}
}

func (s *server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/v1/upload-contexts", s.handleCreateUploadContext)
	mux.Handle("POST /api/v1/files", s.uploadCORS(http.HandlerFunc(s.handleUploadFile)))
	mux.Handle("OPTIONS /api/v1/files", s.uploadCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("GET /api/v1/files", s.handleListFiles)
	mux.HandleFunc("GET /api/v1/files/{id}/content", s.handleDownloadFile)
	return securityHeaders(mux)
}

func (s *server) handleCreateUploadContext(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authorizePRP(w, r, "upload-context:create")
	if !ok {
		return
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.config.UploadContextTTL)
	if claims.ExpiresAt.Before(expiresAt) {
		expiresAt = claims.ExpiresAt
	}
	raw, err := s.uploadContexts.create(claims.Subject, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"upload_context": raw,
		"expires_at":     expiresAt,
		"upload_url":     s.config.PublicBaseURL + "/api/v1/files",
	})
}

func (s *server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	raw, ok := bearerCredential(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "auth_required")
		return
	}
	context, err := s.uploadContexts.consume(raw, s.now().UTC())
	if errors.Is(err, errUploadContextExpired) {
		writeError(w, http.StatusUnauthorized, "upload_context_expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, "upload_context_invalid")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.config.MaxFileSizeBytes+1024*1024)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		writeError(w, http.StatusBadRequest, "invalid_upload")
		return
	}
	item, err := s.files.uploadFile(
		r.Context(), context.Subject, part.FileName(), part.Header.Get("Content-Type"), part, s.now().UTC(),
	)
	_ = part.Close()
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	if extra, nextErr := reader.NextPart(); nextErr != io.EOF {
		if extra != nil {
			_ = extra.Close()
		}
		if deleteErr := s.files.delete(r.Context(), context.Subject, item.ID); deleteErr != nil {
			writeError(w, http.StatusInternalServerError, "internal_error")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_upload")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]fileItem{"file": item})
}

func (s *server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authorizePRP(w, r, "files:list")
	if !ok {
		return
	}
	if err := s.files.cleanup(r.Context(), s.now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	page, pageSize, ok := parsePagination(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_pagination")
		return
	}
	result, err := s.files.list(r.Context(), claims.Subject, page, pageSize)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authorizePRP(w, r, "files:download")
	if !ok {
		return
	}
	if err := s.files.cleanup(r.Context(), s.now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	stored, file, err := s.files.openDownload(r.Context(), claims.Subject, r.PathValue("id"))
	if errors.Is(err, errFileNotFound) {
		writeError(w, http.StatusNotFound, "file_not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	defer file.Close()

	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": stored.Name})
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Type", stored.MediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(stored.Size, 10))
	w.Header().Set("X-Content-SHA256", stored.SHA256)
	w.WriteHeader(http.StatusOK)
	written, copyErr := io.Copy(w, file)
	if copyErr == nil && written == stored.Size {
		_ = s.files.markDownloaded(r.Context(), claims.Subject, stored.ID, s.now().UTC())
	}
}

func (s *server) authorizePRP(w http.ResponseWriter, r *http.Request, scope string) (accessClaims, bool) {
	raw, ok := bearerCredential(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "auth_required")
		return accessClaims{}, false
	}
	claims, err := s.verifier.verify(raw, scope, s.now().UTC())
	if errors.Is(err, errTokenExpired) {
		writeError(w, http.StatusUnauthorized, "token_expired")
		return accessClaims{}, false
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, "token_invalid")
		return accessClaims{}, false
	}
	return claims, true
}

func bearerCredential(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	return raw, raw != ""
}

func parsePagination(r *http.Request) (int, int, bool) {
	page, pageSize := 1, 20
	var err error
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, false
		}
	}
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, false
		}
	}
	return page, pageSize, page >= 1 && pageSize >= 1 && pageSize <= 50
}

func (s *server) uploadCORS(next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(s.config.AllowedUploadOrigins))
	for _, origin := range s.config.AllowedUploadOrigins {
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if _, exists := allowed[origin]; origin != "" && exists {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Add("Vary", "Origin")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) writeFileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errUnsupportedFileType):
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_file_type")
	case errors.Is(err, errFileTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large")
	case errors.Is(err, errStorageCapacity):
		writeError(w, http.StatusInsufficientStorage, "storage_capacity_exceeded")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code},
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
