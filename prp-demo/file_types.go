package main

import (
	"errors"
	"time"
)

var (
	errUnsupportedFileType = errors.New("unsupported file type")
	errFileTooLarge        = errors.New("file too large")
	errFileNotFound        = errors.New("file not found")
)

type fileItem struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	MediaType        string     `json:"media_type"`
	Size             int64      `json:"size"`
	SHA256           string     `json:"sha256"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	LastDownloadedAt *time.Time `json:"last_downloaded_at"`
}

type fileList struct {
	Items    []fileItem `json:"items"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Total    int        `json:"total"`
}

type storedFile struct {
	fileItem
	RelativePath string
}
