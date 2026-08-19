package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func main() {
	config, store, listenAddress, sweepInterval, err := configurationFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	service := newSessionFileService(config, store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := service.pruneExpired(ctx); err != nil {
					log.Printf("session_file_prune failed: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	log.Printf("session-file-service listening on %s", listenAddress)
	log.Fatal(http.ListenAndServe(listenAddress, service.handler()))
}

func configurationFromEnvironment() (serviceConfig, objectStore, string, time.Duration, error) {
	maxTTL, err := positiveDurationEnv("SESSION_FILE_MAX_TTL", time.Hour)
	if err != nil {
		return serviceConfig{}, nil, "", 0, err
	}
	clockSkew, err := positiveDurationEnv("SESSION_FILE_CLOCK_SKEW", 5*time.Minute)
	if err != nil {
		return serviceConfig{}, nil, "", 0, err
	}
	sweepInterval, err := positiveDurationEnv("SESSION_FILE_SWEEP_INTERVAL", time.Minute)
	if err != nil {
		return serviceConfig{}, nil, "", 0, err
	}
	maxFileSize, err := positiveInt64Env("SESSION_FILE_MAX_FILE_SIZE", 20<<20)
	if err != nil {
		return serviceConfig{}, nil, "", 0, err
	}
	maxFilesRaw, err := positiveInt64Env("SESSION_FILE_MAX_FILES", 10)
	if err != nil {
		return serviceConfig{}, nil, "", 0, err
	}
	maxTotalBytes, err := positiveInt64Env("SESSION_FILE_MAX_TOTAL_BYTES", 80<<20)
	if err != nil {
		return serviceConfig{}, nil, "", 0, err
	}
	secret := os.Getenv("SESSION_FILE_SIGN_SECRET")
	if len(secret) < 32 {
		return serviceConfig{}, nil, "", 0, fmt.Errorf("SESSION_FILE_SIGN_SECRET must be at least 32 characters")
	}
	allowed, err := parseCIDRs(os.Getenv("SESSION_FILE_ALLOWED_CIDRS"), true)
	if err != nil {
		return serviceConfig{}, nil, "", 0, err
	}
	trusted, err := parseCIDRs(os.Getenv("SESSION_FILE_TRUSTED_PROXY_CIDRS"), false)
	if err != nil {
		return serviceConfig{}, nil, "", 0, err
	}
	endpoint := strings.TrimSpace(os.Getenv("SESSION_FILE_MINIO_ENDPOINT"))
	accessKey := strings.TrimSpace(os.Getenv("SESSION_FILE_MINIO_ACCESS_KEY"))
	secretKey := os.Getenv("SESSION_FILE_MINIO_SECRET_KEY")
	bucket := strings.TrimSpace(os.Getenv("SESSION_FILE_MINIO_BUCKET"))
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		return serviceConfig{}, nil, "", 0, fmt.Errorf("session file MinIO configuration is incomplete")
	}
	useSSL, err := strconv.ParseBool(defaultValue(os.Getenv("SESSION_FILE_MINIO_USE_SSL"), "false"))
	if err != nil {
		return serviceConfig{}, nil, "", 0, fmt.Errorf("SESSION_FILE_MINIO_USE_SSL is invalid")
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: useSSL})
	if err != nil {
		return serviceConfig{}, nil, "", 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil || !exists {
		if err == nil {
			err = fmt.Errorf("bucket %s does not exist", bucket)
		}
		return serviceConfig{}, nil, "", 0, err
	}
	listenPort := defaultValue(os.Getenv("SESSION_FILE_LISTEN_PORT"), "8080")
	port, err := strconv.Atoi(listenPort)
	if err != nil || port < 1 || port > 65535 {
		return serviceConfig{}, nil, "", 0, fmt.Errorf("SESSION_FILE_LISTEN_PORT is invalid")
	}
	config := serviceConfig{
		SharedSecret: secret, MaxTTL: maxTTL, ClockSkew: clockSkew,
		MaxFileSize: maxFileSize, MaxFiles: int(maxFilesRaw), MaxTotalBytes: maxTotalBytes,
		AllowedCIDRs: allowed, TrustedProxyCIDRs: trusted,
	}
	return config, &minioObjectStore{client: client, bucket: bucket}, fmt.Sprintf(":%d", port), sweepInterval, nil
}

func positiveDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return value, nil
}

func positiveInt64Env(name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return value, nil
}

func parseCIDRs(raw string, required bool) ([]*net.IPNet, error) {
	parts := strings.Split(raw, ",")
	result := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q", value)
		}
		result = append(result, network)
	}
	if required && len(result) == 0 {
		return nil, fmt.Errorf("SESSION_FILE_ALLOWED_CIDRS is required")
	}
	return result, nil
}

func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
