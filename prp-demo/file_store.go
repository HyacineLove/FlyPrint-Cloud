package main

import (
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
	db           *sql.DB
	dataDir      string
	maxFileSize  int64
	fileTTL      time.Duration
	temporaryDir string
	filesDir     string
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
	return &fileStore{
		db:           db,
		dataDir:      config.DataDir,
		maxFileSize:  config.MaxFileSizeBytes,
		fileTTL:      config.FileTTL,
		temporaryDir: temporaryDir,
		filesDir:     filesDir,
	}, nil
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

func (s *fileStore) uploadPDF(ctx context.Context, owner, originalName, declaredMediaType string, source io.Reader, now time.Time) (fileItem, error) {
	displayName := filepath.Base(strings.ReplaceAll(originalName, `\`, "/"))
	mediaType, _, err := mime.ParseMediaType(declaredMediaType)
	if err != nil || !strings.EqualFold(filepath.Ext(displayName), ".pdf") || mediaType != "application/pdf" {
		return fileItem{}, errUnsupportedFileType
	}
	id, err := randomFileID()
	if err != nil {
		return fileItem{}, fmt.Errorf("generate file id: %w", err)
	}
	temporaryPath := filepath.Join(s.temporaryDir, id+".part")
	targetName := id + ".pdf"
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
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return fileItem{}, fmt.Errorf("seek temporary file: %w", err)
	}
	signature := make([]byte, len("%PDF-"))
	if _, err := io.ReadFull(temporary, signature); err != nil || string(signature) != "%PDF-" {
		return fileItem{}, errUnsupportedFileType
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

func (s *fileStore) list(ctx context.Context, owner string, page, pageSize int) (fileList, error) {
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

func (s *fileStore) openDownload(ctx context.Context, owner, id string) (storedFile, *os.File, error) {
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
	return stored, file, nil
}

func (s *fileStore) markDownloaded(ctx context.Context, owner, id string, downloadedAt time.Time) error {
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
	stored, file, err := s.openDownload(ctx, owner, id)
	if err != nil {
		return err
	}
	_ = file.Close()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ? AND owner_subject = ?`, id, owner); err != nil {
		return fmt.Errorf("delete file metadata: %w", err)
	}
	if err := os.Remove(filepath.Join(s.dataDir, filepath.FromSlash(stored.RelativePath))); err != nil {
		return fmt.Errorf("delete stored file: %w", err)
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
