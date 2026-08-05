package config

import "testing"

func TestValidateStorageProvider(t *testing.T) {
	t.Parallel()

	cfg := validConfigForTest()
	cfg.Storage.Provider = "unsupported"

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() error = nil, want non-nil for unsupported provider")
	}
}

func TestValidateStorageDownloadMode(t *testing.T) {
	t.Parallel()

	cfg := validConfigForTest()
	cfg.Storage.DownloadMode = "invalid"

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() error = nil, want non-nil for unsupported download mode")
	}
}

func TestValidateStorageMinIOConfig(t *testing.T) {
	t.Parallel()

	cfg := validConfigForTest()
	cfg.Storage.Provider = "minio"
	cfg.Storage.DownloadMode = "proxy"
	cfg.Storage.MinIO = MinIOConfig{}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() error = nil, want non-nil for missing minio config")
	}
}

func TestValidateStorageMinIOConfigLocalProvider(t *testing.T) {
	t.Parallel()

	cfg := validConfigForTest()
	cfg.Storage.Provider = "local"
	cfg.Storage.DownloadMode = "proxy"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestValidateSitePortalBootstrapRequiresCompleteConfiguration(t *testing.T) {
	t.Parallel()

	cfg := validConfigForTest()
	cfg.SitePortalBootstrap = SitePortalBootstrapConfig{
		Code: "official",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want incomplete Site Portal bootstrap rejection")
	}
}

func TestValidateSitePortalBootstrapAcceptsExplicitConfiguration(t *testing.T) {
	t.Parallel()

	cfg := validConfigForTest()
	cfg.SitePortalBootstrap = SitePortalBootstrapConfig{
		Code:         "official",
		DisplayName:  "FlyPrint",
		EntryURL:     "https://portal.example.test/entry",
		ClaimBaseURL: "https://portal.example.test",
		OAuthClientID: "site-portal-official",
		OAuthClientSecret: "12345678901234567890123456789012",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func validConfigForTest() *Config {
	return &Config{
		App: AppConfig{
			Name:  "fly-print-cloud",
			Debug: true,
		},
		Database: DatabaseConfig{
			Host:   "localhost",
			Port:   5432,
			User:   "postgres",
			DBName: "fly_print_cloud",
		},
		Server: ServerConfig{
			Port: 8080,
		},
		OAuth2: OAuth2Config{
			Mode:             "builtin",
			JWTSigningSecret: "12345678901234567890123456789012",
		},
		Storage: StorageConfig{
			UploadDir:        "./uploads",
			Provider:         "local",
			DownloadMode:     "proxy",
			MaxSize:          1024,
			MaxDocumentPages: 5,
		},
		Security: SecurityConfig{
			FileAccessSecret:               "12345678901234567890123456789012",
			OAuthClientSecretEncryptionKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			UploadTokenTTL:                 180,
			DownloadTokenTTL:               180,
		},
	}
}
