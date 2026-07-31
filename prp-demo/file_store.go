package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const fileSchema = `
CREATE TABLE IF NOT EXISTS files (
  id TEXT PRIMARY KEY,
  owner_subject TEXT NOT NULL,
  original_name TEXT NOT NULL,
  media_type TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  relative_path TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  last_downloaded_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_files_owner_created
  ON files(owner_subject, created_at DESC, id DESC);
`

type fileStore struct {
	db              *sql.DB
	dataDir         string
	maxFileSize     int64
	maxFilesPerUser int
	maxBytesPerUser int64
	maxTotalBytes   int64
	fileTTL         time.Duration
	temporaryDir    string
	filesDir        string
	mu              sync.Mutex
	activeDownloads map[string]int
}

type leasedFile struct {
	*os.File
	once    sync.Once
	release func()
}

func (f *leasedFile) Close() error {
	err := f.File.Close()
	f.once.Do(f.release)
	return err
}

func openFileStore(config configuration) (*fileStore, error) {
	temporaryDir := filepath.Join(config.DataDir, "tmp")
	filesDir := filepath.Join(config.DataDir, "files")
	for _, directory := range []string{config.DataDir, temporaryDir, filesDir} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return nil, fmt.Errorf("create storage directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", config.DatabaseFile)
	if err != nil {
		return nil, fmt.Errorf("open metadata database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(fileSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize metadata database: %w", err)
	}
	store := &fileStore{
		db:              db,
		dataDir:         config.DataDir,
		maxFileSize:     config.MaxFileSizeBytes,
		maxFilesPerUser: config.MaxFilesPerUser,
		maxBytesPerUser: config.MaxBytesPerUser,
		maxTotalBytes:   config.MaxTotalBytes,
		fileTTL:         config.FileTTL,
		temporaryDir:    temporaryDir,
		filesDir:        filesDir,
		activeDownloads: make(map[string]int),
	}
	if err := store.reconcileStartup(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *fileStore) close() error {
	return s.db.Close()
}

func randomFileID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (s *fileStore) uploadFile(ctx context.Context, owner, originalName, declaredMediaType string, source io.Reader, now time.Time) (fileItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	displayName := filepath.Base(strings.ReplaceAll(originalName, `\`, "/"))
	mediaType, _, err := mime.ParseMediaType(declaredMediaType)
	if err != nil {
		return fileItem{}, errUnsupportedFileType
	}
	extension, ok := supportedFileExtension(displayName, mediaType)
	if !ok {
		return fileItem{}, errUnsupportedFileType
	}
	id, err := randomFileID()
	if err != nil {
		return fileItem{}, fmt.Errorf("generate file id: %w", err)
	}
	temporaryPath := filepath.Join(s.temporaryDir, id+".part")
	targetName := id + extension
	targetPath := filepath.Join(s.filesDir, targetName)

	temporary, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o640)
	if err != nil {
		return fileItem{}, fmt.Errorf("create temporary file: %w", err)
	}
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(source, s.maxFileSize+1))
	if err != nil {
		return fileItem{}, fmt.Errorf("write temporary file: %w", err)
	}
	if size > s.maxFileSize {
		return fileItem{}, errFileTooLarge
	}
	if err := validateTemporaryFile(temporary, size, mediaType); err != nil {
		return fileItem{}, errUnsupportedFileType
	}
	if err := s.cleanupExpiredLocked(ctx, now.UTC()); err != nil {
		return fileItem{}, err
	}
	if err := s.ensureCapacityLocked(ctx, owner, size); err != nil {
		return fileItem{}, err
	}
	if err := temporary.Sync(); err != nil {
		return fileItem{}, fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fileItem{}, fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, targetPath); err != nil {
		return fileItem{}, fmt.Errorf("publish file: %w", err)
	}
	removeTemporary = false

	createdAt := now.UTC()
	item := fileItem{
		ID:        id,
		Name:      displayName,
		MediaType: mediaType,
		Size:      size,
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
		CreatedAt: createdAt,
		ExpiresAt: createdAt.Add(s.fileTTL),
	}
	relativePath := filepath.ToSlash(filepath.Join("files", targetName))
	_, err = s.db.ExecContext(ctx, `
INSERT INTO files (
  id, owner_subject, original_name, media_type, size_bytes, sha256,
  relative_path, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, owner, item.Name, item.MediaType, item.Size, item.SHA256,
		relativePath, formatDatabaseTime(item.CreatedAt), formatDatabaseTime(item.ExpiresAt),
	)
	if err != nil {
		_ = os.Remove(targetPath)
		return fileItem{}, fmt.Errorf("insert file metadata: %w", err)
	}
	return item, nil
}

func supportedFileExtension(displayName, mediaType string) (string, bool) {
	extension := strings.ToLower(filepath.Ext(displayName))
	switch mediaType {
	case "application/pdf":
		return extension, extension == ".pdf"
	case "image/png":
		return extension, extension == ".png"
	case "image/jpeg":
		return extension, extension == ".jpg" || extension == ".jpeg"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return extension, extension == ".docx"
	default:
		return "", false
	}
}

func validateTemporaryFile(file *os.File, size int64, mediaType string) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	switch mediaType {
	case "application/pdf":
		signature := make([]byte, len("%PDF-"))
		if _, err := io.ReadFull(file, signature); err != nil || string(signature) != "%PDF-" {
			return errUnsupportedFileType
		}
	case "image/png":
		signature := make([]byte, 8)
		if _, err := io.ReadFull(file, signature); err != nil || !bytes.Equal(signature, []byte("\x89PNG\r\n\x1a\n")) {
			return errUnsupportedFileType
		}
	case "image/jpeg":
		signature := make([]byte, 3)
		if _, err := io.ReadFull(file, signature); err != nil ||
			signature[0] != 0xff || signature[1] != 0xd8 || signature[2] != 0xff {
			return errUnsupportedFileType
		}
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		archive, err := zip.NewReader(file, size)
		if err != nil {
			return errUnsupportedFileType
		}
		required := map[string]bool{
			"[Content_Types].xml": false,
			"word/document.xml":   false,
		}
		for _, item := range archive.File {
			if _, exists := required[item.Name]; exists {
				required[item.Name] = true
			}
		}
		for _, present := range required {
			if !present {
				return errUnsupportedFileType
			}
		}
	default:
		return errUnsupportedFileType
	}
	return nil
}

func (s *fileStore) list(ctx context.Context, owner string, page, pageSize int) (fileList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := fileList{Items: make([]fileItem, 0), Page: page, PageSize: pageSize}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE owner_subject = ?`, owner).Scan(&result.Total); err != nil {
		return fileList{}, fmt.Errorf("count files: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, original_name, media_type, size_bytes, sha256, created_at, expires_at, last_downloaded_at
FROM files
WHERE owner_subject = ?
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?`, owner, pageSize, (page-1)*pageSize)
	if err != nil {
		return fileList{}, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item fileItem
		var createdAt, expiresAt string
		var lastDownloadedAt sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Name, &item.MediaType, &item.Size, &item.SHA256,
			&createdAt, &expiresAt, &lastDownloadedAt,
		); err != nil {
			return fileList{}, fmt.Errorf("scan file: %w", err)
		}
		item.CreatedAt, err = parseDatabaseTime(createdAt)
		if err != nil {
			return fileList{}, err
		}
		item.ExpiresAt, err = parseDatabaseTime(expiresAt)
		if err != nil {
			return fileList{}, err
		}
		if lastDownloadedAt.Valid {
			parsed, parseErr := parseDatabaseTime(lastDownloadedAt.String)
			if parseErr != nil {
				return fileList{}, parseErr
			}
			item.LastDownloadedAt = &parsed
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return fileList{}, fmt.Errorf("iterate files: %w", err)
	}
	return result, nil
}

func (s *fileStore) openDownload(ctx context.Context, owner, id string) (storedFile, *leasedFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var stored storedFile
	var createdAt, expiresAt string
	var lastDownloadedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT id, original_name, media_type, size_bytes, sha256, relative_path,
       created_at, expires_at, last_downloaded_at
FROM files
WHERE id = ? AND owner_subject = ?`, id, owner).Scan(
		&stored.ID, &stored.Name, &stored.MediaType, &stored.Size, &stored.SHA256,
		&stored.RelativePath, &createdAt, &expiresAt, &lastDownloadedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return storedFile{}, nil, errFileNotFound
	}
	if err != nil {
		return storedFile{}, nil, fmt.Errorf("load file metadata: %w", err)
	}
	stored.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return storedFile{}, nil, err
	}
	stored.ExpiresAt, err = parseDatabaseTime(expiresAt)
	if err != nil {
		return storedFile{}, nil, err
	}
	if lastDownloadedAt.Valid {
		parsed, parseErr := parseDatabaseTime(lastDownloadedAt.String)
		if parseErr != nil {
			return storedFile{}, nil, parseErr
		}
		stored.LastDownloadedAt = &parsed
	}
	path := filepath.Join(s.dataDir, filepath.FromSlash(stored.RelativePath))
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return storedFile{}, nil, errFileNotFound
	}
	if err != nil {
		return storedFile{}, nil, fmt.Errorf("open stored file: %w", err)
	}
	s.activeDownloads[id]++
	return stored, &leasedFile{
		File: file,
		release: func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.activeDownloads[id] <= 1 {
				delete(s.activeDownloads, id)
				return
			}
			s.activeDownloads[id]--
		},
	}, nil
}

func (s *fileStore) markDownloaded(ctx context.Context, owner, id string, downloadedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
UPDATE files SET last_downloaded_at = ? WHERE id = ? AND owner_subject = ?`,
		formatDatabaseTime(downloadedAt.UTC()), id, owner,
	)
	if err != nil {
		return fmt.Errorf("update download time: %w", err)
	}
	return nil
}

func (s *fileStore) delete(ctx context.Context, owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var relativePath string
	err := s.db.QueryRowContext(ctx, `SELECT relative_path FROM files WHERE id = ? AND owner_subject = ?`, id, owner).Scan(&relativePath)
	if errors.Is(err, sql.ErrNoRows) {
		return errFileNotFound
	}
	if err != nil {
		return err
	}
	if s.activeDownloads[id] > 0 {
		return errStorageCapacity
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ? AND owner_subject = ?`, id, owner); err != nil {
		return fmt.Errorf("delete file metadata: %w", err)
	}
	if err := os.Remove(filepath.Join(s.dataDir, filepath.FromSlash(relativePath))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete stored file: %w", err)
	}
	return nil
}

func (s *fileStore) cleanup(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupExpiredLocked(ctx, now.UTC())
}

func (s *fileStore) cleanupExpiredLocked(ctx context.Context, now time.Time) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, relative_path FROM files
WHERE expires_at <= ?
ORDER BY expires_at, id`, formatDatabaseTime(now))
	if err != nil {
		return fmt.Errorf("query expired files: %w", err)
	}
	type expiredFile struct {
		id           string
		relativePath string
	}
	var expired []expiredFile
	for rows.Next() {
		var item expiredFile
		if err := rows.Scan(&item.id, &item.relativePath); err != nil {
			return fmt.Errorf("scan expired file: %w", err)
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate expired files: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close expired file rows: %w", err)
	}
	for _, item := range expired {
		if s.activeDownloads[item.id] > 0 {
			continue
		}
		if err := s.removeRecordLocked(ctx, item.id, item.relativePath); err != nil {
			return err
		}
	}
	return nil
}

func (s *fileStore) ensureCapacityLocked(ctx context.Context, owner string, incomingBytes int64) error {
	if incomingBytes > s.maxBytesPerUser || incomingBytes > s.maxTotalBytes {
		return errStorageCapacity
	}
	for {
		var count int
		var used int64
		if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM files WHERE owner_subject = ?`, owner).Scan(&count, &used); err != nil {
			return fmt.Errorf("query user storage usage: %w", err)
		}
		if count+1 <= s.maxFilesPerUser && used+incomingBytes <= s.maxBytesPerUser {
			break
		}
		evicted, err := s.evictOneLocked(ctx, owner)
		if err != nil {
			return err
		}
		if !evicted {
			return errStorageCapacity
		}
	}
	for {
		var used int64
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM files`).Scan(&used); err != nil {
			return fmt.Errorf("query total storage usage: %w", err)
		}
		if used+incomingBytes <= s.maxTotalBytes {
			return nil
		}
		evicted, err := s.evictOneLocked(ctx, "")
		if err != nil {
			return err
		}
		if !evicted {
			return errStorageCapacity
		}
	}
}

func (s *fileStore) evictOneLocked(ctx context.Context, owner string) (bool, error) {
	query := `
SELECT id, relative_path FROM files`
	args := make([]any, 0, 1)
	if owner != "" {
		query += ` WHERE owner_subject = ?`
		args = append(args, owner)
	}
	query += ` ORDER BY COALESCE(last_downloaded_at, created_at), created_at, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("query eviction candidates: %w", err)
	}
	type candidate struct {
		id           string
		relativePath string
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.relativePath); err != nil {
			return false, fmt.Errorf("scan eviction candidate: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, fmt.Errorf("iterate eviction candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close eviction rows: %w", err)
	}
	for _, item := range candidates {
		if s.activeDownloads[item.id] > 0 {
			continue
		}
		if err := s.removeRecordLocked(ctx, item.id, item.relativePath); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (s *fileStore) removeRecordLocked(ctx context.Context, id, relativePath string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete file metadata: %w", err)
	}
	path := filepath.Join(s.dataDir, filepath.FromSlash(relativePath))
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete stored file: %w", err)
	}
	return nil
}

func (s *fileStore) reconcileStartup(ctx context.Context) error {
	temporaryEntries, err := os.ReadDir(s.temporaryDir)
	if err != nil {
		return fmt.Errorf("read temporary directory: %w", err)
	}
	for _, entry := range temporaryEntries {
		if err := os.RemoveAll(filepath.Join(s.temporaryDir, entry.Name())); err != nil {
			return fmt.Errorf("remove stale temporary file: %w", err)
		}
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, relative_path FROM files`)
	if err != nil {
		return fmt.Errorf("query stored files: %w", err)
	}
	referenced := make(map[string]struct{})
	type missingRecord struct {
		id           string
		relativePath string
	}
	var missing []missingRecord
	for rows.Next() {
		var item missingRecord
		if err := rows.Scan(&item.id, &item.relativePath); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan stored file: %w", err)
		}
		path := filepath.Clean(filepath.Join(s.dataDir, filepath.FromSlash(item.relativePath)))
		relative, relErr := filepath.Rel(s.filesDir, path)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			missing = append(missing, item)
			continue
		}
		if _, statErr := os.Stat(path); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				missing = append(missing, item)
				continue
			}
			_ = rows.Close()
			return fmt.Errorf("stat stored file: %w", statErr)
		}
		referenced[path] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close stored file rows: %w", err)
	}
	for _, item := range missing {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, item.id); err != nil {
			return fmt.Errorf("delete missing file metadata: %w", err)
		}
	}
	entries, err := os.ReadDir(s.filesDir)
	if err != nil {
		return fmt.Errorf("read files directory: %w", err)
	}
	for _, entry := range entries {
		path := filepath.Join(s.filesDir, entry.Name())
		if _, ok := referenced[path]; ok {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove orphaned file: %w", err)
		}
	}
	return nil
}

func formatDatabaseTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseDatabaseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse database time: %w", err)
	}
	return parsed.UTC(), nil
}
