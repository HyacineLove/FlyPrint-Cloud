package security

import (
	"fmt"
	"testing"
	"time"

	"fly-print-cloud/api/internal/business"
)

type testTokenRecord struct {
	tokenType  string
	nodeID     string
	resourceID string
	jobID      string
	expiresAt  time.Time
	revoked    bool
	used       bool
}

type testTokenRepo struct {
	records map[string]*testTokenRecord
}

func newTestTokenRepo() *testTokenRepo {
	return &testTokenRepo{records: map[string]*testTokenRecord{}}
}

func (r *testTokenRepo) PreRegisterToken(tokenHash, tokenType, nodeID, resourceID, jobID string, expiresAt time.Time) error {
	if _, ok := r.records[tokenHash]; ok {
		return nil
	}
	r.records[tokenHash] = &testTokenRecord{
		tokenType:  tokenType,
		nodeID:     nodeID,
		resourceID: resourceID,
		jobID:      jobID,
		expiresAt:  expiresAt,
	}
	return nil
}

func (r *testTokenRepo) MarkTokenAsUsed(tokenHash, tokenType, nodeID, resourceID, jobID string, expiresAt time.Time) error {
	if existing, ok := r.records[tokenHash]; ok {
		if existing.revoked {
			return fmt.Errorf("token has been revoked")
		}
		if existing.used {
			return fmt.Errorf("token has already been used")
		}
		existing.used = true
		return nil
	}

	r.records[tokenHash] = &testTokenRecord{
		tokenType:  tokenType,
		nodeID:     nodeID,
		resourceID: resourceID,
		jobID:      jobID,
		expiresAt:  expiresAt,
		used:       true,
	}
	return nil
}

func (r *testTokenRepo) RevokeTokensByNodeAndResource(tokenType, nodeID, resourceID string) (int64, error) {
	var revoked int64
	for _, record := range r.records {
		if record.tokenType == tokenType && record.nodeID == nodeID && record.resourceID == resourceID && !record.revoked {
			record.revoked = true
			revoked++
		}
	}
	return revoked, nil
}

func (r *testTokenRepo) RevokeTokensByNodeResourceAndJob(tokenType, nodeID, resourceID, jobID string) (int64, error) {
	var revoked int64
	for _, record := range r.records {
		if record.tokenType == tokenType && record.nodeID == nodeID && record.resourceID == resourceID && record.jobID == jobID && !record.revoked {
			record.revoked = true
			revoked++
		}
	}
	return revoked, nil
}

func (r *testTokenRepo) GetTokenStatus(tokenHash string) (bool, bool, bool, error) {
	record, ok := r.records[tokenHash]
	if !ok {
		return false, false, false, nil
	}
	return record.used, record.revoked, true, nil
}

func TestGenerateUploadTokenProducesUniqueTokensForRapidRefresh(t *testing.T) {
	repo := newTestTokenRepo()
	tm := NewTokenManager("secret", 180, 180, repo)

	first, _, err := tm.GenerateUploadToken("node-1", "printer-1")
	if err != nil {
		t.Fatalf("first token: %v", err)
	}

	second, _, err := tm.GenerateUploadToken("node-1", "printer-1")
	if err != nil {
		t.Fatalf("second token: %v", err)
	}

	if first == second {
		t.Fatalf("expected rapid refresh tokens to be unique")
	}

	if _, err := tm.ValidateUploadToken(second); err != nil {
		t.Fatalf("expected latest token to validate, got %v", err)
	}

	// 上传 token 不做批量 revoke：短 TTL + 一次性消费防重放，
	// 避免在途上传被并发刷新打断（见 GenerateUploadToken 注释）。
	if _, err := tm.VerifyUploadTokenAvailable(first, "node-1", "printer-1"); err != nil {
		t.Fatalf("previous token should remain available within TTL (no batch revoke), got %v", err)
	}
}

func TestVerifyUploadTokenAvailableRejectsUsedToken(t *testing.T) {
	repo := newTestTokenRepo()
	tm := NewTokenManager("secret", 180, 180, repo)

	token, _, err := tm.GenerateUploadToken("node-1", "printer-1")
	if err != nil {
		t.Fatalf("GenerateUploadToken() error = %v", err)
	}

	if _, err := tm.ValidateUploadToken(token); err != nil {
		t.Fatalf("ValidateUploadToken() error = %v", err)
	}

	if _, err := tm.VerifyUploadTokenAvailable(token, "node-1", "printer-1"); GetTokenErrorCode(err) != "token_already_used" {
		t.Fatalf("error code = %s, want token_already_used", GetTokenErrorCode(err))
	}
}

func TestVerifyUploadTokenAvailableRejectsWrongContext(t *testing.T) {
	repo := newTestTokenRepo()
	tm := NewTokenManager("secret", 180, 180, repo)

	token, _, err := tm.GenerateUploadToken("node-1", "printer-1")
	if err != nil {
		t.Fatalf("GenerateUploadToken() error = %v", err)
	}

	if _, err := tm.VerifyUploadTokenAvailable(token, "node-2", "printer-1"); GetTokenErrorCode(err) != "invalid_context" {
		t.Fatalf("error code = %s, want invalid_context", GetTokenErrorCode(err))
	}
}

func TestValidateUploadTokenForContextRejectsMismatchWithoutConsuming(t *testing.T) {
	repo := newTestTokenRepo()
	tm := NewTokenManager("secret", 180, 180, repo)

	token, _, err := tm.GenerateUploadToken("node-1", "printer-1")
	if err != nil {
		t.Fatalf("GenerateUploadToken() error = %v", err)
	}
	if _, err := tm.ValidateUploadTokenForContext(token, "node-2", "printer-1"); GetTokenErrorCode(err) != "invalid_context" {
		t.Fatalf("error code = %s, want invalid_context", GetTokenErrorCode(err))
	}
	if _, err := tm.ValidateUploadTokenForContext(token, "node-1", "printer-1"); err != nil {
		t.Fatalf("matching context should still consume the token: %v", err)
	}
	if _, err := tm.ValidateUploadTokenForContext(token, "node-1", "printer-1"); GetTokenErrorCode(err) != "token_already_used" {
		t.Fatalf("second consume error code = %s, want token_already_used", GetTokenErrorCode(err))
	}
}

func TestGenerateTokensUseDynamicTTLProvider(t *testing.T) {
	t.Parallel()

	tm := NewTokenManager("secret", 180, 180, nil)
	tm.SetTTLProvider(staticTTLProvider{uploadTTL: 45, downloadTTL: 75})

	uploadToken, uploadExpiresAt, err := tm.GenerateUploadToken("node-1", "printer-1")
	if err != nil {
		t.Fatalf("GenerateUploadToken() error = %v", err)
	}
	uploadPayload, err := tm.VerifyUploadTokenLightweight(uploadToken)
	if err != nil {
		t.Fatalf("VerifyUploadTokenLightweight() error = %v", err)
	}
	if got := uploadPayload.ExpiresAt - uploadPayload.IssuedAt; got != 45 {
		t.Fatalf("upload ttl = %d, want %d", got, 45)
	}
	if uploadExpiresAt.Unix() != uploadPayload.ExpiresAt {
		t.Fatalf("upload expiresAt = %d, want %d", uploadExpiresAt.Unix(), uploadPayload.ExpiresAt)
	}

	downloadToken, _, err := tm.GenerateDownloadToken("file-1", "job-1", "node-1")
	if err != nil {
		t.Fatalf("GenerateDownloadToken() error = %v", err)
	}
	downloadPayload, err := tm.ValidateDownloadTokenSimple(downloadToken)
	if err != nil {
		t.Fatalf("ValidateDownloadTokenSimple() error = %v", err)
	}
	if got := downloadPayload.ExpiresAt - downloadPayload.IssuedAt; got != 75 {
		t.Fatalf("download ttl = %d, want %d", got, 75)
	}
}

func TestGenerateDownloadTokenDoesNotRevokeAnotherJobToken(t *testing.T) {
	repo := newTestTokenRepo()
	tm := NewTokenManager("secret", 180, 180, repo)

	first, _, err := tm.GenerateDownloadToken("file-1", "job-1", "node-1")
	if err != nil {
		t.Fatalf("first token: %v", err)
	}
	second, _, err := tm.GenerateDownloadToken("file-1", "job-2", "node-1")
	if err != nil {
		t.Fatalf("second token: %v", err)
	}
	if _, err := tm.ValidateDownloadTokenSimple(first); err != nil {
		t.Fatalf("job-1 token was revoked by job-2 token: %v", err)
	}
	if _, err := tm.ValidateDownloadTokenSimple(second); err != nil {
		t.Fatalf("job-2 token should validate: %v", err)
	}
}

type staticTTLProvider struct {
	uploadTTL   int
	downloadTTL int
}

func (p staticTTLProvider) Current() (business.Settings, error) {
	return business.Settings{
		UploadMaxSizeBytes:      1024,
		MaxDocumentPages:        5,
		UploadTokenTTLSeconds:   p.uploadTTL,
		DownloadTokenTTLSeconds: p.downloadTTL,
		AllowedExtensions:       []string{".pdf"},
	}, nil
}
