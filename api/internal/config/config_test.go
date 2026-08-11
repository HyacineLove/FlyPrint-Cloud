package config

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestLoadBindsPublicBaseURLEnvironment(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("FLY_PRINT_SERVER_PUBLIC_BASE_URL", "http://203.0.113.10:80")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.PublicBaseURL != "http://203.0.113.10:80" {
		t.Fatalf("PublicBaseURL = %q, want environment value", cfg.Server.PublicBaseURL)
	}
}

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

func TestValidateTemporaryInsecureHTTPRequiresShortLivedIPOrigin(t *testing.T) {
	cfg := validConfigForTest()
	cfg.App.Debug = false
	cfg.Security.EntryCookieSecure = false
	cfg.Security.InsecureHTTPMode = true
	cfg.Security.InsecureHTTPUntil = time.Now().Add(6 * 24 * time.Hour).UTC().Format(time.RFC3339)
	cfg.Server.PublicBaseURL = "http://203.0.113.10"
	cfg.Admin.ConsoleURL = "http://203.0.113.10"
	cfg.Server.AllowedOrigins = []string{"http://203.0.113.10"}
	cfg.Server.TrustedProxyCIDRs = []string{"172.18.0.0/16"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil for bounded temporary HTTP mode", err)
	}
}

func TestValidateTemporaryInsecureHTTPRejectsHostnameAndLongWindow(t *testing.T) {
	cfg := validConfigForTest()
	cfg.App.Debug = false
	cfg.Security.EntryCookieSecure = false
	cfg.Security.InsecureHTTPMode = true
	cfg.Security.InsecureHTTPUntil = time.Now().Add(8 * 24 * time.Hour).UTC().Format(time.RFC3339)
	cfg.Server.PublicBaseURL = "http://cloud.example.test"
	cfg.Admin.ConsoleURL = "http://cloud.example.test"
	cfg.Server.AllowedOrigins = []string{"http://cloud.example.test"}
	cfg.Server.TrustedProxyCIDRs = []string{"172.18.0.0/16"}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want temporary HTTP hostname/window rejection")
	}
}

func TestValidateProductionRequiresSpecificTrustedProxyCIDR(t *testing.T) {
	cfg := validConfigForTest()
	cfg.App.Debug = false
	cfg.Security.EntryCookieSecure = true
	cfg.Server.TrustedProxyCIDRs = []string{"0.0.0.0/0"}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want unsafe trusted proxy rejection")
	}

	cfg.Server.TrustedProxyCIDRs = []string{"172.18.0.0/16"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want specific proxy CIDR acceptance", err)
	}
}

func TestValidateKeycloakRedirectRequiresPublicCallbackOrigin(t *testing.T) {
	cfg := validConfigForTest()
	cfg.App.Debug = false
	cfg.Security.EntryCookieSecure = true
	cfg.Server.PublicBaseURL = "https://print.example.test"
	cfg.Server.TrustedProxyCIDRs = []string{"172.18.0.0/16"}
	cfg.OAuth2 = OAuth2Config{
		Mode: "keycloak", JWTSigningSecret: "12345678901234567890123456789012",
		ClientID: "cloud", ClientSecret: "secret", AuthURL: "https://id.example.test/auth",
		TokenURL: "https://id.example.test/token", UserInfoURL: "https://id.example.test/userinfo",
		JWKSURL: "https://id.example.test/jwks", Audience: "cloud", RedirectURI: "http://localhost:8012/auth/callback",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want localhost callback rejection")
	}

	cfg.OAuth2.RedirectURI = "https://print.example.test/auth/callback"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want public HTTPS callback acceptance", err)
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
