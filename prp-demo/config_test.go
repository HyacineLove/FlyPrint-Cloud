package main

import (
	"path/filepath"
	"testing"
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
