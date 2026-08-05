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
		h.directEntryPage(c)
		return
	}
	if c.Query("token") != "" {
		h.redirectUploadTokenToEntry(c)
		return
	}
	renderEntryError(c, http.StatusBadRequest, "二维码无效", "请返回打印终端刷新二维码后重新扫码。", false)
}

// directEntryPage routes a ticket using the Site Portal configured for the Edge node.
func (h *TerminalTicketHandler) directEntryPage(c *gin.Context) {
	raw := c.Query("terminal_ticket")
	ticket, err := h.tickets.GetValidByHash(ticketHash(raw), time.Now())
	if err != nil {
		renderEntryError(c, http.StatusGone, "terminal ticket expired", "Please return to the terminal and scan a new QR code.", false)
		return
	}
	if h.sessions != nil {
		matched, matchErr := h.sessions.Matches(ticket.NodeID, ticket.TerminalSessionID, ticket.TicketHash)
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
	if err != nil || portal == nil {
		renderEntryError(c, http.StatusServiceUnavailable, "terminal entry unavailable", "The terminal login source is not configured correctly.", true)
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

// SelectEntry keeps the public entry selector limited to official and the
// configured Site Portal boundary.
func (h *TerminalTicketHandler) SelectEntry(c *gin.Context) {
	var req selectTerminalEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil || (req.Entry != "official" && !sitePortalCodePattern.MatchString(req.Entry)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_terminal_entry"})
		return
	}
	var portal *models.SitePortal
	var err error
	if req.Entry != "official" {
		portal, err = h.sitePortals.GetByCode(req.Entry)
		if err != nil || portal == nil || !portal.Enabled {
			c.JSON(http.StatusBadRequest, gin.H{"error": "terminal_entry_unavailable"})
			return
		}
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
	ticket, err = h.tickets.Select(ticketHash(req.TerminalTicket), req.Entry, time.Now())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "terminal_ticket_locked_or_expired"})
		return
	}
	if h.uploadSessions != nil {
		_ = h.uploadSessions.DeleteOpenForTicket(ticket.TicketHash)
	}
	if req.Entry == "official" {
		token, expiresAt, err := h.tokens.GenerateUploadToken(ticket.NodeID, ticket.PrinterID)
		if err != nil || h.uploadSessions == nil || h.uploadSessions.Create(token, ticket.TicketHash, ticket.NodeID, ticket.PrinterID, ticket.TerminalSessionID, expiresAt) != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "official_upload_unavailable"})
			return
		}
		query := url.Values{"token": {token}, "node_id": {ticket.NodeID}, "printer_id": {ticket.PrinterID}}
		c.JSON(http.StatusOK, gin.H{"redirect_url": "/upload?" + query.Encode()})
		return
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
