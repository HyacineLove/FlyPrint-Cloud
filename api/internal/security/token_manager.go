package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fly-print-cloud/api/internal/business"
	"fly-print-cloud/api/internal/logger"

	"go.uber.org/zap"
)

const (
	TokenTypeUpload   = "upload"
	TokenTypeDownload = "download"

	DefaultTokenTTL = 180
	DefaultSecret   = "fly-print-file-access-secret-dev-only"
)

// TokenUsageMarker lets the token manager pre-register and consume one-time tokens
// without introducing a dependency cycle on the database package.
type TokenUsageMarker interface {
	PreRegisterToken(tokenHash, tokenType, nodeID, resourceID, jobID string, expiresAt time.Time) error
	MarkTokenAsUsed(tokenHash, tokenType, nodeID, resourceID, jobID string, expiresAt time.Time) error
}

type TokenManager struct {
	secret           string
	uploadTokenTTL   int
	downloadTokenTTL int
	ttlProvider      tokenTTLProvider
	tokenRepo        TokenUsageMarker
}

type tokenTTLProvider interface {
	Current() (business.Settings, error)
}

type tokenStatusReader interface {
	GetTokenStatus(tokenHash string) (used bool, revoked bool, found bool, err error)
}

type UploadTokenPayload struct {
	NodeID    string
	PrinterID string
	IssuedAt  int64
	ExpiresAt int64
}

type DownloadTokenPayload struct {
	FileID    string
	JobID     string
	NodeID    string
	IssuedAt  int64
	ExpiresAt int64
}

type TokenError struct {
	Code    string
	Message string
}

func (e *TokenError) Error() string {
	return e.Message
}

var (
	ErrInvalidFormat    = &TokenError{Code: "invalid_format", Message: "Invalid token format"}
	ErrTokenExpired     = &TokenError{Code: "token_expired", Message: "Token has expired"}
	ErrInvalidSignature = &TokenError{Code: "invalid_signature", Message: "Invalid token signature"}
	ErrInvalidType      = &TokenError{Code: "invalid_type", Message: "Invalid token type"}
	ErrInvalidContext   = &TokenError{Code: "invalid_context", Message: "Token context mismatch"}
	ErrTokenAlreadyUsed = &TokenError{Code: "token_already_used", Message: "Token has already been used"}
)

func NewTokenManager(secret string, uploadTTL, downloadTTL int, tokenRepo TokenUsageMarker) *TokenManager {
	if secret == "" {
		secret = DefaultSecret
	}
	if uploadTTL <= 0 {
		uploadTTL = DefaultTokenTTL
	}
	if downloadTTL <= 0 {
		downloadTTL = DefaultTokenTTL
	}

	return &TokenManager{
		secret:           secret,
		uploadTokenTTL:   uploadTTL,
		downloadTokenTTL: downloadTTL,
		tokenRepo:        tokenRepo,
	}
}

func (tm *TokenManager) SetTTLProvider(provider tokenTTLProvider) {
	tm.ttlProvider = provider
}

// GenerateUploadToken emits a unique upload token. New-format tokens include a nonce
// so repeated refreshes within the same second do not recreate the same token string.
func (tm *TokenManager) GenerateUploadToken(nodeID, printerID string) (string, time.Time, error) {
	now := time.Now()
	issuedAt := now.Unix()
	expiresAt := now.Add(time.Duration(tm.currentUploadTTL()) * time.Second).Unix()
	nonce, err := generateTokenNonce()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate upload token nonce: %w", err)
	}

	payload := buildUploadTokenPayload(TokenTypeUpload, nodeID, printerID, issuedAt, expiresAt, nonce)
	signature := tm.generateSignature(payload)
	token := base64.StdEncoding.EncodeToString([]byte(payload + "|" + signature))

	if tm.tokenRepo != nil {
		if preRegRepo, ok := tm.tokenRepo.(interface {
			PreRegisterToken(tokenHash, tokenType, nodeID, resourceID, jobID string, expiresAt time.Time) error
		}); ok {
			tokenHash := generateTokenHash(token)
			err := preRegRepo.PreRegisterToken(tokenHash, TokenTypeUpload, nodeID, printerID, "", time.Unix(expiresAt, 0))
			if err != nil {
				logger.Warn("Failed to pre-register upload token", zap.Error(err))
			}
		}
	}

	// 上传 token 不做批量 revoke：短 TTL + 一次性消费已防重放，且批量 revoke
	// 会误伤"已通过阶段一、正在上传"的在途请求。旧 token 由 TTL 自然失效。

	return token, time.Unix(expiresAt, 0), nil
}

func (tm *TokenManager) GenerateDownloadToken(fileID, jobID, nodeID string) (string, time.Time, error) {
	if tm.tokenRepo != nil {
		if tokenRepo, ok := tm.tokenRepo.(interface {
			RevokeTokensByNodeAndResource(tokenType, nodeID, resourceID string) (int64, error)
		}); ok {
			revokedCount, err := tokenRepo.RevokeTokensByNodeAndResource(TokenTypeDownload, nodeID, fileID)
			if err != nil {
				logger.Warn("Failed to revoke old download tokens for node and file", zap.String("node_id", nodeID), zap.String("file_id", fileID), zap.Error(err))
			} else if revokedCount > 0 {
				logger.Debug("Revoked old download tokens for node and file", zap.Int64("count", revokedCount), zap.String("node_id", nodeID), zap.String("file_id", fileID))
			}
		}
	}

	now := time.Now()
	issuedAt := now.Unix()
	expiresAt := now.Add(time.Duration(tm.currentDownloadTTL()) * time.Second).Unix()

	payload := fmt.Sprintf("%s|%s|%s|%s|%d|%d", TokenTypeDownload, fileID, jobID, nodeID, issuedAt, expiresAt)
	signature := tm.generateSignature(payload)
	token := base64.StdEncoding.EncodeToString([]byte(payload + "|" + signature))

	if tm.tokenRepo != nil {
		if preRegRepo, ok := tm.tokenRepo.(interface {
			PreRegisterToken(tokenHash, tokenType, nodeID, resourceID, jobID string, expiresAt time.Time) error
		}); ok {
			tokenHash := generateTokenHash(token)
			err := preRegRepo.PreRegisterToken(tokenHash, TokenTypeDownload, nodeID, fileID, jobID, time.Unix(expiresAt, 0))
			if err != nil {
				logger.Warn("Failed to pre-register download token", zap.Error(err))
			}
		}
	}

	return token, time.Unix(expiresAt, 0), nil
}

func (tm *TokenManager) ValidateUploadToken(token string) (*UploadTokenPayload, error) {
	return tm.ValidateUploadTokenForContext(token, "", "")
}

// ValidateUploadTokenForContext atomically consumes an upload token only when
// its signed node and printer match the expected terminal context.
func (tm *TokenManager) ValidateUploadTokenForContext(token, expectedNodeID, expectedPrinterID string) (*UploadTokenPayload, error) {
	tokenHash, _, nodeID, printerID, issuedAt, expiresAt, _, _, err := tm.parseUploadToken(token)
	if err != nil {
		return nil, err
	}
	if expectedNodeID != "" && nodeID != expectedNodeID {
		return nil, ErrInvalidContext
	}
	if expectedPrinterID != "" && printerID != expectedPrinterID {
		return nil, ErrInvalidContext
	}

	if tm.tokenRepo != nil {
		err := tm.tokenRepo.MarkTokenAsUsed(tokenHash, TokenTypeUpload, nodeID, printerID, "", time.Unix(expiresAt, 0))
		if err != nil {
			if err.Error() == "token has already been used" {
				return nil, ErrTokenAlreadyUsed
			}
			if err.Error() == "token has been revoked" {
				return nil, &TokenError{Code: "token_revoked", Message: "Token has been revoked"}
			}
			return nil, &TokenError{Code: "database_error", Message: "Failed to verify token usage"}
		}
	}

	return &UploadTokenPayload{
		NodeID:    nodeID,
		PrinterID: printerID,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

func (tm *TokenManager) ValidateDownloadToken(token, expectedFileID, expectedNodeID string) (*DownloadTokenPayload, error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, ErrInvalidFormat
	}

	parts := strings.Split(string(decoded), "|")
	if len(parts) != 7 {
		return nil, ErrInvalidFormat
	}

	tokenType := parts[0]
	fileID := parts[1]
	jobID := parts[2]
	nodeID := parts[3]
	issuedAt, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return nil, ErrInvalidFormat
	}
	expiresAt, err := strconv.ParseInt(parts[5], 10, 64)
	if err != nil {
		return nil, ErrInvalidFormat
	}
	signature := parts[6]

	if tokenType != TokenTypeDownload {
		return nil, ErrInvalidType
	}

	if time.Now().Unix() > expiresAt {
		return nil, ErrTokenExpired
	}

	payload := fmt.Sprintf("%s|%s|%s|%s|%d|%d", tokenType, fileID, jobID, nodeID, issuedAt, expiresAt)
	expectedSignature := tm.generateSignature(payload)
	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		return nil, ErrInvalidSignature
	}

	if expectedFileID != "" && fileID != expectedFileID {
		return nil, ErrInvalidContext
	}
	if expectedNodeID != "" && nodeID != expectedNodeID {
		return nil, ErrInvalidContext
	}

	if tm.tokenRepo != nil {
		tokenHash := generateTokenHash(token)
		// 下载 token 在 TTL 内可重试：Edge 断线重连同一 URL 不应因一次性消费而 401。
		// 失效由 TTL 过期与 RevokeTokensByNodeAndResource（换发时）保证。
		if reader, ok := tm.tokenRepo.(tokenStatusReader); ok {
			_, revoked, found, err := reader.GetTokenStatus(tokenHash)
			if err != nil {
				return nil, &TokenError{Code: "database_error", Message: "Failed to verify token usage"}
			}
			if found && revoked {
				return nil, &TokenError{Code: "token_revoked", Message: "Token has been revoked"}
			}
		} else {
			err := tm.tokenRepo.MarkTokenAsUsed(tokenHash, TokenTypeDownload, nodeID, fileID, jobID, time.Unix(expiresAt, 0))
			if err != nil {
				if err.Error() == "token has already been used" {
					return nil, ErrTokenAlreadyUsed
				}
				if err.Error() == "token has been revoked" {
					return nil, &TokenError{Code: "token_revoked", Message: "Token has been revoked"}
				}
				return nil, &TokenError{Code: "database_error", Message: "Failed to verify token usage"}
			}
		}
	}

	return &DownloadTokenPayload{
		FileID:    fileID,
		JobID:     jobID,
		NodeID:    nodeID,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

func (tm *TokenManager) ValidateDownloadTokenSimple(token string) (*DownloadTokenPayload, error) {
	return tm.ValidateDownloadToken(token, "", "")
}

// VerifyUploadTokenLightweight validates signature and expiry without consuming the token.
func (tm *TokenManager) VerifyUploadTokenLightweight(token string) (*UploadTokenPayload, error) {
	_, _, nodeID, printerID, issuedAt, expiresAt, _, _, err := tm.parseUploadToken(token)
	if err != nil {
		return nil, err
	}

	return &UploadTokenPayload{
		NodeID:    nodeID,
		PrinterID: printerID,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

func (tm *TokenManager) VerifyUploadTokenAvailable(token, expectedNodeID, expectedPrinterID string) (*UploadTokenPayload, error) {
	tokenHash, _, nodeID, printerID, issuedAt, expiresAt, _, _, err := tm.parseUploadToken(token)
	if err != nil {
		return nil, err
	}

	if expectedNodeID != "" && nodeID != expectedNodeID {
		return nil, ErrInvalidContext
	}
	if expectedPrinterID != "" && printerID != expectedPrinterID {
		return nil, ErrInvalidContext
	}

	if inspector, ok := tm.tokenRepo.(tokenStatusReader); ok {
		used, revoked, found, err := inspector.GetTokenStatus(tokenHash)
		if err != nil {
			return nil, &TokenError{Code: "database_error", Message: "Failed to verify token usage"}
		}
		if found && revoked {
			return nil, &TokenError{Code: "token_revoked", Message: "Token has been revoked"}
		}
		if found && used {
			return nil, ErrTokenAlreadyUsed
		}
	}

	return &UploadTokenPayload{
		NodeID:    nodeID,
		PrinterID: printerID,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

func (tm *TokenManager) parseUploadToken(token string) (tokenHash, tokenType, nodeID, printerID string, issuedAt, expiresAt int64, payload, signature string, err error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		err = ErrInvalidFormat
		return
	}

	tokenType, nodeID, printerID, issuedAt, expiresAt, payload, signature, err = parseUploadTokenParts(string(decoded))
	if err != nil {
		return
	}

	if tokenType != TokenTypeUpload {
		err = ErrInvalidType
		return
	}

	if time.Now().Unix() > expiresAt {
		err = ErrTokenExpired
		return
	}

	expectedSignature := tm.generateSignature(payload)
	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		err = ErrInvalidSignature
		return
	}

	tokenHash = generateTokenHash(token)
	return
}

func (tm *TokenManager) generateSignature(payload string) string {
	h := hmac.New(sha256.New, []byte(tm.secret))
	h.Write([]byte(payload))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (tm *TokenManager) currentUploadTTL() int {
	if tm.ttlProvider != nil {
		if settings, err := tm.ttlProvider.Current(); err == nil && settings.UploadTokenTTLSeconds > 0 {
			return settings.UploadTokenTTLSeconds
		}
	}
	return tm.uploadTokenTTL
}

func (tm *TokenManager) currentDownloadTTL() int {
	if tm.ttlProvider != nil {
		if settings, err := tm.ttlProvider.Current(); err == nil && settings.DownloadTokenTTLSeconds > 0 {
			return settings.DownloadTokenTTLSeconds
		}
	}
	return tm.downloadTokenTTL
}

func buildUploadTokenPayload(tokenType, nodeID, printerID string, issuedAt, expiresAt int64, nonce string) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d|%s", tokenType, nodeID, printerID, issuedAt, expiresAt, nonce)
}

func parseUploadTokenParts(decoded string) (tokenType, nodeID, printerID string, issuedAt, expiresAt int64, payload, signature string, err error) {
	parts := strings.Split(decoded, "|")
	if len(parts) != 7 {
		err = ErrInvalidFormat
		return
	}

	tokenType = parts[0]
	nodeID = parts[1]
	printerID = parts[2]
	issuedAt, err = strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		err = ErrInvalidFormat
		return
	}
	expiresAt, err = strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		err = ErrInvalidFormat
		return
	}

	nonce := parts[5]
	signature = parts[6]
	payload = buildUploadTokenPayload(tokenType, nodeID, printerID, issuedAt, expiresAt, nonce)
	return
}

func generateTokenNonce() (string, error) {
	var nonceBytes [16]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonceBytes[:]), nil
}

func generateTokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", hash)
}

func IsTokenError(err error) bool {
	var tokenErr *TokenError
	return errors.As(err, &tokenErr)
}

func GetTokenErrorCode(err error) string {
	var tokenErr *TokenError
	if errors.As(err, &tokenErr) {
		return tokenErr.Code
	}
	return "unknown_error"
}
