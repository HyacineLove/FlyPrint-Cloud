package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"

	"github.com/gin-gonic/gin"
)

type fakePrintAuthorizer struct {
	input  models.PrintAuthorizationInput
	result *models.PrintAuthorizationResult
	err    error
}

func (f *fakePrintAuthorizer) Authorize(input models.PrintAuthorizationInput) (*models.PrintAuthorizationResult, error) {
	f.input = input
	return f.result, f.err
}

func performPrintAuthorizationRequest(t *testing.T, authorizer PrintAuthorizer, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewPortalPrintHandler(authorizer)
	router.POST("/api/v1/edge/:node_id/print-authorizations", handler.Authorize)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/edge/edge-1/print-authorizations",
		bytes.NewBufferString(body),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

const validPrintAuthorizationBody = `{
	"confirmation_id":"confirm-1",
	"terminal_session_id":"session-1",
	"site_portal_code":"official",
	"local_file_id":"file-1",
	"file_display_name":"课件.pdf",
	"page_count":3,
	"copies":2,
	"paper_size":"A4",
	"color_mode":"color",
	"duplex_mode":"longedge",
	"printer_id":"11111111-1111-1111-1111-111111111111"
}`

func TestPortalPrintHandlerReturnsAuthorizationResult(t *testing.T) {
	authorizer := &fakePrintAuthorizer{
		result: &models.PrintAuthorizationResult{
			JobID:         "33333333-3333-3333-3333-333333333333",
			ReservedQuota: 8,
			QuotaBalance:  42,
		},
	}
	recorder := performPrintAuthorizationRequest(t, authorizer, validPrintAuthorizationBody)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if authorizer.input.NodeID != "edge-1" || authorizer.input.SitePortalCode != "official" {
		t.Fatalf("authorizer input = %#v", authorizer.input)
	}
	expected := `{"allowed":true,"job_id":"33333333-3333-3333-3333-333333333333","quota_balance":42,"reserved_quota":8}`
	if recorder.Body.String() != expected {
		t.Fatalf("body = %s, want %s", recorder.Body.String(), expected)
	}
}

func TestPortalPrintHandlerRejectsQuotaWithoutJobID(t *testing.T) {
	authorizer := &fakePrintAuthorizer{err: database.ErrPrintQuotaInsufficient}
	recorder := performPrintAuthorizationRequest(t, authorizer, validPrintAuthorizationBody)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("job_id")) {
		t.Fatalf("quota denial exposed a job id: %s", recorder.Body.String())
	}
	expected := `{"allowed":false,"error_code":"print_quota_insufficient","message":"打印额度不足，请联系管理员增加额度。"}`
	if recorder.Body.String() != expected {
		t.Fatalf("body = %s, want %s", recorder.Body.String(), expected)
	}
}

func TestPortalPrintHandlerRejectsInvalidSession(t *testing.T) {
	authorizer := &fakePrintAuthorizer{err: database.ErrPrintAuthorizationSessionInvalid}
	recorder := performPrintAuthorizationRequest(t, authorizer, validPrintAuthorizationBody)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"error_code":"terminal_session_invalid"`)) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestPortalPrintHandlerRejectsMalformedRequestBeforeRepository(t *testing.T) {
	authorizer := &fakePrintAuthorizer{
		err: errors.New("repository must not be called"),
	}
	recorder := performPrintAuthorizationRequest(t, authorizer, `{"copies":0}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if authorizer.input.NodeID != "" {
		t.Fatalf("repository was called with %#v", authorizer.input)
	}
}
