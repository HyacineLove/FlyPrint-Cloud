package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/security"
	"fly-print-cloud/api/internal/websocket"

	"github.com/gin-gonic/gin"
)

const terminalTicketTTL = 5 * time.Minute

type TerminalTicketHandler struct {
	tickets        *database.TerminalTicketRepository
	printers       *database.PrinterRepository
	edgeNodes      *database.EdgeNodeRepository
	providers      *database.IntegrationProviderRepository
	uploadSessions *database.TerminalUploadSessionRepository
	sessions       *database.TerminalSessionRepository
	sitePortals    *database.SitePortalRepository
	tokens         *security.TokenManager
	wsManager      *websocket.ConnectionManager
}

func NewTerminalTicketHandler(tickets *database.TerminalTicketRepository, printers *database.PrinterRepository, edgeNodes *database.EdgeNodeRepository, providers *database.IntegrationProviderRepository, uploadSessions *database.TerminalUploadSessionRepository, tokens *security.TokenManager, wsManager *websocket.ConnectionManager, sessions *database.TerminalSessionRepository, sitePortals *database.SitePortalRepository) *TerminalTicketHandler {
	return &TerminalTicketHandler{tickets: tickets, printers: printers, edgeNodes: edgeNodes, providers: providers, uploadSessions: uploadSessions, sessions: sessions, sitePortals: sitePortals, tokens: tokens, wsManager: wsManager}
}

func (h *TerminalTicketHandler) EntryPage(c *gin.Context) {
	if c.Query("terminal_ticket") != "" {
		h.directEntryPage(c)
		return
	}
	raw := c.Query("terminal_ticket")
	if raw == "" {
		if c.Query("token") != "" {
			h.redirectUploadTokenToEntry(c)
			return
		}
		renderEntryError(c, http.StatusBadRequest, "二维码无效", "请返回打印终端刷新二维码后重新扫描。", false)
		return
	}
	if _, err := h.tickets.GetValidByHash(ticketHash(raw), time.Now()); err != nil {
		renderEntryError(c, http.StatusGone, "二维码已失效", "请返回打印终端刷新二维码后重新扫描。", false)
		return
	}
	providers, err := h.providers.List()
	if err != nil {
		renderEntryError(c, http.StatusServiceUnavailable, "打印入口暂时不可用", "服务暂时无法加载，请稍后重新尝试。", true)
		return
	}
	ticket := html.EscapeString(raw)
	buttons := "<button data-entry=official><strong>飞印官方打印</strong><span>上传文件并在当前终端打印</span></button>"
	for _, provider := range providers {
		if provider.Enabled && provider.EntryVisible {
			buttons += "<button data-entry='" + html.EscapeString(provider.Code) + "'><strong>" + html.EscapeString(provider.DisplayName) + "</strong><span>进入第三方打印入口</span></button>"
		}
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte("<!doctype html><html lang=zh-CN><head><meta charset=utf-8><meta name=viewport content='width=device-width,initial-scale=1'><title>飞印打印入口</title><style>*{box-sizing:border-box}body{margin:0;background:#f4f7fb;color:#172033;font-family:system-ui,-apple-system,'Microsoft YaHei',sans-serif}main{max-width:560px;margin:0 auto;padding:48px 20px}h1{font-size:28px;margin:0 0 8px}p{color:#697386;margin:0 0 28px}.entries{display:grid;gap:14px}button{width:100%;padding:20px;text-align:left;border:1px solid #dce3ee;border-radius:14px;background:#fff;box-shadow:0 6px 20px rgba(37,61,95,.07);cursor:pointer}button:active{transform:translateY(1px)}button:disabled{cursor:wait;opacity:.65}strong,span{display:block}strong{font-size:18px;color:#1268e8;margin-bottom:5px}span{font-size:14px;color:#778195}.notice{display:none;margin-top:18px;padding:14px 16px;border-radius:12px;background:#fff2f0;color:#b42318;border:1px solid #ffccc7;line-height:1.55}.notice.visible{display:block}</style></head><body><main><h1>选择打印入口</h1><p>未上传完成前可重新选择入口；上传或提交后不可更换。</p><div class=entries>"+buttons+"</div><div id=notice class=notice role=alert></div><script>const notice=document.getElementById('notice');window.addEventListener('pageshow',()=>document.querySelectorAll('button').forEach(x=>x.disabled=false));const errors={terminal_ticket_locked_or_expired:'本次二维码已失效或已完成上传，请返回终端重新扫码。',terminal_session_invalid:'终端会话已更新，请返回终端重新扫码。',terminal_entry_unavailable:'所选打印入口当前不可用，请联系管理员。',node_disabled:'打印终端已停用，请联系工作人员。',printer_unavailable:'打印机不可用或已删除，请联系工作人员。',official_upload_unavailable:'官方打印暂时不可用，请稍后重新扫码。'};document.querySelectorAll('[data-entry]').forEach(b=>b.onclick=async()=>{document.querySelectorAll('button').forEach(x=>x.disabled=true);notice.classList.remove('visible');try{const r=await fetch('/api/v1/public/terminal-entry/select',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({terminal_ticket:'"+ticket+"',entry:b.dataset.entry})});const d=await r.json();if(r.ok&&d.redirect_url){document.querySelectorAll('button').forEach(x=>x.disabled=false);location.href=d.redirect_url;return}notice.textContent=errors[d.error]||'打印入口暂时不可用，请返回终端重新扫码。'}catch(e){notice.textContent='网络连接异常，请检查网络后重新尝试。'}notice.classList.add('visible');document.querySelectorAll('button').forEach(x=>x.disabled=false)});</script></main></body></html>"))
}

// directEntryPage routes a QR ticket using the login source configured on its
// Edge node. The public page intentionally has no provider selection step.
func (h *TerminalTicketHandler) directEntryPage(c *gin.Context) {
	raw := c.Query("terminal_ticket")
	ticket, err := h.tickets.GetValidByHash(ticketHash(raw), time.Now())
	if err != nil {
		renderEntryError(c, http.StatusGone, "terminal ticket expired", "Please return to the terminal and scan a new QR code.", false)
		return
	}

	if h.sessions != nil {
		matched, matchErr := h.sessions.Matches(ticket.NodeID, ticket.TerminalSessionID, ticket.TicketHash, "")
		if matchErr != nil || !matched {
			renderEntryError(c, http.StatusConflict, "terminal session expired", "The terminal session has changed. Please scan a new QR code.", false)
			return
		}
	}

	node, err := h.edgeNodes.GetEdgeNodeByID(ticket.NodeID)
	if err != nil || node == nil || !node.Enabled {
		renderEntryError(c, http.StatusForbidden, "terminal unavailable", "This terminal is disabled or offline.", false)
		return
	}
	portal, err := h.sitePortals.GetDefaultForNode(ticket.NodeID)
	if err != nil {
		renderEntryError(c, http.StatusServiceUnavailable, "terminal entry unavailable", "The terminal login source is not configured correctly.", false)
		return
	}

	printer, err := h.printers.GetPrinterByID(ticket.PrinterID)
	if err != nil || printer == nil || printer.EdgeNodeID != ticket.NodeID || !printer.Enabled {
		renderEntryError(c, http.StatusForbidden, "printer unavailable", "The configured printer is unavailable.", false)
		return
	}

	selected, err := h.tickets.Select(ticket.TicketHash, portal.Code, time.Now())
	if err != nil {
		renderEntryError(c, http.StatusConflict, "terminal ticket expired", "The ticket is expired or has already been used.", false)
		return
	}
	_ = h.uploadSessions.DeleteOpenForTicket(selected.TicketHash)

	redirect, err := buildSitePortalEntryURL(portal.EntryURL, raw)
	if err != nil {
		renderEntryError(c, http.StatusServiceUnavailable, "terminal entry unavailable", "The configured provider entry is invalid.", false)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, redirect)
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

// redirectUploadTokenToEntry bridges the original Edge QR path into the entry
// selector. The upload token proves node/printer ownership; a separate opaque
// terminal ticket is issued so providers never receive an upload credential.
func (h *TerminalTicketHandler) redirectUploadTokenToEntry(c *gin.Context) {
	rawUploadToken := c.Query("token")
	nodeID := c.Query("node_id")
	printerID := c.Query("printer_id")
	payload, err := h.tokens.VerifyUploadTokenAvailable(rawUploadToken, nodeID, printerID)
	if err != nil {
		renderEntryError(c, http.StatusGone, "二维码已失效", "该二维码已被使用或已过期，请返回打印终端查看是否已换新码后重新扫描。", false)
		return
	}
	node, err := h.edgeNodes.GetEdgeNodeByID(payload.NodeID)
	if err != nil || node == nil || !node.Enabled {
		renderEntryError(c, http.StatusForbidden, "打印终端不可用", "当前终端已停用或离线，请联系工作人员处理。", false)
		return
	}
	printer, err := h.printers.GetPrinterByID(payload.PrinterID)
	if err != nil || printer == nil || printer.EdgeNodeID != payload.NodeID || !printer.Enabled {
		renderEntryError(c, http.StatusForbidden, "打印机不可用", "当前打印机已停用或已删除，请联系工作人员处理。", false)
		return
	}
	sessionNotBefore := time.Unix(payload.IssuedAt, 0).Add(-5 * time.Second)
	hasSession, err := h.tickets.HasCurrentSession(payload.NodeID, sessionNotBefore)
	if err != nil || !hasSession {
		renderEntryError(c, http.StatusConflict, "终端正在准备", "请稍后重新尝试；若仍无法进入，请返回终端刷新二维码。", true)
		return
	}
	// Consume the bridge credential before issuing the separate terminal
	// ticket. Concurrent scans can therefore create at most one entry ticket.
	if _, err := h.tokens.ValidateUploadTokenForContext(rawUploadToken, nodeID, printerID); err != nil {
		renderEntryError(c, http.StatusGone, "二维码已失效", "该二维码已被使用或已过期，请返回打印终端查看是否已换新码后重新扫描。", false)
		return
	}

	rawTicket, err := newTerminalTicket()
	if err != nil {
		renderEntryError(c, http.StatusServiceUnavailable, "打印入口暂时不可用", "服务暂时无法处理请求，请稍后重新尝试。", true)
		return
	}
	expiresAt := time.Now().Add(terminalTicketTTL)
	if uploadExpiry := time.Unix(payload.ExpiresAt, 0); uploadExpiry.Before(expiresAt) {
		expiresAt = uploadExpiry
	}
	ticket := &models.TerminalTicket{
		TicketHash: ticketHash(rawTicket), NodeID: payload.NodeID,
		PrinterID: payload.PrinterID, ExpiresAt: expiresAt,
	}
	if err := h.tickets.CreateForCurrentSession(ticket, sessionNotBefore); err != nil {
		renderEntryError(c, http.StatusConflict, "二维码已失效", "终端会话已经变化，请返回打印终端刷新二维码后重新扫描。", false)
		return
	}
	if h.wsManager != nil {
		h.wsManager.MarkTerminalOccupied(ticket.NodeID, websocket.TerminalOccupiedPayload{
			TerminalSessionID:  ticket.TerminalSessionID,
			TerminalTicketHash: ticket.TicketHash,
			ExpiresAt:          ticket.ExpiresAt,
		})
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, "/entry?"+url.Values{"terminal_ticket": {rawTicket}}.Encode())
}

type selectTerminalEntryRequest struct {
	TerminalTicket string `json:"terminal_ticket" binding:"required"`
	Entry          string `json:"entry" binding:"required"`
}

func (h *TerminalTicketHandler) SelectEntry(c *gin.Context) {
	var req selectTerminalEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil || !providerCodePattern.MatchString(req.Entry) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_terminal_entry"})
		return
	}
	var provider *models.IntegrationProvider
	if req.Entry != "official" {
		var err error
		provider, err = h.providers.Get(req.Entry, false)
		if err != nil || provider == nil || !provider.Enabled || !provider.EntryVisible {
			c.JSON(http.StatusBadRequest, gin.H{"error": "terminal_entry_unavailable"})
			return
		}
	}
	ticket, err := h.tickets.GetValidByHash(ticketHash(req.TerminalTicket), time.Now())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "terminal_ticket_locked_or_expired", "message": "会话已失效，请返回终端重新扫码。"})
		return
	}
	if h.sessions != nil {
		ok, matchErr := h.sessions.Matches(ticket.NodeID, ticket.TerminalSessionID, ticket.TicketHash, "")
		if matchErr != nil || !ok {
			c.JSON(http.StatusConflict, gin.H{"error": "terminal_session_invalid", "message": "终端会话已更新，请返回终端重新扫码。"})
			return
		}
	}
	node, err := h.edgeNodes.GetEdgeNodeByID(ticket.NodeID)
	if err != nil || node == nil || !node.Enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "node_disabled", "message": "打印终端已停用，请联系工作人员。"})
		return
	}
	printer, err := h.printers.GetPrinterByID(ticket.PrinterID)
	if err != nil || printer == nil || printer.EdgeNodeID != ticket.NodeID || !printer.Enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "printer_unavailable", "message": "打印机不可用或已删除，请联系工作人员。"})
		return
	}
	ticket, err = h.tickets.Select(ticketHash(req.TerminalTicket), req.Entry, time.Now())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "terminal_ticket_locked_or_expired", "message": "会话已失效或已完成上传，请返回终端重新扫码。"})
		return
	}
	// Drop stale official upload bindings when re-selecting after backing out.
	_ = h.uploadSessions.DeleteOpenForTicket(ticket.TicketHash)
	if req.Entry == "official" {
		token, expiresAt, err := h.tokens.GenerateUploadToken(ticket.NodeID, ticket.PrinterID)
		if err != nil || h.uploadSessions.Create(token, ticket.TicketHash, ticket.NodeID, ticket.PrinterID, ticket.TerminalSessionID, expiresAt) != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "official_upload_unavailable"})
			return
		}
		query := url.Values{"token": {token}, "node_id": {ticket.NodeID}, "printer_id": {ticket.PrinterID}}
		c.JSON(http.StatusOK, gin.H{"redirect_url": "/upload?" + query.Encode()})
		return
	}
	redirect, err := url.Parse(provider.EntryURL)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "terminal_entry_unavailable"})
		return
	}
	query := redirect.Query()
	query.Set("terminal_ticket", req.TerminalTicket)
	redirect.RawQuery = query.Encode()
	c.JSON(http.StatusOK, gin.H{"redirect_url": redirect.String()})
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
		action = "<button type=button onclick='location.reload()'>重新尝试</button>"
	}
	c.Header("Cache-Control", "no-store")
	c.Data(status, "text/html; charset=utf-8", []byte("<!doctype html><html lang=zh-CN><head><meta charset=utf-8><meta name=viewport content='width=device-width,initial-scale=1'><title>"+html.EscapeString(title)+"</title><style>*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:#f4f7fb;color:#172033;font-family:system-ui,-apple-system,'Microsoft YaHei',sans-serif}.card{width:min(100%,480px);padding:34px 28px;border:1px solid #e4e9f1;border-radius:18px;background:#fff;box-shadow:0 16px 48px rgba(37,61,95,.1);text-align:center}.icon{width:58px;height:58px;margin:0 auto 20px;border-radius:50%;display:grid;place-items:center;background:#fff2f0;color:#d92d20;font-size:30px;font-weight:700}h1{margin:0 0 12px;font-size:24px}p{margin:0;color:#697386;line-height:1.7}button{margin-top:24px;padding:11px 24px;border:0;border-radius:10px;background:#1268e8;color:#fff;font-size:16px;cursor:pointer}</style></head><body><main class=card><div class=icon>!</div><h1>"+html.EscapeString(title)+"</h1><p>"+html.EscapeString(message)+"</p>"+action+"</main></body></html>"))
}
