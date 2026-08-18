package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/websocket"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	portalHandoffTTL   = 90 * time.Second
	entryAcquireCookie = "flyprint_entry_acquire"
	entryCookie        = "flyprint_entry"
)

var sitePortalCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

type TerminalTicketHandler struct {
	entries      *database.EntrySessionRepository
	printers     *database.PrinterRepository
	edgeNodes    *database.EdgeNodeRepository
	sitePortals  *database.SitePortalRepository
	wsManager    *websocket.ConnectionManager
	cookieSecure bool
}

func NewTerminalTicketHandler(entries *database.EntrySessionRepository, printers *database.PrinterRepository, edgeNodes *database.EdgeNodeRepository, wsManager *websocket.ConnectionManager, sitePortals *database.SitePortalRepository, cookieSecure bool) *TerminalTicketHandler {
	return &TerminalTicketHandler{entries: entries, printers: printers, edgeNodes: edgeNodes, wsManager: wsManager, sitePortals: sitePortals, cookieSecure: cookieSecure}
}

// EntryPage never reads T1 from the query string.  The QR fragment is erased
// by the browser before POST /acquire receives it, keeping T1 out of Referer,
// history, proxy logs, and server access logs.
func (h *TerminalTicketHandler) EntryPage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(entryBootstrapPage))
}

type acquireEntryRequest struct {
	Ticket string `json:"ticket" binding:"required"`
}

type entryStatusRequest struct {
	EntrySessionID string `json:"entry_session_id" binding:"required"`
}

func (h *TerminalTicketHandler) Acquire(c *gin.Context) {
	var request acquireEntryRequest
	if err := c.ShouldBindJSON(&request); err != nil || strings.TrimSpace(request.Ticket) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "entry_invalid"})
		return
	}
	acquireRaw, acquireHash, err := newEntrySecret()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "entry_unavailable"})
		return
	}
	commandID := uuid.NewString()
	entry, err := h.entries.Acquire(ticketHash(strings.TrimSpace(request.Ticket)), acquireHash, commandID, time.Now())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "qr_already_used"})
		return
	}
	if h.wsManager == nil || h.wsManager.DispatchTerminalMask(entry.NodeID, commandID, websocket.TerminalMaskPayload{EntrySessionID: entry.ID, TerminalSessionID: entry.TerminalSessionID, QRGeneration: entry.QRGeneration, ExpiresAt: entry.ExpiresAt}) != nil {
		// Parent is invalidated rather than leaving a browser lease that cannot
		// ever receive its visual confirmation.
		_ = h.entries.InvalidateForNode(entry.NodeID)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "terminal_unavailable"})
		return
	}
	h.setCookie(c, entryAcquireCookie, acquireRaw, entry.ExpiresAt)
	c.JSON(http.StatusAccepted, gin.H{"status": "mask_pending", "entry_session_id": entry.ID})
}

func (h *TerminalTicketHandler) EntryStatus(c *gin.Context) {
	var request entryStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil || strings.TrimSpace(request.EntrySessionID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "entry_invalid"})
		return
	}
	lease, err := c.Cookie(entryAcquireCookie)
	if err != nil || lease == "" {
		c.JSON(http.StatusGone, gin.H{"error": "entry_invalid"})
		return
	}
	leaseHash := ticketHash(lease)
	entry, err := h.entries.GetByAcquire(leaseHash, strings.TrimSpace(request.EntrySessionID), time.Now())
	if err != nil {
		c.JSON(http.StatusGone, gin.H{"error": "entry_invalid"})
		return
	}
	if entry.MaskConfirmedAt == nil {
		c.JSON(http.StatusAccepted, gin.H{"status": "mask_pending"})
		return
	}
	t2Raw, t2Hash, err := newEntrySecret()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "entry_unavailable"})
		return
	}
	entry, err = h.entries.Activate(leaseHash, entry.ID, t2Hash, time.Now())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "entry_invalid"})
		return
	}
	h.clearCookie(c, entryAcquireCookie)
	h.setCookie(c, entryCookie, t2Raw, entry.ExpiresAt)
	c.JSON(http.StatusOK, gin.H{"status": "entry_active", "entry_session_id": entry.ID})
}

func (h *TerminalTicketHandler) SelectPage(c *gin.Context) {
	entry, portals, err := h.loadActiveEntry(c, strings.TrimSpace(c.Query("entry_session_id")))
	if err != nil {
		renderEntryError(c, http.StatusGone, "二维码已失效", "请返回终端刷新二维码后重新扫码。", false)
		return
	}
	renderSitePortalSelectionPage(c.Writer, entry.ID, portals.Portals, portals.DefaultPortalCode, entry.ExpiresAt)
}

type selectTerminalEntryRequest struct {
	Entry          string `json:"entry" binding:"required"`
	EntrySessionID string `json:"entry_session_id" binding:"required"`
}

// SelectEntry creates a short lived T3 and hands it to the Portal in a form
// body.  JSON redirects are intentionally not used: a bearer T3 must never be
// copied into an address bar, history record, or Referer header.
func (h *TerminalTicketHandler) SelectEntry(c *gin.Context) {
	var req selectTerminalEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil || !sitePortalCodePattern.MatchString(req.Entry) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_terminal_entry"})
		return
	}
	entry, portals, err := h.loadActiveEntry(c, strings.TrimSpace(req.EntrySessionID))
	if err != nil {
		c.JSON(http.StatusGone, gin.H{"error": "entry_invalid"})
		return
	}
	portal := findSitePortal(portals, req.Entry)
	if portal == nil || !portal.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "terminal_entry_unavailable"})
		return
	}
	if assigned, assignErr := h.sitePortals.IsAssignedToNode(entry.NodeID, req.Entry); assignErr != nil || !assigned {
		c.JSON(http.StatusBadRequest, gin.H{"error": "terminal_entry_unavailable"})
		return
	}
	raw, hash, err := newEntrySecret()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "entry_unavailable"})
		return
	}
	t2, cookieErr := c.Cookie(entryCookie)
	if cookieErr != nil {
		c.JSON(http.StatusGone, gin.H{"error": "entry_invalid"})
		return
	}
	attempt, err := h.entries.CreateAttempt(ticketHash(t2), req.Entry, hash, time.Now().Add(portalHandoffTTL), time.Now())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "entry_invalid"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(renderPortalHandoffPage(portal.EntryURL, raw, attempt.ID)))
}

func (h *TerminalTicketHandler) loadActiveEntry(c *gin.Context, entryID string) (*models.EntrySession, *models.EdgeSitePortalConfig, error) {
	if strings.TrimSpace(entryID) == "" {
		return nil, nil, database.ErrEntrySessionInvalid
	}
	raw, err := c.Cookie(entryCookie)
	if err != nil || raw == "" {
		return nil, nil, database.ErrEntrySessionInvalid
	}
	entry, err := h.entries.GetActiveByT2(ticketHash(raw), entryID, time.Now())
	if err != nil {
		return nil, nil, err
	}
	node, err := h.edgeNodes.GetEdgeNodeByID(entry.NodeID)
	if err != nil || node == nil || !node.Enabled {
		return nil, nil, database.ErrEntrySessionInvalid
	}
	printer, err := h.printers.GetPrinterByID(entry.PrinterID)
	if err != nil || printer == nil || !printer.Enabled || printer.EdgeNodeID != entry.NodeID {
		return nil, nil, database.ErrEntrySessionInvalid
	}
	portals, err := h.sitePortals.GetEdgeSitePortalConfig(entry.NodeID)
	if err != nil || portals == nil {
		return nil, nil, err
	}
	active := make([]*models.SitePortal, 0, len(portals.Portals))
	for _, p := range portals.Portals {
		if p != nil && p.Enabled {
			active = append(active, p)
		}
	}
	portals.Portals = active
	if len(active) == 0 {
		return nil, nil, database.ErrEntrySessionInvalid
	}
	return entry, portals, nil
}

func findSitePortal(config *models.EdgeSitePortalConfig, code string) *models.SitePortal {
	for _, p := range config.Portals {
		if p != nil && p.Code == code {
			return p
		}
	}
	return nil
}

func (h *TerminalTicketHandler) setCookie(c *gin.Context, name, value string, expires time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: value, Path: "/", Expires: expires, HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode})
}
func (h *TerminalTicketHandler) clearCookie(c *gin.Context, name string) {
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode})
}

func newEntrySecret() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, ticketHash(raw), nil
}
func ticketHash(raw string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) }

func renderSitePortalSelectionPage(w http.ResponseWriter, entryID string, portals []*models.SitePortal, defaultCode string, _ time.Time) {
	var options strings.Builder
	for _, p := range portals {
		defaultAttr := ""
		if p.Code == defaultCode {
			defaultAttr = ` data-default="true"`
		}
		fmt.Fprintf(&options, `<button type="button" class="portal-option" data-entry="%s"%s><span class="portal-name">%s</span><span class="portal-arrow" aria-hidden="true">›</span></button>`, html.EscapeString(p.Code), defaultAttr, html.EscapeString(p.DisplayName))
	}
	title := "选择打印入口"
	description := "请选择本次打印使用的入口。"
	if len(portals) == 1 {
		title = "正在进入打印入口"
		description = "正在进入，请稍候…"
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"><meta name="referrer" content="no-referrer"><title>`+html.EscapeString(title)+`</title><style>`+entryPageStyle+`</style></head><body><main class="card" aria-labelledby="page-title"><p class="eyebrow">飞印打印</p><h1 id="page-title">`+html.EscapeString(title)+`</h1><p class="description">`+html.EscapeString(description)+`</p><p class="status" role="status" aria-live="polite"></p><section class="portal-options" aria-label="打印入口">`+options.String()+`</section><p class="error" role="alert" aria-live="assertive"></p></main><script>(async()=>{const entrySessionId=`+strconv.Quote(entryID)+`;const buttons=[...document.querySelectorAll('.portal-option')];const error=document.querySelector('.error');const status=document.querySelector('.status');const options=document.querySelector('.portal-options');let submitting=false;const fail=(message)=>{status.textContent='';error.textContent=message;submitting=false;buttons.forEach((item)=>{item.disabled=false})};const submit=async(button)=>{if(submitting)return;submitting=true;buttons.forEach((item)=>{item.disabled=true});status.textContent='正在进入，请稍候…';try{const response=await fetch('/api/v1/public/terminal-entry/select',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({entry:button.dataset.entry,entry_session_id:entrySessionId})});if(!response.ok){fail('入口已失效，请返回终端刷新二维码后重新扫码。');return}document.open();document.write(await response.text());document.close()}catch{fail('网络连接失败，请返回终端刷新二维码后重新扫码。')}};for(const button of buttons)button.addEventListener('click',()=>submit(button));if(buttons.length===1){if(options)options.hidden=true;await submit(buttons[0])}})()</script></body></html>`)
}

func renderPortalHandoffPage(entryURL, t3, attemptID string) string {
	return `<!doctype html><meta charset="utf-8"><meta name="referrer" content="no-referrer"><form id="handoff" method="post" action="` + html.EscapeString(entryURL) + `"><input type="hidden" name="handoff" value="` + html.EscapeString(t3) + `"><input type="hidden" name="attempt_id" value="` + html.EscapeString(attemptID) + `"></form><script>document.getElementById('handoff').submit()</script>`
}

const entryBootstrapPage = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>正在进入打印入口</title><style>` + entryPageStyle + `</style><main class="card"><h1>正在确认终端</h1><p id="message">请稍候…</p></main><script>(async()=>{const t=new URL(location.href).hash.match(/(?:^#|[&#])t=([^&]+)/)?.[1];history.replaceState(null,'','/entry');if(!t){document.getElementById('message').textContent='二维码无效，请返回终端刷新。';return}let r=await fetch('/api/v1/public/terminal-entry/acquire',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ticket:decodeURIComponent(t)})});if(!r.ok){document.getElementById('message').textContent='二维码已被使用，请在终端刷新。';return}const acquired=await r.json().catch(()=>null);const entrySessionId=String(acquired?.entry_session_id||'').trim();if(!entrySessionId){document.getElementById('message').textContent='终端会话已失效，请重新扫码。';return}for(;;){await new Promise(x=>setTimeout(x,700));r=await fetch('/api/v1/public/terminal-entry/status',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({entry_session_id:entrySessionId}),cache:'no-store'});if(r.status===202)continue;if(!r.ok){document.getElementById('message').textContent='终端会话已失效，请重新扫码。';return}location.replace('/entry/options?entry_session_id='+encodeURIComponent(entrySessionId));return}})()</script></html>`
const entryPageStyle = `*{box-sizing:border-box}body{margin:0;min-height:100vh;background:#f5f7fb;color:#172033;font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;display:flex;align-items:center;justify-content:center;padding:20px}.card{width:min(440px,100%);background:#fff;border:1px solid #e4e8f0;border-radius:20px;padding:24px;box-shadow:0 12px 36px rgba(23,32,51,.08)}.eyebrow{margin:0 0 8px;color:#356ae6;font-size:13px;font-weight:700;letter-spacing:.04em}.card h1{margin:0;color:#172033;font-size:25px;line-height:1.3}.description{margin:8px 0 0;color:#667085;font-size:15px;line-height:1.6}.status{min-height:24px;margin:18px 0 0;color:#356ae6;font-size:14px}.status:empty{display:none}.portal-options{display:grid;gap:12px;margin-top:20px}.portal-options[hidden]{display:none}.portal-option{display:flex;align-items:center;justify-content:space-between;width:100%;min-height:64px;padding:16px;border:1px solid #d8deea;border-radius:14px;background:#fff;color:#172033;font:600 17px/1.35 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;text-align:left;cursor:pointer;-webkit-tap-highlight-color:transparent;transition:border-color .15s,box-shadow .15s,background .15s}.portal-option:active{background:#f5f8ff}.portal-option[data-default=true]{border-color:#356ae6;box-shadow:0 0 0 3px rgba(53,106,230,.1)}.portal-option:disabled{background:#f7f8fa;color:#98a2b3;cursor:wait;opacity:.75}.portal-option span:first-child{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.portal-option span:last-child{margin-left:12px;color:#356ae6;font-size:24px;font-weight:400;line-height:1}.portal-option:disabled span:last-child{color:#98a2b3}.error{margin:16px 0 0;color:#ba1a1a;font-size:14px;line-height:1.6}@media (max-width:380px){body{padding:12px}.card{padding:20px;border-radius:16px}.card h1{font-size:23px}.portal-option{min-height:60px;padding:14px;font-size:16px}}`

func renderEntryError(c *gin.Context, status int, title, message string, retry bool) {
	c.Header("Cache-Control", "no-store")
	c.Data(status, "text/html; charset=utf-8", []byte(`<!doctype html><meta charset="utf-8"><title>`+html.EscapeString(title)+`</title><main><h1>`+html.EscapeString(title)+`</h1><p>`+html.EscapeString(message)+`</p></main>`))
}
