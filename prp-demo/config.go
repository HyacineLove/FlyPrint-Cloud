package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultMaxFileSizeBytes int64 = 50 * 1024 * 1024

const (
	defaultFileTTL          = 7 * 24 * time.Hour
	defaultUploadContextTTL = 5 * time.Minute
	defaultCleanupInterval  = 5 * time.Minute
	defaultMaxFilesPerUser  = 20
	defaultMaxBytesPerUser  = 200 * 1024 * 1024
	defaultMaxTotalBytes    = 1024 * 1024 * 1024
)

type configuration struct {
	DataDir              string
	DatabaseFile         string
	TokenSecret          string
	TokenIssuer          string
	TokenAudience        string
	SitePortalCode       string
	AllowedUploadOrigins []string
	PublicBaseURL        string
	MaxFileSizeBytes     int64
	MaxFilesPerUser      int
	MaxBytesPerUser      int64
	MaxTotalBytes        int64
	FileTTL              time.Duration
	CleanupInterval      time.Duration
	UploadContextTTL     time.Duration
}

func (c configuration) validate() error {
	if strings.TrimSpace(c.DataDir) == "" ||
		strings.TrimSpace(c.DatabaseFile) == "" ||
		len(c.TokenSecret) < 32 ||
		strings.TrimSpace(c.TokenIssuer) == "" ||
		strings.TrimSpace(c.TokenAudience) == "" ||
		strings.TrimSpace(c.SitePortalCode) == "" ||
		len(c.AllowedUploadOrigins) == 0 ||
		c.MaxFileSizeBytes <= 0 ||
		c.MaxFilesPerUser <= 0 ||
		c.MaxBytesPerUser <= 0 ||
		c.MaxTotalBytes <= 0 ||
		c.MaxFileSizeBytes > c.MaxBytesPerUser ||
		c.MaxFileSizeBytes > c.MaxTotalBytes ||
		c.FileTTL <= 0 ||
		c.CleanupInterval <= 0 ||
		c.UploadContextTTL <= 0 {
		return fmt.Errorf("PRP Demo configuration is incomplete")
	}
	dataDir, err := filepath.Abs(c.DataDir)
	if err != nil {
		return fmt.Errorf("resolve PRP_DATA_DIR: %w", err)
	}
	databaseFile, err := filepath.Abs(c.DatabaseFile)
	if err != nil {
		return fmt.Errorf("resolve PRP_DATABASE_FILE: %w", err)
	}
	relative, err := filepath.Rel(dataDir, databaseFile)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("PRP_DATABASE_FILE must be inside PRP_DATA_DIR")
	}
	if err := validateHTTPBaseURL(c.PublicBaseURL); err != nil {
		return fmt.Errorf("PRP_PUBLIC_BASE_URL: %w", err)
	}
	for _, origin := range c.AllowedUploadOrigins {
		if err := validateOrigin(origin); err != nil {
			return fmt.Errorf("PRP_ALLOWED_UPLOAD_ORIGINS: %w", err)
		}
	}
	return nil
}

func validateHTTPBaseURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must be an absolute HTTP(S) URL without userinfo, query, or fragment")
	}
	return nil
}

func validateOrigin(raw string) error {
	if strings.Contains(raw, "*") {
		return fmt.Errorf("wildcard origins are not allowed")
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("origin must contain only scheme and host")
	}
	return nil
}

func configurationFromEnvironment() (configuration, string, error) {
	maxFileSize, err := positiveInt64Environment("PRP_MAX_FILE_SIZE_BYTES", defaultMaxFileSizeBytes)
	if err != nil {
		return configuration{}, "", err
	}
	maxFilesPerUser, err := positiveIntEnvironment("PRP_MAX_FILES_PER_USER", defaultMaxFilesPerUser)
	if err != nil {
		return configuration{}, "", err
	}
	maxBytesPerUser, err := positiveInt64Environment("PRP_MAX_BYTES_PER_USER", defaultMaxBytesPerUser)
	if err != nil {
		return configuration{}, "", err
	}
	maxTotalBytes, err := positiveInt64Environment("PRP_MAX_TOTAL_BYTES", defaultMaxTotalBytes)
	if err != nil {
		return configuration{}, "", err
	}
	fileTTLSeconds, err := positiveInt64Environment("PRP_FILE_TTL_SECONDS", int64(defaultFileTTL/time.Second))
	if err != nil {
		return configuration{}, "", err
	}
	cleanupIntervalSeconds, err := positiveInt64Environment("PRP_CLEANUP_INTERVAL_SECONDS", int64(defaultCleanupInterval/time.Second))
	if err != nil {
		return configuration{}, "", err
	}
	config := configuration{
		DataDir:              strings.TrimSpace(os.Getenv("PRP_DATA_DIR")),
		DatabaseFile:         strings.TrimSpace(os.Getenv("PRP_DATABASE_FILE")),
		TokenSecret:          os.Getenv("PRP_TOKEN_SECRET"),
		TokenIssuer:          strings.TrimSpace(os.Getenv("PRP_TOKEN_ISSUER")),
		TokenAudience:        strings.TrimSpace(os.Getenv("PRP_TOKEN_AUDIENCE")),
		SitePortalCode:       strings.TrimSpace(os.Getenv("PRP_SITE_PORTAL_CODE")),
		AllowedUploadOrigins: splitNonEmpty(os.Getenv("PRP_ALLOWED_UPLOAD_ORIGINS")),
		PublicBaseURL:        strings.TrimRight(strings.TrimSpace(os.Getenv("PRP_PUBLIC_BASE_URL")), "/"),
		MaxFileSizeBytes:     maxFileSize,
		MaxFilesPerUser:      maxFilesPerUser,
		MaxBytesPerUser:      maxBytesPerUser,
		MaxTotalBytes:        maxTotalBytes,
		FileTTL:              time.Duration(fileTTLSeconds) * time.Second,
		CleanupInterval:      time.Duration(cleanupIntervalSeconds) * time.Second,
		UploadContextTTL:     defaultUploadContextTTL,
	}
	if err := config.validate(); err != nil {
		return configuration{}, "", err
	}
	port := 8080
	if raw := strings.TrimSpace(os.Getenv("PRP_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			return configuration{}, "", fmt.Errorf("PRP_PORT is invalid")
		}
		port = parsed
	}
	return config, fmt.Sprintf(":%d", port), nil
}

func positiveIntEnvironment(name string, defaultValue int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return parsed, nil
}

func positiveInt64Environment(name string, defaultValue int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return parsed, nil
}

func splitNonEmpty(raw string) []string {
	values := make([]string, 0)
	for _, value := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}
