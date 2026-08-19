package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionProviderID = "session-upload"

type serviceConfig struct {
	SharedSecret      string
	MaxTTL            time.Duration
	MaxFileSize       int64
	MaxFiles          int
	MaxTotalBytes     int64
	ClockSkew         time.Duration
	AllowedCIDRs      []*net.IPNet
	TrustedProxyCIDRs []*net.IPNet
}

type sessionFileService struct {
	config serviceConfig
	store  objectStore
	now    func() time.Time
	mu     sync.Mutex
	nonces map[string]time.Time
}

type sessionMetadata struct {
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type fileMetadata struct {
	FileID           string     `json:"file_id"`
	Name             string     `json:"name"`
	MediaType        string     `json:"media_type"`
	Size             int64      `json:"size"`
	SHA256           string     `json:"sha256"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	LastDownloadedAt *time.Time `json:"last_downloaded_at"`
}

func newSessionFileService(config serviceConfig, store objectStore) *sessionFileService {
	return &sessionFileService{config: config, store: store, now: time.Now, nonces: make(map[string]time.Time)}
}

func (s *sessionFileService) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("POST /api/v1/sessions/create", s.signed(http.HandlerFunc(s.createSession)))
	mux.Handle("POST /api/v1/sessions/upload", s.signed(http.HandlerFunc(s.uploadFile)))
	mux.Handle("POST /api/v1/sessions/delete", s.signed(http.HandlerFunc(s.deleteSession)))
	mux.Handle("POST /api/v1/files/list", s.signed(http.HandlerFunc(s.listFiles)))
	mux.Handle("POST /api/v1/files/download", s.signed(http.HandlerFunc(s.downloadFile)))
	return mux
}

func (s *sessionFileService) signed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requestIPAllowed(r) {
			writeEnvelope(w, http.StatusForbidden, "forbidden", nil)
			return
		}
		limit := s.config.MaxFileSize + (2 << 20)
		if limit < 2<<20 {
			limit = 2 << 20
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			writeEnvelope(w, http.StatusUnauthorized, "unauthorized", nil)
			return
		}
		if int64(len(body)) > limit {
			writeEnvelope(w, http.StatusRequestEntityTooLarge, "file_too_large", nil)
			return
		}
		if !s.verifySignature(r, body) {
			writeEnvelope(w, http.StatusUnauthorized, "unauthorized", nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func (s *sessionFileService) requestIPAllowed(r *http.Request) bool {
	if len(s.config.AllowedCIDRs) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	remoteIP := net.ParseIP(host)
	if remoteIP == nil {
		return false
	}
	requestIP := remoteIP
	if ipInCIDRs(remoteIP, s.config.TrustedProxyCIDRs) {
		forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
		if parsed := net.ParseIP(forwarded); parsed != nil {
			requestIP = parsed
		}
	}
	return ipInCIDRs(requestIP, s.config.AllowedCIDRs)
}

func ipInCIDRs(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *sessionFileService) verifySignature(r *http.Request, body []byte) bool {
	timestampRaw := strings.TrimSpace(r.Header.Get("X-Timestamp"))
	nonce := strings.TrimSpace(r.Header.Get("X-Nonce"))
	provided := strings.TrimSpace(r.Header.Get("X-Signature"))
	timestamp, err := strconv.ParseInt(timestampRaw, 10, 64)
	if err != nil || len(nonce) < 16 || provided == "" || len(s.config.SharedSecret) < 32 {
		return false
	}
	now := s.now().UTC()
	issuedAt := time.Unix(timestamp, 0).UTC()
	if issuedAt.Before(now.Add(-s.config.ClockSkew)) || issuedAt.After(now.Add(s.config.ClockSkew)) {
		return false
	}
	message := timestampRaw + "\n" + nonce + "\n" + string(body) + "\n" + s.config.SharedSecret
	sum := md5.Sum([]byte(message))
	expected := base64.StdEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, expiresAt := range s.nonces {
		if !expiresAt.After(now) {
			delete(s.nonces, key)
		}
	}
	if _, exists := s.nonces[nonce]; exists {
		return false
	}
	s.nonces[nonce] = now.Add(s.config.ClockSkew)
	return true
}

func (s *sessionFileService) createSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID    string    `json:"user_id"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if decodeJSON(r, &input) != nil || !validOwnerID(input.UserID) || !s.validExpiry(input.ExpiresAt) {
		writeEnvelope(w, http.StatusBadRequest, "invalid_session", nil)
		return
	}
	metadata := sessionMetadata{UserID: input.UserID, CreatedAt: s.now().UTC(), ExpiresAt: input.ExpiresAt.UTC()}
	raw, _ := json.Marshal(metadata)
	if err := s.store.put(r.Context(), sessionKey(input.UserID), raw, "application/json"); err != nil {
		writeEnvelope(w, http.StatusInternalServerError, "storage_error", nil)
		return
	}
	writeEnvelope(w, http.StatusCreated, "created", map[string]any{"user_id": input.UserID, "expires_at": metadata.ExpiresAt})
}

func (s *sessionFileService) uploadFile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(s.config.MaxFileSize + (1 << 20)); err != nil {
		writeEnvelope(w, http.StatusBadRequest, "invalid_upload", nil)
		return
	}
	ownerID := strings.TrimSpace(r.FormValue("user_id"))
	session, err := s.loadSession(r.Context(), ownerID)
	if err != nil {
		writeEnvelope(w, http.StatusNotFound, "session_not_found", nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeEnvelope(w, http.StatusBadRequest, "invalid_upload", nil)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, s.config.MaxFileSize+1))
	if err != nil || int64(len(content)) > s.config.MaxFileSize {
		writeEnvelope(w, http.StatusRequestEntityTooLarge, "file_too_large", nil)
		return
	}
	name := cleanFilename(header.Filename)
	mediaType := normalizedMediaType(name, header.Header.Get("Content-Type"))
	if mediaType == "" || !contentMatches(content, mediaType) {
		writeEnvelope(w, http.StatusUnsupportedMediaType, "unsupported_file_type", nil)
		return
	}
	files, err := s.loadFiles(r.Context(), ownerID)
	if err != nil {
		writeEnvelope(w, http.StatusInternalServerError, "storage_error", nil)
		return
	}
	total := int64(len(content))
	for _, item := range files {
		total += item.Size
	}
	if len(files) >= s.config.MaxFiles || total > s.config.MaxTotalBytes {
		writeEnvelope(w, http.StatusTooManyRequests, "session_upload_limit", nil)
		return
	}
	fileID, err := randomID(16)
	if err != nil {
		writeEnvelope(w, http.StatusInternalServerError, "internal_error", nil)
		return
	}
	digest := sha256.Sum256(content)
	metadata := fileMetadata{
		FileID: fileID, Name: name, MediaType: mediaType, Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]),
		CreatedAt: s.now().UTC(), ExpiresAt: session.ExpiresAt,
	}
	if err := s.store.put(r.Context(), fileContentKey(ownerID, fileID), content, mediaType); err != nil {
		writeEnvelope(w, http.StatusInternalServerError, "storage_error", nil)
		return
	}
	raw, _ := json.Marshal(metadata)
	if err := s.store.put(r.Context(), fileMetadataKey(ownerID, fileID), raw, "application/json"); err != nil {
		_ = s.store.delete(r.Context(), fileContentKey(ownerID, fileID))
		writeEnvelope(w, http.StatusInternalServerError, "storage_error", nil)
		return
	}
	writeEnvelope(w, http.StatusCreated, "created", map[string]any{"file_id": fileID, "name": name, "media_type": mediaType, "size": len(content), "sha256": metadata.SHA256})
}

func (s *sessionFileService) listFiles(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID   string `json:"user_id"`
		Page     int    `json:"page"`
		PageSize int    `json:"page_size"`
	}
	if decodeJSON(r, &input) != nil || input.Page < 1 || input.PageSize < 1 || input.PageSize > 50 {
		writeEnvelope(w, http.StatusBadRequest, "invalid_pagination", nil)
		return
	}
	if _, err := s.loadSession(r.Context(), input.UserID); err != nil {
		writeEnvelope(w, http.StatusNotFound, "session_not_found", nil)
		return
	}
	files, err := s.loadFiles(r.Context(), input.UserID)
	if err != nil {
		writeEnvelope(w, http.StatusInternalServerError, "storage_error", nil)
		return
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].CreatedAt.Equal(files[j].CreatedAt) {
			return files[i].FileID > files[j].FileID
		}
		return files[i].CreatedAt.After(files[j].CreatedAt)
	})
	start := (input.Page - 1) * input.PageSize
	if start > len(files) {
		start = len(files)
	}
	end := start + input.PageSize
	if end > len(files) {
		end = len(files)
	}
	items := make([]map[string]any, 0, end-start)
	for _, item := range files[start:end] {
		items = append(items, map[string]any{
			"file_id": item.FileID, "name": item.Name, "media_type": item.MediaType,
			"size": item.Size, "sha256": item.SHA256, "created_at": item.CreatedAt,
			"expires_at": item.ExpiresAt, "last_downloaded_at": item.LastDownloadedAt,
		})
	}
	writeEnvelope(w, http.StatusOK, "ok", map[string]any{"items": items, "page": input.Page, "page_size": input.PageSize, "total": len(files)})
}

func (s *sessionFileService) downloadFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID string `json:"user_id"`
		FileID string `json:"file_id"`
	}
	if decodeJSON(r, &input) != nil || !validOwnerID(input.UserID) || strings.TrimSpace(input.FileID) == "" {
		writeEnvelope(w, http.StatusBadRequest, "invalid_request", nil)
		return
	}
	if _, err := s.loadSession(r.Context(), input.UserID); err != nil {
		writeEnvelope(w, http.StatusNotFound, "file_not_found", nil)
		return
	}
	metadata, err := s.loadFile(r.Context(), input.UserID, input.FileID)
	if err != nil {
		writeEnvelope(w, http.StatusNotFound, "file_not_found", nil)
		return
	}
	content, _, err := s.store.get(r.Context(), fileContentKey(input.UserID, input.FileID))
	if err != nil || int64(len(content)) != metadata.Size {
		writeEnvelope(w, http.StatusNotFound, "file_not_found", nil)
		return
	}
	digest, _ := hex.DecodeString(metadata.SHA256)
	w.Header().Set("Content-Type", metadata.MediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(metadata.Size, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": metadata.Name}))
	w.Header().Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(digest)+":")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(content); err == nil {
		downloadedAt := s.now().UTC()
		metadata.LastDownloadedAt = &downloadedAt
		raw, _ := json.Marshal(metadata)
		_ = s.store.put(r.Context(), fileMetadataKey(input.UserID, input.FileID), raw, "application/json")
	}
}

func (s *sessionFileService) deleteSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID string `json:"user_id"`
	}
	if decodeJSON(r, &input) != nil || !validOwnerID(input.UserID) {
		writeEnvelope(w, http.StatusBadRequest, "invalid_session", nil)
		return
	}
	if err := s.deleteOwner(r.Context(), input.UserID); err != nil {
		writeEnvelope(w, http.StatusInternalServerError, "storage_error", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *sessionFileService) validExpiry(expiresAt time.Time) bool {
	now := s.now().UTC()
	return expiresAt.After(now) && !expiresAt.After(now.Add(s.config.MaxTTL))
}

func (s *sessionFileService) loadSession(ctx context.Context, ownerID string) (sessionMetadata, error) {
	if !validOwnerID(ownerID) {
		return sessionMetadata{}, errObjectNotFound
	}
	raw, _, err := s.store.get(ctx, sessionKey(ownerID))
	if err != nil {
		return sessionMetadata{}, err
	}
	var metadata sessionMetadata
	if json.Unmarshal(raw, &metadata) != nil || !metadata.ExpiresAt.After(s.now().UTC()) {
		_ = s.deleteOwner(ctx, ownerID)
		return sessionMetadata{}, errObjectNotFound
	}
	return metadata, nil
}

func (s *sessionFileService) loadFiles(ctx context.Context, ownerID string) ([]fileMetadata, error) {
	objects, err := s.store.list(ctx, ownerPrefix(ownerID)+"files/")
	if err != nil {
		return nil, err
	}
	result := make([]fileMetadata, 0)
	for _, object := range objects {
		if !strings.HasSuffix(object.Key, ".json") {
			continue
		}
		raw, _, err := s.store.get(ctx, object.Key)
		if err != nil {
			return nil, err
		}
		var metadata fileMetadata
		if json.Unmarshal(raw, &metadata) != nil {
			return nil, fmt.Errorf("invalid file metadata")
		}
		result = append(result, metadata)
	}
	return result, nil
}

func (s *sessionFileService) loadFile(ctx context.Context, ownerID, fileID string) (fileMetadata, error) {
	raw, _, err := s.store.get(ctx, fileMetadataKey(ownerID, fileID))
	if err != nil {
		return fileMetadata{}, err
	}
	var metadata fileMetadata
	if json.Unmarshal(raw, &metadata) != nil || metadata.FileID != fileID {
		return fileMetadata{}, errObjectNotFound
	}
	return metadata, nil
}

func (s *sessionFileService) deleteOwner(ctx context.Context, ownerID string) error {
	objects, err := s.store.list(ctx, ownerPrefix(ownerID))
	if err != nil {
		return err
	}
	for _, object := range objects {
		if err := s.store.delete(ctx, object.Key); err != nil {
			return err
		}
	}
	return nil
}

func (s *sessionFileService) pruneExpired(ctx context.Context) error {
	objects, err := s.store.list(ctx, "sessions/")
	if err != nil {
		return err
	}
	for _, object := range objects {
		if !strings.HasSuffix(object.Key, "/session.json") {
			continue
		}
		raw, _, err := s.store.get(ctx, object.Key)
		if err != nil {
			return err
		}
		var metadata sessionMetadata
		if json.Unmarshal(raw, &metadata) != nil || !metadata.ExpiresAt.After(s.now().UTC()) {
			parts := strings.Split(object.Key, "/")
			if len(parts) >= 3 {
				prefixObjects, listErr := s.store.list(ctx, "sessions/"+parts[1]+"/")
				if listErr != nil {
					return listErr
				}
				for _, item := range prefixObjects {
					if err := s.store.delete(ctx, item.Key); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func ownerPrefix(ownerID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(ownerID)))
	return "sessions/" + hex.EncodeToString(sum[:]) + "/"
}
func sessionKey(ownerID string) string { return ownerPrefix(ownerID) + "session.json" }
func fileMetadataKey(ownerID, fileID string) string {
	return ownerPrefix(ownerID) + "files/" + fileID + ".json"
}
func fileContentKey(ownerID, fileID string) string {
	return ownerPrefix(ownerID) + "files/" + fileID + ".bin"
}

func validOwnerID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func randomID(bytesCount int) (string, error) {
	raw := make([]byte, bytesCount)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func cleanFilename(value string) string {
	return filepath.Base(strings.ReplaceAll(strings.TrimSpace(value), `\`, "/"))
}

func normalizedMediaType(name, declared string) string {
	parsed, _, _ := mime.ParseMediaType(declared)
	ext := strings.ToLower(filepath.Ext(name))
	expected := map[string]string{".pdf": "application/pdf", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}[ext]
	if expected == "" || (parsed != "" && parsed != expected && parsed != "application/octet-stream") {
		return ""
	}
	return expected
}

func contentMatches(content []byte, mediaType string) bool {
	switch mediaType {
	case "application/pdf":
		return bytes.HasPrefix(content, []byte("%PDF-"))
	case "image/png":
		return bytes.HasPrefix(content, []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		return len(content) >= 3 && content[0] == 0xff && content[1] == 0xd8 && content[2] == 0xff
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return bytes.HasPrefix(content, []byte("PK\x03\x04"))
	default:
		return false
	}
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected JSON data")
	}
	return nil
}

func writeEnvelope(w http.ResponseWriter, status int, message string, result any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": status, "message": message, "result": result})
}
