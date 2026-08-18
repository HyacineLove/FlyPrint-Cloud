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

func TestEntryBootstrapBindsPollingToAcquiredEntry(t *testing.T) {
	if !strings.Contains(entryBootstrapPage, "entry_session_id") {
		t.Fatal("entry bootstrap must bind status polling to the entry session returned by acquire")
	}
	if !strings.Contains(entryBootstrapPage, "JSON.stringify({entry_session_id") {
		t.Fatal("entry status polling must send its entry session binding")
	}
	if !strings.Contains(entryBootstrapPage, "/entry/options?entry_session_id=") {
		t.Fatal("entry options navigation must retain the acquired entry session binding")
	}
}

func TestSelectionPageBindsSelectionToEntry(t *testing.T) {
	w := httptest.NewRecorder()
	renderSitePortalSelectionPage(w, "entry-1", []*models.SitePortal{{Code: "official", DisplayName: "官方入口", Enabled: true}}, "official", time.Now().Add(time.Minute))
	page := w.Body.String()
	if !strings.Contains(page, "entry_session_id") {
		t.Fatal("entry selection must retain the entry session binding")
	}
}

func TestSelectionPageDoesNotEmbedCredential(t *testing.T) {
	w := httptest.NewRecorder()
	renderSitePortalSelectionPage(w, "entry-1", []*models.SitePortal{{Code: "internal-campus", DisplayName: "校园打印", Enabled: true}}, "internal-campus", time.Now().Add(time.Minute))
	page := w.Body.String()
	if !strings.Contains(page, "/api/v1/public/terminal-entry/select") {
		t.Fatal("missing select endpoint")
	}
	if strings.Contains(page, "terminal_ticket") {
		t.Fatal("selection page leaked legacy ticket")
	}
	if !strings.Contains(page, `class="portal-name">校园打印</span>`) {
		t.Fatal("selection page must show the portal display name")
	}
	if strings.Contains(page, `class="portal-name">internal-campus</span>`) {
		t.Fatal("selection page must not show the internal portal code")
	}
	if !strings.Contains(page, `data-entry="internal-campus"`) {
		t.Fatal("selection page must retain the internal entry value for submission")
	}
	if !strings.Contains(page, "let submitting=false") || !strings.Contains(page, "if(submitting)return") {
		t.Fatal("selection page must guard against duplicate submissions")
	}
}

func TestSelectionPageAutoSubmitsWhenOnlyOnePortal(t *testing.T) {
	w := httptest.NewRecorder()
	renderSitePortalSelectionPage(w, "entry-1", []*models.SitePortal{{Code: "liwa", DisplayName: "丽娃云聘", Enabled: true}}, "liwa", time.Now().Add(time.Minute))
	page := w.Body.String()
	if !strings.Contains(page, "buttons.length===1") || !strings.Contains(page, "submit(buttons[0])") {
		t.Fatal("single portal must auto-submit without waiting for a click")
	}
	if !strings.Contains(page, "正在进入打印入口") {
		t.Fatal("single portal page must look like a direct handoff")
	}
}

func TestSelectionPageWaitsForClickWhenMultiplePortals(t *testing.T) {
	w := httptest.NewRecorder()
	renderSitePortalSelectionPage(w, "entry-1", []*models.SitePortal{
		{Code: "liwa", DisplayName: "丽娃云聘", Enabled: true},
		{Code: "campus", DisplayName: "校园打印", Enabled: true},
	}, "liwa", time.Now().Add(time.Minute))
	page := w.Body.String()
	if !strings.Contains(page, `data-entry="liwa"`) || !strings.Contains(page, `data-entry="campus"`) {
		t.Fatal("multiple portals must remain selectable")
	}
	if !strings.Contains(page, "if(buttons.length===1)") {
		t.Fatal("multiple portals must keep the single-entry auto-submit guard")
	}
	if strings.Contains(page, "正在进入打印入口") {
		t.Fatal("multiple portals must not auto-handoff copy")
	}
	if !strings.Contains(page, "选择打印入口") {
		t.Fatal("multiple portals must keep the selection copy")
	}
}

func TestSelectionPageEscapesPortalDisplayName(t *testing.T) {
	w := httptest.NewRecorder()
	renderSitePortalSelectionPage(w, "entry-1", []*models.SitePortal{{Code: "safe-code", DisplayName: `<img src=x onerror=alert(1)>`, Enabled: true}}, "", time.Now().Add(time.Minute))
	page := w.Body.String()
	if strings.Contains(page, `<img src=x onerror=alert(1)>`) {
		t.Fatal("selection page must escape portal display names")
	}
	if !strings.Contains(page, `class="portal-name">&lt;img src=x onerror=alert(1)&gt;</span>`) {
		t.Fatal("selection page lost the escaped portal display name")
	}
}

func TestPortalHandoffUsesPostForm(t *testing.T) {
	page := renderPortalHandoffPage("https://portal.test/entry", "t3", "attempt")
	if !strings.Contains(page, `method="post"`) || strings.Contains(page, "?handoff=") {
		t.Fatalf("T3 handoff is not POST-only: %s", page)
	}
	_ = http.MethodPost
}
