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
	GetByCode(code string) (*models.SitePortal, error)
}
type sitePortalProviderLister interface {
	ListEnabledProviders(portalCode string) ([]*models.SitePortalProvider, int64, error)
}
type portalLoginCompleter interface {
	CompleteLogin(input database.CompletePortalLoginInput) (*models.PortalLoginCompletion, error)
}
type portalEntryStore interface {
	ConsumeT3(t3Hash, portalCode string, now time.Time) (*models.EntryPortalAttempt, *models.EntrySession, error)
	ValidateClaim(claimHash, nodeID, terminalSessionID string, generation int64, now time.Time) error
}
type portalReadyDispatcher interface {
	DispatchPortalSessionReady(nodeID string, payload websocket.PortalSessionReadyPayload) error
}
type portalReadyOutbox interface{ MarkDelivered(eventID string) error }

type SitePortalHandler struct {
	portals     sitePortalAuthenticator
	entries     portalEntryStore
	identities  portalLoginCompleter
	dispatcher  portalReadyDispatcher
	readyOutbox portalReadyOutbox
	now         func() time.Time
}

func NewSitePortalHandler(portals sitePortalAuthenticator, entries portalEntryStore, identities portalLoginCompleter, dispatcher portalReadyDispatcher, readyOutboxes ...portalReadyOutbox) *SitePortalHandler {
	var outbox portalReadyOutbox
	if len(readyOutboxes) > 0 {
		outbox = readyOutboxes[0]
	}
	return &SitePortalHandler{portals: portals, entries: entries, identities: identities, dispatcher: dispatcher, readyOutbox: outbox, now: time.Now}
}

type portalContextRequest struct {
	Handoff string `json:"handoff" binding:"required"`
}
type completePortalLoginRequest struct {
	AttemptID      string    `json:"attempt_id" binding:"required"`
	ExternalUserID string    `json:"external_user_id" binding:"required"`
	DisplayName    string    `json:"display_name" binding:"required"`
	ClaimCode      string    `json:"claim_code" binding:"required"`
	ClaimExpiresAt time.Time `json:"claim_expires_at" binding:"required"`
}
type validateClaimRequest struct {
	ClaimCode         string `json:"claim_code" binding:"required"`
	NodeID            string `json:"node_id" binding:"required"`
	TerminalSessionID string `json:"terminal_session_id" binding:"required"`
	QRGeneration      int64  `json:"qr_generation" binding:"required"`
}

type portalContextProvider struct {
	ProviderID       string    `json:"provider_id"`
	DisplayName      string    `json:"display_name"`
	SortOrder        int       `json:"sort_order"`
	FileBaseURL      string    `json:"file_base_url"`
	SignSecretRef    string    `json:"sign_secret_ref"`
	PortalAPIBaseURL string    `json:"portal_api_base_url,omitempty"`
	UploadEnabled    bool      `json:"upload_enabled,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (h *SitePortalHandler) authenticate(c *gin.Context) (*models.SitePortal, bool) {
	clientType, _ := c.Get("client_type")
	claimed, _ := c.Get("site_portal_code")
	header := strings.TrimSpace(c.GetHeader("X-FlyPrint-Site-Portal"))
	code, ok := claimed.(string)
	if clientType != "site_portal" || !ok || strings.TrimSpace(code) == "" || (header != "" && header != code) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "site_portal_unauthorized"})
		return nil, false
	}
	p, err := h.portals.GetByCode(strings.TrimSpace(code))
	if err != nil || p == nil || !p.Enabled {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "site_portal_unauthorized"})
		return nil, false
	}
	return p, true
}

// Context is the one-time Cloud-side T3 consumer.  The returned attempt ID is
// non-bearer correlation state for the Portal OAuth callback.
func (h *SitePortalHandler) Context(c *gin.Context) {
	p, ok := h.authenticate(c)
	if !ok {
		return
	}
	var in portalContextRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_terminal_context"})
		return
	}
	var providers []*models.SitePortalProvider
	var providerRevision int64
	if lister, ok := h.portals.(sitePortalProviderLister); ok {
		var err error
		providers, providerRevision, err = lister.ListEnabledProviders(p.Code)
		if err != nil {
			if errors.Is(err, database.ErrSitePortalNotFound) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "site_portal_unauthorized"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "site_portal_provider_config_unavailable"})
			return
		}
		if len(providers) == 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "site_portal_provider_config_unavailable"})
			return
		}
	}
	attempt, entry, err := h.entries.ConsumeT3(ticketHash(strings.TrimSpace(in.Handoff)), p.Code, h.now())
	if err != nil {
		c.JSON(http.StatusGone, gin.H{"error": "entry_handoff_invalid"})
		return
	}
	contextProviders := make([]portalContextProvider, 0, len(providers))
	for _, provider := range providers {
		contextProviders = append(contextProviders, portalContextProvider{
			ProviderID: provider.ProviderID, DisplayName: provider.DisplayName, SortOrder: provider.SortOrder,
			FileBaseURL: provider.FileBaseURL, SignSecretRef: provider.SignSecretRef,
			PortalAPIBaseURL: provider.PortalAPIBaseURL, UploadEnabled: provider.UploadEnabled, UpdatedAt: provider.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"portal_attempt_id": attempt.ID, "site_portal_code": p.Code, "node_id": entry.NodeID, "printer_id": entry.PrinterID, "terminal_session_id": entry.TerminalSessionID, "qr_generation": entry.QRGeneration, "expires_at": entry.ExpiresAt, "provider_config_revision": providerRevision, "providers": contextProviders})
}

func (h *SitePortalHandler) CompleteLogin(c *gin.Context) {
	p, ok := h.authenticate(c)
	if !ok {
		return
	}
	var in completePortalLoginRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_login_completion"})
		return
	}
	in.AttemptID = strings.TrimSpace(in.AttemptID)
	in.ExternalUserID = strings.TrimSpace(in.ExternalUserID)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.ClaimCode = strings.TrimSpace(in.ClaimCode)
	now := h.now()
	if in.AttemptID == "" || len(in.ExternalUserID) > 255 || in.DisplayName == "" || len([]rune(in.DisplayName)) > 120 || len(in.ClaimCode) < 12 || len(in.ClaimCode) > 256 || !in.ClaimExpiresAt.After(now) || in.ClaimExpiresAt.After(now.Add(maxPortalClaimTTL)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_login_completion"})
		return
	}
	completion, err := h.identities.CompleteLogin(database.CompletePortalLoginInput{SitePortalCode: p.Code, PortalAttemptID: in.AttemptID, ExternalUserID: in.ExternalUserID, DisplayName: in.DisplayName, ClaimCode: in.ClaimCode, ClaimExpiresAt: in.ClaimExpiresAt, Now: now})
	if err != nil {
		if errors.Is(err, database.ErrExternalIdentityDisabled) {
			c.JSON(http.StatusForbidden, gin.H{"error": "user_disabled"})
		} else if errors.Is(err, database.ErrPortalLoginTicketInvalid) || errors.Is(err, database.ErrPortalLoginPortalMismatch) {
			c.JSON(http.StatusConflict, gin.H{"error": "entry_attempt_invalid"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "login_completion_failed"})
		}
		return
	}
	payload := websocket.PortalSessionReadyPayload{SitePortalCode: completion.SitePortalCode, SitePortalDisplayName: completion.SitePortalDisplayName, ClaimBaseURL: completion.ClaimBaseURL, ClaimCode: in.ClaimCode, TerminalSessionID: completion.TerminalSessionID, CloudUserID: completion.CloudUserID, ExpiresAt: in.ClaimExpiresAt}
	if err := h.dispatcher.DispatchPortalSessionReady(completion.NodeID, payload); err != nil {
		if completion.ReadyEventID != "" && h.readyOutbox != nil {
			c.JSON(http.StatusAccepted, gin.H{"cloud_user_id": completion.CloudUserID, "notification_pending": true})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "edge_notification_failed"})
		return
	}
	if completion.ReadyEventID != "" && h.readyOutbox != nil {
		if err := h.readyOutbox.MarkDelivered(completion.ReadyEventID); err != nil {
			c.JSON(http.StatusAccepted, gin.H{"cloud_user_id": completion.CloudUserID, "notification_pending": true})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"cloud_user_id": completion.CloudUserID})
}

// ValidateClaim lets a Portal prove its locally held Claim still belongs to a
// live root session before returning identity material to Edge.
func (h *SitePortalHandler) ValidateClaim(c *gin.Context) {
	if _, ok := h.authenticate(c); !ok {
		return
	}
	var in validateClaimRequest
	if err := c.ShouldBindJSON(&in); err != nil || in.QRGeneration <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_claim_validation"})
		return
	}
	if err := h.entries.ValidateClaim(ticketHash(strings.TrimSpace(in.ClaimCode)), strings.TrimSpace(in.NodeID), strings.TrimSpace(in.TerminalSessionID), in.QRGeneration, h.now()); err != nil {
		c.JSON(http.StatusGone, gin.H{"error": "claim_invalid"})
		return
	}
	c.Status(http.StatusNoContent)
}
