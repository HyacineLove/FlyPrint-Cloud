package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"fly-print-cloud/api/internal/models"

	"github.com/gin-gonic/gin"
)

func TestEntryPageUsesStyledErrorForMissingTicket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/entry", nil)

	(&TerminalTicketHandler{}).EntryPage(context)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("content type = %q, want HTML", contentType)
	}
	body := recorder.Body.String()
	for _, expected := range []string{"二维码无效", "class=card", "刷新二维码"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("styled error page missing %q", expected)
		}
	}
}

func TestBuildSitePortalEntryURLPreservesExistingQueryAndAddsTicket(t *testing.T) {
	redirect, err := buildSitePortalEntryURL("https://portal.example.test/entry?theme=official", "raw ticket")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("theme") != "official" || parsed.Query().Get("terminal_ticket") != "raw ticket" {
		t.Fatalf("unexpected redirect URL: %s", redirect)
	}
}

func TestEntryErrorRetryActionIsExplicit(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	renderEntryError(context, http.StatusServiceUnavailable, "暂时不可用", "请稍后重试。", true)
	if !strings.Contains(recorder.Body.String(), `onclick="location.reload()"`) {
		t.Fatal("retryable entry error should render a retry action")
	}
}

func TestRenderSitePortalSelectionPagePostsSelectedPortal(t *testing.T) {
	recorder := httptest.NewRecorder()
	portals := []*models.SitePortal{
		{Code: "official", DisplayName: "Official Portal", EntryURL: "http://official.test/entry", Enabled: true},
		{Code: "local-test", DisplayName: "Local Test Portal", EntryURL: "http://local.test/entry", Enabled: true},
	}

	renderSitePortalSelectionPage(recorder, "ticket-123", portals, "official")
	body := recorder.Body.String()

	for _, expected := range []string{
		"Official Portal",
		"Local Test Portal",
		"/api/v1/public/terminal-entry/select",
		"ticket-123",
		`data-entry="local-test"`,
		`data-default="true"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("selection page missing %q: %s", expected, body)
		}
	}
}
