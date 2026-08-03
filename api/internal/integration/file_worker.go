package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/storage"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// errTransientFileFetch 标记网络/服务端 5xx/存储等瞬时错误：应重试而非永久失败。
var errTransientFileFetch = errors.New("transient file fetch error")

// FileWorker is the single owner of provider file retrieval. It validates the
// remote resource before it becomes a FlyPrint file; Edge never receives the
// provider URL.
type FileWorker struct {
	requests        *database.IntegrationPrintRequestRepository
	providers       *database.IntegrationProviderRepository
	files           *database.FileRepository
	storage         storage.Service
	storageProvider string
	storageBucket   string
	interval        time.Duration
}

func NewFileWorker(requests *database.IntegrationPrintRequestRepository, providers *database.IntegrationProviderRepository, files *database.FileRepository, service storage.Service, storageProvider, storageBucket string) *FileWorker {
	return &FileWorker{requests: requests, providers: providers, files: files, storage: service, storageProvider: storageProvider, storageBucket: storageBucket, interval: time.Second}
}

func (w *FileWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.ProcessOne(ctx); err != nil {
			logger.Error("Integration file worker iteration failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *FileWorker) ProcessOne(ctx context.Context) error {	request, err := w.requests.ClaimWaitingFile(time.Now(), 2*time.Minute)
	if err != nil || request == nil {
		return err
	}
	provider, err := w.providers.Get(request.ProviderCode, false)
	if err != nil {
		// DB/瞬时错误：不永久失败，返回 err 交给 worker 循环重试
		return err
	}
	if provider == nil || !provider.Enabled {
		return w.fail(request.ID, "provider_unavailable", "provider is unavailable")
	}
	file, objectKey, err := w.downloadAndStore(ctx, request, provider)
	if err != nil {
		if errors.Is(err, errTransientFileFetch) {
			// 网络/5xx/存储等瞬时错误：释放 claim 重试，不标记永久失败
			return err
		}
		return w.fail(request.ID, "file_validation_failed", "provider file could not be accepted")
	}
	if err := w.files.Create(file); err != nil {
		_ = w.storage.Delete(context.Background(), objectKey)
		return w.fail(request.ID, "file_record_failed", "FlyPrint file record could not be created")
	}
	if err := w.requests.MarkFileReady(request.ID, file.ID); err != nil {
		_ = w.files.DeleteByID(file.ID)
		_ = w.storage.Delete(context.Background(), objectKey)
		return err
	}
	return nil
}

func (w *FileWorker) fail(requestID, code, message string) error {
	if err := w.requests.MarkFileFailed(requestID, code, message); err != nil {
		return err
	}
	logger.Warn("Integration file request failed", zap.String("request_id", requestID), zap.String("code", code))
	return nil
}

func (w *FileWorker) downloadAndStore(ctx context.Context, request *models.IntegrationPrintRequest, provider *models.IntegrationProvider) (*models.File, string, error) {
	if !request.FileExpiresAt.After(time.Now()) {
		return nil, "", fmt.Errorf("provider file URL has expired")
	}
	response, err := fetchApprovedURL(ctx, request.FileURL, provider.AllowedFileHosts, provider.AllowPrivateFileHosts)
	if err != nil {
		// 网络/连接类瞬时错误可重试；URL 格式错误会持续失败直到 claim 超时释放
		return nil, "", errTransientFileFetch
	}
	defer response.Body.Close()
	if response.StatusCode >= 500 {
		return nil, "", errTransientFileFetch
	}
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return nil, "", fmt.Errorf("provider file status is not successful")
	}

	temporary, err := os.CreateTemp("", "flyprint-integration-*")
	if err != nil {
		return nil, "", err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()

	hash := sha256.New()
	limited := io.LimitReader(response.Body, request.FileSize+1)
	written, err := io.Copy(io.MultiWriter(temporary, hash), limited)
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, "", errTransientFileFetch
	}
	if written != request.FileSize {
		return nil, "", fmt.Errorf("provider file size does not match")
	}
	if actualHash := hex.EncodeToString(hash.Sum(nil)); actualHash != request.FileSHA256 {
		return nil, "", fmt.Errorf("provider file hash does not match")
	}

	if err := validateStoredMIME(temporaryName, request.MimeType, provider.AllowedMIMETypes); err != nil {
		return nil, "", err
	}
	reader, err := os.Open(temporaryName)
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()
	objectKey := "integrations/" + provider.Code + "/" + request.ID + "/" + uuid.NewString()
	if _, err := w.storage.Put(ctx, objectKey, reader, storage.PutOptions{ContentType: request.MimeType}); err != nil {
		return nil, "", err
	}
	return &models.File{
		OriginalName: filepath.Base(request.FileName), FileName: filepath.Base(request.FileName), FilePath: objectKey,
		ObjectKey: objectKey, StorageProvider: w.storageProvider, StorageBucket: w.storageBucket, MimeType: request.MimeType, Size: request.FileSize,
		ContentHash: request.FileSHA256, UploaderID: "integration:" + provider.Code,
	}, objectKey, nil
}

func validateStoredMIME(path, declared, allowed string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := make([]byte, 512)
	count, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return err
	}
	detected := http.DetectContentType(buffer[:count])
	detectedBase, _, _ := mime.ParseMediaType(detected)
	declaredBase, _, _ := mime.ParseMediaType(declared)
	if !strings.EqualFold(detectedBase, declaredBase) || !allowedCSVContains(allowed, declaredBase) {
		return fmt.Errorf("provider file MIME does not match")
	}
	return nil
}

func fetchApprovedURL(ctx context.Context, rawURL, allowedHosts string, allowPrivate bool) (*http.Response, error) {
	current, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	for redirects := 0; redirects <= 3; redirects++ {
		if err := validateProviderURL(ctx, current, allowedHosts, allowPrivate); err != nil {
			return nil, err
		}
		client := &http.Client{
			Timeout:       30 * time.Second,
			Transport:     &http.Transport{DialContext: dialApprovedAddress(allowPrivate)},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
		response, err := client.Get(current.String())
		if err != nil {
			return nil, err
		}
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			return response, nil
		}
		location, err := response.Location()
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		current = current.ResolveReference(location)
	}
	return nil, fmt.Errorf("provider file redirect limit exceeded")
}

// DialContext repeats resolution immediately before connection, preventing a
// host from passing policy resolution and then rebinding to a private address.
func dialApprovedAddress(allowPrivate bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("provider file host could not be resolved")
		}
		dialer := net.Dialer{Timeout: 10 * time.Second}
		var lastErr error
		for _, resolved := range addresses {
			if !isAllowedProviderAddress(resolved.IP, allowPrivate) {
				return nil, fmt.Errorf("provider file host resolved to forbidden address")
			}
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			if err == nil {
				return connection, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func validateProviderURL(ctx context.Context, target *url.URL, allowedHosts string, allowPrivate bool) error {
	if !IsHTTPOrHTTPSScheme(target.Scheme) || target.User != nil || !allowedCSVContains(allowedHosts, target.Hostname()) {
		return fmt.Errorf("provider file URL is not allowed")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, target.Hostname())
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("provider file host could not be resolved")
	}
	for _, address := range addresses {
		if !isAllowedProviderAddress(address.IP, allowPrivate) {
			return fmt.Errorf("provider file host resolved to forbidden address")
		}
	}
	return nil
}

func isPublicAddress(address net.IP) bool {
	return isAllowedProviderAddress(address, false)
}

func isAllowedProviderAddress(address net.IP, allowPrivate bool) bool {
	if address == nil || !address.IsGlobalUnicast() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() {
		return false
	}
	if address.IsPrivate() {
		return allowPrivate
	}
	for _, raw := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		_, block, _ := net.ParseCIDR(raw)
		if block.Contains(address) {
			return false
		}
	}
	return true
}

func allowedCSVContains(values, needle string) bool {
	for _, value := range strings.Split(values, ",") {
		if strings.EqualFold(strings.TrimSpace(value), needle) {
			return true
		}
	}
	return false
}
