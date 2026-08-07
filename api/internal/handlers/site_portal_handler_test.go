package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/websocket"

	"github.com/gin-gonic/gin"
)

type fakeSitePortalAuthenticator struct{ portal *models.SitePortal }

func (f *fakeSitePortalAuthenticator) GetByCode(code string) (*models.SitePortal, error) {
	if code != "official" {
		return nil, database.ErrSitePortalUnauthorized
	}
	return f.portal, nil
}

type fakePortalEntries struct {
	attempt      *models.EntryPortalAttempt
	entry        *models.EntrySession
	err          error
	consumedHash string
	validated    bool
}

func (f *fakePortalEntries) ConsumeT3(hash, _ string, _ time.Time) (*models.EntryPortalAttempt, *models.EntrySession, error) {
	f.consumedHash = hash
	return f.attempt, f.entry, f.err
}
func (f *fakePortalEntries) ValidateClaim(_, _, _ string, _ int64, _ time.Time) error {
	f.validated = true
	return f.err
}

type fakePortalLoginCompleter struct {
	input      database.CompletePortalLoginInput
	completion *models.PortalLoginCompletion
	err        error
}

func (f *fakePortalLoginCompleter) CompleteLogin(in database.CompletePortalLoginInput) (*models.PortalLoginCompletion, error) {
	f.input = in
	return f.completion, f.err
}

type fakePortalDispatcher struct{}

func (*fakePortalDispatcher) DispatchPortalSessionReady(string, websocket.PortalSessionReadyPayload) error {
	return nil
}

func newSitePortalTestRouter(h *SitePortalHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("client_type", "site_portal"); c.Set("site_portal_code", "official") })
	r.POST("/context", h.Context)
	r.POST("/claims/validate", h.ValidateClaim)
	return r
}

func TestSitePortalContextConsumesOnlyHandoff(t *testing.T) {
	entries := &fakePortalEntries{attempt: &models.EntryPortalAttempt{ID: "attempt-1"}, entry: &models.EntrySession{NodeID: "node-1", PrinterID: "printer-1", TerminalSessionID: "session-1", QRGeneration: 7, ExpiresAt: time.Now().Add(time.Minute)}}
	h := NewSitePortalHandler(&fakeSitePortalAuthenticator{portal: &models.SitePortal{Code: "official", Enabled: true}}, entries, &fakePortalLoginCompleter{}, &fakePortalDispatcher{})
	body, _ := json.Marshal(map[string]string{"handoff": "t3-secret"})
	w := httptest.NewRecorder()
	newSitePortalTestRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/context", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if entries.consumedHash != ticketHash("t3-secret") {
		t.Fatal("handoff was not hashed before storage lookup")
	}
	if bytes.Contains(w.Body.Bytes(), []byte("t3-secret")) {
		t.Fatal("handoff leaked in context response")
	}
}

func TestValidateClaimRejectsInvalidRoot(t *testing.T) {
	entries := &fakePortalEntries{err: errors.New("invalid")}
	h := NewSitePortalHandler(&fakeSitePortalAuthenticator{portal: &models.SitePortal{Code: "official", Enabled: true}}, entries, &fakePortalLoginCompleter{}, &fakePortalDispatcher{})
	w := httptest.NewRecorder()
	newSitePortalTestRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/claims/validate", bytes.NewBufferString(`{"claim_code":"claim","node_id":"n","terminal_session_id":"s","qr_generation":1}`)))
	if w.Code != http.StatusGone {
		t.Fatalf("status=%d", w.Code)
	}
}
