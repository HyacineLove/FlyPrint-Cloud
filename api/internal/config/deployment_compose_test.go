package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDevelopmentComposeUsesAvailableMinIORegistryByDefault(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))

	composeBytes, err := os.ReadFile(filepath.Join(repoRoot, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	compose := string(composeBytes)
	for _, want := range []string{
		"${MINIO_IMAGE_MIRROR:-quay.io}/minio/minio:RELEASE.2025-04-22T22-12-26Z",
		"${MINIO_IMAGE_MIRROR:-quay.io}/minio/mc:RELEASE.2025-04-16T18-13-26Z",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("docker-compose.yml missing supported default image %q", want)
		}
	}

	envBytes, err := os.ReadFile(filepath.Join(repoRoot, ".env.example"))
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	if !strings.Contains(string(envBytes), "MINIO_IMAGE_MIRROR=quay.io") {
		t.Error(".env.example must default MINIO_IMAGE_MIRROR to quay.io")
	}
}

func TestCloudComposeKeepsSessionFilesInIndependentService(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	for _, relative := range []string{"docker-compose.yml", filepath.Join("deploy", "docker-compose.release.yml")} {
		raw, err := os.ReadFile(filepath.Join(repoRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		compose := string(raw)
		for _, want := range []string{
			"session-file-service:",
			"SESSION_FILE_MINIO_BUCKET: fly-print-session-files",
			"SESSION_FILE_SIGN_SECRET",
			"session-file-minio-policy.json",
		} {
			if !strings.Contains(compose, want) {
				t.Errorf("%s missing %q", relative, want)
			}
		}
		if strings.Contains(compose, "FLY_PRINT_SESSION_FILE") {
			t.Errorf("%s incorrectly injects transient file settings into Cloud API", relative)
		}
	}

	envBytes, err := os.ReadFile(filepath.Join(repoRoot, ".env.release.example"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"SESSION_FILE_SERVICE_IMAGE_TAG=",
		"SESSION_FILE_MINIO_ACCESS_KEY=",
		"SESSION_FILE_ALLOWED_CIDRS=",
	} {
		if !strings.Contains(string(envBytes), want) {
			t.Errorf(".env.release.example missing %q", want)
		}
	}
}
