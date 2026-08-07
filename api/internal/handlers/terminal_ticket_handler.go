package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/security"
	"fly-print-cloud/api/internal/websocket"

	"github.com/gin-gonic/gin"
)

const terminalTicketTTL = 5 * time.Minute

var sitePortalCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

type TerminalTicketHandler struct {
	tickets        *database.TerminalTicketRepository
	printers       *database.PrinterRepository
	edgeNodes      *database.EdgeNodeRepository
	uploadSessions *database.TerminalUploadSessionRepository
	sessions       *database.TerminalSessionRepository
	sitePortals    *database.SitePortalRepository
	tokens         *security.TokenManager
	wsManager      *websocket.ConnectionManager
}

func NewTerminalTicketHandler(tickets *database.TerminalTicketRepository, printers *database.PrinterRepository, edgeNodes *database.EdgeNodeRepository, uploadSessions *database.TerminalUploadSessionRepository, tokens *security.TokenManager, wsManager *websocket.ConnectionManager, sessions *database.TerminalSessionRepository, sitePortals *database.SitePortalRepository) *TerminalTicketHandler {
	return &TerminalTicketHandler{tickets: tickets, printers: printers, edgeNodes: edgeNodes, uploadSessions: uploadSessions, sessions: sessions, sitePortals: sitePortals, tokens: tokens, wsManager: wsManager}
}

func (h *TerminalTicketHandler) EntryPage(c *gin.Context) {
	if c.Query("terminal_ticket") != "" {
		entry, validationErr := h.loadTerminalEntryContext(c.Query("terminal_ticket"))
		if validationErr != nil {
			renderEntryError(c, validationErr.status, validationErr.title, validationErr.message, validationErr.retry)
			return
		}
		if len(entry.portals.Portals) > 1 {
			renderSitePortalSelectionPage(c.Writer, c.Query("terminal_ticket"), entry.portals.Portals, entry.portals.DefaultPortalCode)
			return
		}
		h.directEntryPage(c)
		return
	}
	if c.Query("token") != "" {
		h.redirectUploadTokenToEntry(c)
		return
	}
	renderEntryError(c, http.StatusBadRequest, "二维码无效", "请返回打印终端刷新二维码后重新扫码。", false)
}

type terminalEntryContext struct {
	ticket  *models.TerminalTicket
	portals *models.EdgeSitePortalConfig
}

type terminalEntryValidationError struct {
	status  int
	title   string
	message string
	retry   bool
}

func (e *terminalEntryValidationError) Error() string {
	return e.title
}

func (h *TerminalTicketHandler) loadTerminalEntryContext(raw string) (*terminalEntryContext, *terminalEntryValidationError) {
	ticket, err := h.tickets.GetValidByHash(ticketHash(raw), time.Now())
	if err != nil {
		return nil, &terminalEntryValidationError{http.StatusGone, "terminal ticket expired", "Please return to the terminal and scan a new QR code.", false}
	}
	if h.sessions != nil {
		matched, matchErr := h.sessions.Matches(ticket.NodeID, ticket.TerminalSessionID, ticket.TicketHash)
		if matchErr != nil || !matched {
			return nil, &terminalEntryValidationError{http.StatusConflict, "terminal session expired", "The terminal session has changed. Please scan a new QR code.", false}
		}
	}
	node, err := h.edgeNodes.GetEdgeNodeByID(ticket.NodeID)
	if err != nil || node == nil || !node.Enabled {
		return nil, &terminalEntryValidationError{http.StatusForbidden, "terminal unavailable", "This terminal is disabled or offline.", false}
	}
	portals, err := h.sitePortals.GetEdgeSitePortalConfig(ticket.NodeID)
	if err != nil || portals == nil {
		return nil, &terminalEntryValidationError{http.StatusServiceUnavailable, "terminal entry unavailable", "The terminal Site Portal configuration is not available.", true}
	}
	activePortals := make([]*models.SitePortal, 0, len(portals.Portals))
	for _, portal := range portals.Portals {
		if portal != nil && portal.Enabled {
			activePortals = append(activePortals, portal)
		}
	}
	portals.Portals = activePortals
	if len(portals.Portals) == 0 || strings.TrimSpace(portals.DefaultPortalCode) == "" {
		return nil, &terminalEntryValidationError{http.StatusServiceUnavailable, "terminal entry unavailable", "The terminal Site Portal configuration is not available.", true}
	}
	printer, err := h.printers.GetPrinterByID(ticket.PrinterID)
	if err != nil || printer == nil || printer.EdgeNodeID != ticket.NodeID || !printer.Enabled {
		return nil, &terminalEntryValidationError{http.StatusForbidden, "printer unavailable", "The configured printer is unavailable.", false}
	}
	return &terminalEntryContext{ticket: ticket, portals: portals}, nil
}

func findSitePortal(config *models.EdgeSitePortalConfig, code string) *models.SitePortal {
	for _, portal := range config.Portals {
		if portal != nil && portal.Code == code {
			return portal
		}
	}
	return nil
}

// directEntryPage routes a ticket using the Site Portal configured for the Edge node.
func (h *TerminalTicketHandler) directEntryPage(c *gin.Context) {
	raw := c.Query("terminal_ticket")
	entry, validationErr := h.loadTerminalEntryContext(raw)
	if validationErr != nil {
		renderEntryError(c, validationErr.status, validationErr.title, validationErr.message, validationErr.retry)
		return
	}
	portal := findSitePortal(entry.portals, entry.portals.DefaultPortalCode)
	if portal == nil {
		renderEntryError(c, http.StatusServiceUnavailable, "terminal entry unavailable", "The terminal Site Portal configuration is not available.", true)
		return
	}
	selected, err := h.tickets.Select(entry.ticket.TicketHash, portal.Code, time.Now())
	if err != nil {
		renderEntryError(c, http.StatusConflict, "terminal ticket expired", "The ticket is expired or has already been used.", false)
		return
	}
	if h.uploadSessions != nil {
		_ = h.uploadSessions.DeleteOpenForTicket(selected.TicketHash)
	}
	redirect, err := buildSitePortalEntryURL(portal.EntryURL, raw)
	if err != nil {
		renderEntryError(c, http.StatusServiceUnavailable, "terminal entry unavailable", "The configured Site Portal entry is invalid.", false)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, redirect)
}

func renderSitePortalSelectionPage(w http.ResponseWriter, rawTicket string, portals []*models.SitePortal, defaultCode string) {
	var options strings.Builder
	for _, portal := range portals {
		if portal == nil || !portal.Enabled {
			continue
		}
		defaultAttribute := ""
		if portal.Code == defaultCode {
			defaultAttribute = ` data-default="true"`
		}
		fmt.Fprintf(&options, `<button type="button" class="portal-option" data-entry="%s"%s><span>%s</span>%s</button>`,
			html.EscapeString(portal.Code), defaultAttribute, html.EscapeString(portal.DisplayName),
			func() string {
				if portal.Code == defaultCode {
					return `<small>默认入口</small>`
				}
				return ""
			}())
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>选择登录入口</title><style>
*{box-sizing:border-box}body{margin:0;min-height:100vh;background:#f5f7fb;color:#172033;font-family:system-ui,-apple-system,"Segoe UI",sans-serif;display:flex;align-items:center;justify-content:center;padding:24px}.card{width:min(440px,100%%);background:#fff;border:1px solid #e4e8f0;border-radius:20px;padding:28px;box-shadow:0 12px 36px #1d355712}.card h1{margin:0 0 8px;font-size:24px}.card p{margin:0 0 22px;color:#667085}.portal-option{width:100%%;display:flex;align-items:center;justify-content:space-between;gap:16px;margin:10px 0;padding:16px 18px;border:1px solid #d8deea;border-radius:14px;background:#fff;color:#172033;font-size:16px;text-align:left;cursor:pointer}.portal-option:hover,.portal-option:focus{border-color:#356ae6;outline:3px solid #356ae626}.portal-option[data-default="true"]{border-color:#356ae6;background:#f3f7ff}.portal-option small{color:#356ae6}.error{min-height:1.4em;margin-top:14px;color:#ba1a1a}</style></head><body><main class="card" id="site-portal-selection" data-terminal-ticket="%s"><h1>选择登录入口</h1><p>请选择本次打印使用的 Site Portal。</p><section aria-label="Site Portal 列表">%s</section><div class="error" role="alert" aria-live="polite"></div></main><script>
const root=document.getElementById("site-portal-selection");const error=root.querySelector(".error");root.querySelectorAll(".portal-option").forEach((button)=>button.addEventListener("click",async()=>{root.querySelectorAll("button").forEach((item)=>item.disabled=true);error.textContent="正在打开登录入口…";try{const response=await fetch("/api/v1/public/terminal-entry/select",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({terminal_ticket:root.dataset.terminalTicket,entry:button.dataset.entry})});const payload=await response.json().catch(()=>({}));if(!response.ok||typeof payload.redirect_url!=="string"||!payload.redirect_url){throw new Error("selection failed")}window.location.assign(payload.redirect_url)}catch(_){root.querySelectorAll("button").forEach((item)=>item.disabled=false);error.textContent="登录入口暂时不可用，请重新扫码。"}}));</script></body></html>`, html.EscapeString(rawTicket), options.String())
}

func buildSitePortalEntryURL(entryURL, terminalTicket string) (string, error) {
	redirect, err := url.Parse(entryURL)
	if err != nil || redirect.Scheme == "" || redirect.Host == "" {
		return "", fmt.Errorf("invalid Site Portal entry URL")
	}
	query := redirect.Query()
	query.Set("terminal_ticket", terminalTicket)
	redirect.RawQuery = query.Encode()
	return redirect.String(), nil
}

type selectTerminalEntryRequest struct {
	TerminalTicket string `json:"terminal_ticket" binding:"required"`
	Entry          string `json:"entry" binding:"required"`
}

// SelectEntry validates and locks one assigned Site Portal for the terminal
// ticket before returning its configured entry URL.
func (h *TerminalTicketHandler) SelectEntry(c *gin.Context) {
	var req selectTerminalEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil || !sitePortalCodePattern.MatchString(req.Entry) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_terminal_entry"})
		return
	}
	portal, err := h.sitePortals.GetByCode(req.Entry)
	if err != nil || portal == nil || !portal.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "terminal_entry_unavailable"})
		return
	}
	ticket, err := h.tickets.GetValidByHash(ticketHash(req.TerminalTicket), time.Now())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "terminal_ticket_locked_or_expired"})
		return
	}
	if h.sessions != nil {
		ok, matchErr := h.sessions.Matches(ticket.NodeID, ticket.TerminalSessionID, ticket.TicketHash)
		if matchErr != nil || !ok {
			c.JSON(http.StatusConflict, gin.H{"error": "terminal_session_invalid"})
			return
		}
	}
	node, err := h.edgeNodes.GetEdgeNodeByID(ticket.NodeID)
	if err != nil || node == nil || !node.Enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "node_disabled"})
		return
	}
	printer, err := h.printers.GetPrinterByID(ticket.PrinterID)
	if err != nil || printer == nil || printer.EdgeNodeID != ticket.NodeID || !printer.Enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "printer_unavailable"})
		return
	}
	assigned, assignmentErr := h.sitePortals.IsAssignedToNode(ticket.NodeID, req.Entry)
	if assignmentErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "terminal_entry_unavailable"})
		return
	}
	if !assigned {
		c.JSON(http.StatusBadRequest, gin.H{"error": "terminal_entry_unavailable"})
		return
	}
	ticket, err = h.tickets.Select(ticketHash(req.TerminalTicket), req.Entry, time.Now())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "terminal_ticket_locked_or_expired"})
		return
	}
	if h.uploadSessions != nil {
		_ = h.uploadSessions.DeleteOpenForTicket(ticket.TicketHash)
	}
	redirect, err := buildSitePortalEntryURL(portal.EntryURL, req.TerminalTicket)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "terminal_entry_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"redirect_url": redirect})
}

func (h *TerminalTicketHandler) redirectUploadTokenToEntry(c *gin.Context) {
	rawUploadToken := c.Query("token")
	nodeID := c.Query("node_id")
	printerID := c.Query("printer_id")
	payload, err := h.tokens.VerifyUploadTokenAvailable(rawUploadToken, nodeID, printerID)
	if err != nil {
		renderEntryError(c, http.StatusGone, "二维码已失效", "请返回打印终端重新扫码。", false)
		return
	}
	node, err := h.edgeNodes.GetEdgeNodeByID(payload.NodeID)
	if err != nil || node == nil || !node.Enabled {
		renderEntryError(c, http.StatusForbidden, "打印终端不可用", "当前终端已停用或离线。", false)
		return
	}
	printer, err := h.printers.GetPrinterByID(payload.PrinterID)
	if err != nil || printer == nil || printer.EdgeNodeID != payload.NodeID || !printer.Enabled {
		renderEntryError(c, http.StatusForbidden, "打印机不可用", "当前打印机已停用或已删除。", false)
		return
	}
	sessionNotBefore := time.Unix(payload.IssuedAt, 0).Add(-5 * time.Second)
	hasSession, err := h.tickets.HasCurrentSession(payload.NodeID, sessionNotBefore)
	if err != nil || !hasSession {
		renderEntryError(c, http.StatusConflict, "终端正在准备", "请稍后重试。", true)
		return
	}
	if _, err := h.tokens.ValidateUploadTokenForContext(rawUploadToken, nodeID, printerID); err != nil {
		renderEntryError(c, http.StatusGone, "二维码已失效", "请返回打印终端重新扫码。", false)
		return
	}
	rawTicket, err := newTerminalTicket()
	if err != nil {
		renderEntryError(c, http.StatusServiceUnavailable, "打印入口暂不可用", "请稍后重试。", true)
		return
	}
	expiresAt := time.Now().Add(terminalTicketTTL)
	if uploadExpiry := time.Unix(payload.ExpiresAt, 0); uploadExpiry.Before(expiresAt) {
		expiresAt = uploadExpiry
	}
	ticket := &models.TerminalTicket{TicketHash: ticketHash(rawTicket), NodeID: payload.NodeID, PrinterID: payload.PrinterID, ExpiresAt: expiresAt}
	if err := h.tickets.CreateForCurrentSession(ticket, sessionNotBefore); err != nil {
		renderEntryError(c, http.StatusConflict, "二维码已失效", "终端会话已变化，请返回终端刷新二维码。", false)
		return
	}
	if h.wsManager != nil {
		h.wsManager.MarkTerminalOccupied(ticket.NodeID, websocket.TerminalOccupiedPayload{TerminalSessionID: ticket.TerminalSessionID, TerminalTicketHash: ticket.TicketHash, ExpiresAt: ticket.ExpiresAt})
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, "/entry?terminal_ticket="+url.QueryEscape(rawTicket))
}

func newTerminalTicket() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func ticketHash(raw string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) }

func renderEntryError(c *gin.Context, status int, title, message string, retry bool) {
	action := ""
	if retry {
		action = `<button type="button" onclick="location.reload()">重试</button>`
	}
	c.Header("Cache-Control", "no-store")
	c.Data(status, "text/html; charset=utf-8", []byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>`+html.EscapeString(title)+`</title><style>`+entryErrorStyle+`</style></head><body><main class=card role="alert"><div class="error-icon">!</div><h1>`+html.EscapeString(title)+`</h1><p>`+html.EscapeString(message)+`</p>`+action+`</main></body></html>`))
}
