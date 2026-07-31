package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestConfigurationRejectsShortTokenSecret(t *testing.T) {
	config := testConfiguration(t)
	config.TokenSecret = "too-short"

	if err := config.validate(); err == nil {
		t.Fatal("expected short token secret to be rejected")
	}
}

func TestConfigurationRejectsWildcardOrigin(t *testing.T) {
	config := testConfiguration(t)
	config.AllowedUploadOrigins = []string{"*"}

	if err := config.validate(); err == nil {
		t.Fatal("expected wildcard origin to be rejected")
	}
}

func TestConfigurationRejectsDatabaseOutsideDataDir(t *testing.T) {
	config := testConfiguration(t)
	config.DatabaseFile = filepath.Join(filepath.Dir(config.DataDir), "outside.db")

	if err := config.validate(); err == nil {
		t.Fatal("expected database outside data directory to be rejected")
	}
}

func TestConfigurationReadsStorageGovernanceLimits(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("PRP_DATA_DIR", dataDir)
	t.Setenv("PRP_DATABASE_FILE", filepath.Join(dataDir, "prp.db"))
	t.Setenv("PRP_TOKEN_SECRET", testTokenSecret)
	t.Setenv("PRP_TOKEN_ISSUER", "flyprint-sso-demo")
	t.Setenv("PRP_TOKEN_AUDIENCE", "flyprint-prp-demo")
	t.Setenv("PRP_SITE_PORTAL_CODE", "official")
	t.Setenv("PRP_ALLOWED_UPLOAD_ORIGINS", "https://portal.example.test")
	t.Setenv("PRP_PUBLIC_BASE_URL", "https://prp.example.test")
	t.Setenv("PRP_MAX_FILE_SIZE_BYTES", "1024")
	t.Setenv("PRP_MAX_FILES_PER_USER", "3")
	t.Setenv("PRP_MAX_BYTES_PER_USER", "4096")
	t.Setenv("PRP_MAX_TOTAL_BYTES", "8192")
	t.Setenv("PRP_FILE_TTL_SECONDS", "60")
	t.Setenv("PRP_CLEANUP_INTERVAL_SECONDS", "5")

	config, _, err := configurationFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if config.MaxFileSizeBytes != 1024 ||
		config.MaxFilesPerUser != 3 ||
		config.MaxBytesPerUser != 4096 ||
		config.MaxTotalBytes != 8192 ||
		config.FileTTL != time.Minute ||
		config.CleanupInterval != 5*time.Second {
		t.Fatalf("config=%#v", config)
	}
}
