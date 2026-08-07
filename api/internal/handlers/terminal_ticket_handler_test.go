package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fly-print-cloud/api/internal/models"
	"github.com/gin-gonic/gin"
)

func TestEntryPageReadsT1FromFragmentOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/entry?terminal_ticket=must-not-work", nil)
	(&TerminalTicketHandler{}).EntryPage(c)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "location.href).hash") {
		t.Fatalf("unexpected page: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "terminal_ticket") {
		t.Fatal("entry page must not accept terminal_ticket query")
	}
}
func TestSelectionPageDoesNotEmbedCredential(t *testing.T) {
	w := httptest.NewRecorder()
	renderSitePortalSelectionPage(w, []*models.SitePortal{{Code: "official", DisplayName: "Official", Enabled: true}}, "official", time.Now().Add(time.Minute))
	if !strings.Contains(w.Body.String(), "/api/v1/public/terminal-entry/select") {
		t.Fatal("missing select endpoint")
	}
	if strings.Contains(w.Body.String(), "terminal_ticket") {
		t.Fatal("selection page leaked legacy ticket")
	}
}
func TestPortalHandoffUsesPostForm(t *testing.T) {
	page := renderPortalHandoffPage("https://portal.test/entry", "t3", "attempt")
	if !strings.Contains(page, `method="post"`) || strings.Contains(page, "?handoff=") {
		t.Fatalf("T3 handoff is not POST-only: %s", page)
	}
	_ = http.MethodPost
}
