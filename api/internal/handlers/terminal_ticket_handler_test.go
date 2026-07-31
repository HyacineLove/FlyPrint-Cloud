package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
	if !strings.Contains(recorder.Body.String(), "重新尝试") {
		t.Fatal("retryable entry error should render a retry action")
	}
}
