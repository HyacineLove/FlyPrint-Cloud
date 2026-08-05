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

type fakeSitePortalAuthenticator struct {
	portal *models.SitePortal
	err    error
}

func (f *fakeSitePortalAuthenticator) GetByCode(code string) (*models.SitePortal, error) {
	if f.err != nil || code != "official" {
		return nil, database.ErrSitePortalUnauthorized
	}
	return f.portal, nil
}

type fakePortalTicketReader struct {
	ticket *models.TerminalTicket
	err    error
}

func (f *fakePortalTicketReader) GetValidByHash(_ string, _ time.Time) (*models.TerminalTicket, error) {
	return f.ticket, f.err
}

type fakePortalSessionMatcher struct {
	matched bool
}

func (f *fakePortalSessionMatcher) Matches(_, _, _ string) (bool, error) {
	return f.matched, nil
}

type fakePortalLoginCompleter struct {
	calls      int
	input      database.CompletePortalLoginInput
	completion *models.PortalLoginCompletion
	err        error
}

func (f *fakePortalLoginCompleter) CompleteLogin(input database.CompletePortalLoginInput) (*models.PortalLoginCompletion, error) {
	f.calls++
	f.input = input
	return f.completion, f.err
}

type fakePortalReadyDispatcher struct {
	connected bool
	nodeID    string
	payload   websocket.PortalSessionReadyPayload
	err       error
}

type fakePortalReadyOutbox struct {
	ids []string
	err error
}

func (f *fakePortalReadyOutbox) MarkDelivered(eventID string) error {
	f.ids = append(f.ids, eventID)
	return f.err
}

func (f *fakePortalReadyDispatcher) IsNodeConnected(_ string) bool {
	return f.connected
}

func (f *fakePortalReadyDispatcher) DispatchPortalSessionReady(nodeID string, payload websocket.PortalSessionReadyPayload) error {
	f.nodeID = nodeID
	f.payload = payload
	return f.err
}

func newSitePortalTestRouter(handler *SitePortalHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("client_type", "site_portal")
		c.Set("site_portal_code", "official")
		c.Next()
	})
	router.POST("/context", handler.Context)
	router.POST("/login-completions", handler.CompleteLogin)
	return router
}

func sitePortalRequest(t *testing.T, router http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-FlyPrint-Site-Portal", "official")
	request.Header.Set("Authorization", "Bearer portal-token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestSitePortalContextValidationDoesNotConsumeTerminalTicket(t *testing.T) {
	selected := "official"
	completer := &fakePortalLoginCompleter{}
	handler := NewSitePortalHandler(
		&fakeSitePortalAuthenticator{portal: &models.SitePortal{Code: "official"}},
		&fakePortalTicketReader{ticket: &models.TerminalTicket{
			NodeID: "edge-1", PrinterID: "printer-1", TerminalSessionID: "session-1",
			TicketHash: "ticket-hash", SelectedEntry: &selected, ExpiresAt: time.Now().Add(time.Minute),
		}},
		&fakePortalSessionMatcher{matched: true},
		completer,
		&fakePortalReadyDispatcher{connected: true},
	)

	recorder := sitePortalRequest(t, newSitePortalTestRouter(handler), "/context", map[string]string{
		"terminal_ticket": "raw-ticket",
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	if completer.calls != 0 {
		t.Fatalf("context validation consumed login: calls=%d", completer.calls)
	}
}

func TestSitePortalLoginCompletionMapsUserThenSendsCredentialFreeReadyMessage(t *testing.T) {
	now := time.Now().UTC()
	selected := "official"
	completer := &fakePortalLoginCompleter{completion: &models.PortalLoginCompletion{
		NodeID: "edge-1", TerminalSessionID: "session-1", CloudUserID: "cloud-user-1",
		SitePortalCode: "official", ClaimBaseURL: "https://portal.example.test",
	}}
	dispatcher := &fakePortalReadyDispatcher{connected: true}
	handler := NewSitePortalHandler(
		&fakeSitePortalAuthenticator{portal: &models.SitePortal{Code: "official"}},
		&fakePortalTicketReader{ticket: &models.TerminalTicket{
			NodeID: "edge-1", TerminalSessionID: "session-1", TicketHash: "ticket-hash",
			SelectedEntry: &selected, ExpiresAt: now.Add(time.Minute),
		}},
		&fakePortalSessionMatcher{matched: true},
		completer,
		dispatcher,
	)

	recorder := sitePortalRequest(t, newSitePortalTestRouter(handler), "/login-completions", map[string]any{
		"terminal_ticket":  "raw-ticket",
		"external_user_id": "external-user-1",
		"display_name":     "张老师",
		"claim_code":       "claim-code-1",
		"claim_expires_at": now.Add(4 * time.Minute),
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	if completer.calls != 1 || dispatcher.nodeID != "edge-1" {
		t.Fatalf("complete calls=%d dispatch node=%q", completer.calls, dispatcher.nodeID)
	}
	if dispatcher.payload.ClaimCode != "claim-code-1" ||
		dispatcher.payload.TerminalSessionID != "session-1" ||
		dispatcher.payload.CloudUserID != "cloud-user-1" {
		t.Fatalf("unexpected ready payload: %#v", dispatcher.payload)
	}
	raw, err := json.Marshal(dispatcher.payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"access_token", "cookie", "password"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("private field %q leaked: %s", forbidden, raw)
		}
	}
}

func TestSitePortalLoginCompletionPersistsNotificationWhenEdgeIsOffline(t *testing.T) {
	selected := "official"
	completer := &fakePortalLoginCompleter{completion: &models.PortalLoginCompletion{
		NodeID: "edge-1", TerminalSessionID: "session-1", CloudUserID: "cloud-user-1",
		SitePortalCode: "official", ClaimBaseURL: "https://portal.example.test", ReadyEventID: "event-1",
	}}
	outbox := &fakePortalReadyOutbox{}
	handler := NewSitePortalHandler(
		&fakeSitePortalAuthenticator{portal: &models.SitePortal{Code: "official"}},
		&fakePortalTicketReader{ticket: &models.TerminalTicket{
			NodeID: "edge-1", TerminalSessionID: "session-1", TicketHash: "ticket-hash",
			SelectedEntry: &selected, ExpiresAt: time.Now().Add(time.Minute),
		}},
		&fakePortalSessionMatcher{matched: true},
		completer,
		&fakePortalReadyDispatcher{connected: false, err: errors.New("edge offline")},
		outbox,
	)

	recorder := sitePortalRequest(t, newSitePortalTestRouter(handler), "/login-completions", map[string]any{
		"terminal_ticket":  "raw-ticket",
		"external_user_id": "external-user-1",
		"display_name":     "张老师",
		"claim_code":       "claim-code-1",
		"claim_expires_at": time.Now().Add(4 * time.Minute),
	})

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	if completer.calls != 1 || len(outbox.ids) != 0 {
		t.Fatalf("offline Edge must leave durable notification pending: calls=%d ids=%v", completer.calls, outbox.ids)
	}
}

func TestSitePortalLoginCompletionKeepsDurableNotificationPendingWhenDispatchFails(t *testing.T) {
	now := time.Now().UTC()
	selected := "official"
	completer := &fakePortalLoginCompleter{completion: &models.PortalLoginCompletion{
		NodeID: "edge-1", TerminalSessionID: "session-1", CloudUserID: "cloud-user-1",
		SitePortalCode: "official", ClaimBaseURL: "https://portal.example.test", ReadyEventID: "event-1",
	}}
	outbox := &fakePortalReadyOutbox{}
	handler := NewSitePortalHandler(
		&fakeSitePortalAuthenticator{portal: &models.SitePortal{Code: "official"}},
		&fakePortalTicketReader{ticket: &models.TerminalTicket{
			NodeID: "edge-1", TerminalSessionID: "session-1", TicketHash: "ticket-hash",
			SelectedEntry: &selected, ExpiresAt: now.Add(time.Minute),
		}},
		&fakePortalSessionMatcher{matched: true},
		completer,
		&fakePortalReadyDispatcher{connected: true, err: errors.New("websocket send failed")},
		outbox,
	)

	recorder := sitePortalRequest(t, newSitePortalTestRouter(handler), "/login-completions", map[string]any{
		"terminal_ticket":  "raw-ticket",
		"external_user_id": "external-user-1",
		"display_name":     "张老师",
		"claim_code":       "claim-code-1",
		"claim_expires_at": now.Add(4 * time.Minute),
	})

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	if len(outbox.ids) != 0 {
		t.Fatalf("failed dispatch must remain pending, marked ids=%v", outbox.ids)
	}
}
