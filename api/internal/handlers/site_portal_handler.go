package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/websocket"

	"github.com/gin-gonic/gin"
)

const maxPortalClaimTTL = 5 * time.Minute

type sitePortalAuthenticator interface {
	Authenticate(code, rawToken string) (*models.SitePortal, error)
}

type portalTicketReader interface {
	GetValidByHash(hash string, now time.Time) (*models.TerminalTicket, error)
}

type portalSessionMatcher interface {
	Matches(nodeID, sessionID, ticketHash, integrationRequestID string) (bool, error)
}

type portalLoginCompleter interface {
	CompleteLogin(input database.CompletePortalLoginInput) (*models.PortalLoginCompletion, error)
}

type portalReadyDispatcher interface {
	IsNodeConnected(nodeID string) bool
	DispatchPortalSessionReady(nodeID string, payload websocket.PortalSessionReadyPayload) error
}

type SitePortalHandler struct {
	portals    sitePortalAuthenticator
	tickets    portalTicketReader
	sessions   portalSessionMatcher
	identities portalLoginCompleter
	dispatcher portalReadyDispatcher
	now        func() time.Time
}

func NewSitePortalHandler(
	portals sitePortalAuthenticator,
	tickets portalTicketReader,
	sessions portalSessionMatcher,
	identities portalLoginCompleter,
	dispatcher portalReadyDispatcher,
) *SitePortalHandler {
	return &SitePortalHandler{
		portals: portals, tickets: tickets, sessions: sessions,
		identities: identities, dispatcher: dispatcher, now: time.Now,
	}
}

type portalContextRequest struct {
	TerminalTicket string `json:"terminal_ticket" binding:"required"`
}

type completePortalLoginRequest struct {
	TerminalTicket string    `json:"terminal_ticket" binding:"required"`
	ExternalUserID string    `json:"external_user_id" binding:"required"`
	DisplayName    string    `json:"display_name" binding:"required"`
	ClaimCode      string    `json:"claim_code" binding:"required"`
	ClaimExpiresAt time.Time `json:"claim_expires_at" binding:"required"`
}

func (h *SitePortalHandler) authenticate(c *gin.Context) (*models.SitePortal, bool) {
	code := strings.TrimSpace(c.GetHeader("X-FlyPrint-Site-Portal"))
	authorization := strings.TrimSpace(c.GetHeader("Authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "site_portal_unauthorized"})
		return nil, false
	}
	portal, err := h.portals.Authenticate(code, strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "site_portal_unauthorized"})
		return nil, false
	}
	return portal, true
}

func (h *SitePortalHandler) Context(c *gin.Context) {
	portal, ok := h.authenticate(c)
	if !ok {
		return
	}
	var input portalContextRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_terminal_context"})
		return
	}
	rawTicket := strings.TrimSpace(input.TerminalTicket)
	ticket, err := h.tickets.GetValidByHash(ticketHash(rawTicket), h.now())
	if err != nil || ticket.SelectedEntry == nil || *ticket.SelectedEntry != portal.Code {
		c.JSON(http.StatusGone, gin.H{"error": "terminal_ticket_invalid"})
		return
	}
	matched, err := h.sessions.Matches(ticket.NodeID, ticket.TerminalSessionID, ticket.TicketHash, "")
	if err != nil || !matched {
		c.JSON(http.StatusConflict, gin.H{"error": "terminal_session_invalid"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"site_portal_code":    portal.Code,
		"node_id":             ticket.NodeID,
		"printer_id":          ticket.PrinterID,
		"terminal_session_id": ticket.TerminalSessionID,
		"expires_at":          ticket.ExpiresAt,
	})
}

func (h *SitePortalHandler) CompleteLogin(c *gin.Context) {
	portal, ok := h.authenticate(c)
	if !ok {
		return
	}
	var input completePortalLoginRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_login_completion"})
		return
	}
	input.TerminalTicket = strings.TrimSpace(input.TerminalTicket)
	input.ExternalUserID = strings.TrimSpace(input.ExternalUserID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.ClaimCode = strings.TrimSpace(input.ClaimCode)
	now := h.now()
	if input.TerminalTicket == "" || len(input.ExternalUserID) > 255 ||
		input.DisplayName == "" || len([]rune(input.DisplayName)) > 120 ||
		len(input.ClaimCode) < 12 || len(input.ClaimCode) > 256 ||
		!input.ClaimExpiresAt.After(now) || input.ClaimExpiresAt.After(now.Add(maxPortalClaimTTL)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_login_completion"})
		return
	}

	ticket, err := h.tickets.GetValidByHash(ticketHash(input.TerminalTicket), now)
	if err != nil || ticket.SelectedEntry == nil || *ticket.SelectedEntry != portal.Code {
		c.JSON(http.StatusGone, gin.H{"error": "terminal_ticket_invalid"})
		return
	}
	matched, err := h.sessions.Matches(ticket.NodeID, ticket.TerminalSessionID, ticket.TicketHash, "")
	if err != nil || !matched {
		c.JSON(http.StatusConflict, gin.H{"error": "terminal_session_invalid"})
		return
	}
	if !h.dispatcher.IsNodeConnected(ticket.NodeID) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "edge_not_connected"})
		return
	}

	completion, err := h.identities.CompleteLogin(database.CompletePortalLoginInput{
		SitePortalCode: portal.Code,
		TicketHash:     ticket.TicketHash,
		ExternalUserID: input.ExternalUserID,
		DisplayName:    input.DisplayName,
		Now:            now,
	})
	if err != nil {
		switch {
		case errors.Is(err, database.ErrExternalIdentityDisabled):
			c.JSON(http.StatusForbidden, gin.H{"error": "user_disabled"})
		case errors.Is(err, database.ErrPortalLoginTicketInvalid),
			errors.Is(err, database.ErrPortalLoginPortalMismatch):
			c.JSON(http.StatusConflict, gin.H{"error": "terminal_ticket_invalid"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "login_completion_failed"})
		}
		return
	}
	payload := websocket.PortalSessionReadyPayload{
		SitePortalCode:    completion.SitePortalCode,
		ClaimBaseURL:      completion.ClaimBaseURL,
		ClaimCode:         input.ClaimCode,
		TerminalSessionID: completion.TerminalSessionID,
		CloudUserID:       completion.CloudUserID,
		ExpiresAt:         input.ClaimExpiresAt,
	}
	if err := h.dispatcher.DispatchPortalSessionReady(completion.NodeID, payload); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "edge_notification_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"cloud_user_id": completion.CloudUserID})
}
