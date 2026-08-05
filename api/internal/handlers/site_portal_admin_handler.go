package handlers

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"fly-print-cloud/api/internal/auth"
	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/security"

	"github.com/gin-gonic/gin"
)

type SitePortalAdminHandler struct {
	portals *database.SitePortalRepository
	clients *database.OAuth2ClientRepository
	cipher  *security.ClientSecretCipher
}

func NewSitePortalAdminHandler(portals *database.SitePortalRepository, clients *database.OAuth2ClientRepository, cipher *security.ClientSecretCipher) *SitePortalAdminHandler {
	return &SitePortalAdminHandler{portals: portals, clients: clients, cipher: cipher}
}

type sitePortalAdminRequest struct {
	Code         string `json:"code" binding:"required"`
	DisplayName  string `json:"display_name" binding:"required"`
	EntryURL     string `json:"entry_url" binding:"required"`
	ClaimBaseURL string `json:"claim_base_url" binding:"required"`
}

func validateSitePortalAdminRequest(req sitePortalAdminRequest) error {
	if matched, _ := regexp.MatchString(`^[a-z0-9][a-z0-9_-]{1,63}$`, strings.TrimSpace(req.Code)); !matched {
		return fmt.Errorf("invalid Site Portal code")
	}
	for name, raw := range map[string]string{"entry_url": req.EntryURL, "claim_base_url": req.ClaimBaseURL} {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("%s must be an absolute HTTP(S) URL", name)
		}
	}
	return nil
}

func (h *SitePortalAdminHandler) List(c *gin.Context) {
	portals, err := h.portals.ListAll()
	if err != nil {
		InternalErrorResponse(c, "failed to list Site Portals")
		return
	}
	SuccessResponse(c, portals)
}

func (h *SitePortalAdminHandler) Create(c *gin.Context) {
	var req sitePortalAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}
	if err := validateSitePortalAdminRequest(req); err != nil {
		BadRequestResponse(c, err.Error())
		return
	}
	req.Code, req.DisplayName, req.EntryURL, req.ClaimBaseURL = strings.TrimSpace(req.Code), strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.EntryURL), strings.TrimSpace(req.ClaimBaseURL)
	portals, err := h.portals.ListAll()
	if err != nil {
		InternalErrorResponse(c, "failed to check Site Portal")
		return
	}
	for _, portal := range portals {
		if portal.Code == req.Code {
			BadRequestResponse(c, "Site Portal code already exists")
			return
		}
	}
	clientID := "site-portal-" + req.Code
	if exists, err := h.clients.ClientIDExists(clientID); err != nil {
		InternalErrorResponse(c, "failed to check OAuth client")
		return
	} else if exists {
		BadRequestResponse(c, "OAuth client_id already exists")
		return
	}
	rawSecret, err := auth.GenerateClientSecret()
	if err != nil {
		InternalErrorResponse(c, "failed to generate OAuth secret")
		return
	}
	secretHash, err := auth.HashClientSecret(rawSecret)
	if err != nil {
		InternalErrorResponse(c, "failed to hash OAuth secret")
		return
	}
	encrypted, err := h.cipher.Encrypt(rawSecret)
	if err != nil {
		InternalErrorResponse(c, "failed to encrypt OAuth secret")
		return
	}
	portal := &models.SitePortal{Code: req.Code, DisplayName: req.DisplayName, EntryURL: req.EntryURL, ClaimBaseURL: req.ClaimBaseURL}
	portalCode := req.Code
	client := &models.OAuth2Client{ClientID: clientID, ClientSecretHash: secretHash, ClientSecretEncrypted: encrypted, ClientType: "site_portal", SitePortalCode: &portalCode, AllowedScopes: "site-portal:access", Description: req.DisplayName, Enabled: true}
	if err := h.portals.CreateWithOAuthClient(portal, client); err != nil {
		InternalErrorResponse(c, "failed to create Site Portal")
		return
	}
	c.Header("Cache-Control", "no-store")
	CreatedResponse(c, gin.H{"portal": portal, "client_id": clientID, "client_secret": rawSecret})
}

func (h *SitePortalAdminHandler) Update(c *gin.Context) {
	code := strings.TrimSpace(c.Param("code"))
	portal, err := h.portals.GetByCodeAny(code)
	if err != nil {
		NotFoundResponse(c, "Site Portal not found")
		return
	}
	var req sitePortalAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}
	req.Code = code
	if err := validateSitePortalAdminRequest(req); err != nil {
		BadRequestResponse(c, err.Error())
		return
	}
	portal.DisplayName, portal.EntryURL, portal.ClaimBaseURL = strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.EntryURL), strings.TrimSpace(req.ClaimBaseURL)
	if err := h.portals.Update(portal); err != nil {
		InternalErrorResponse(c, "failed to update Site Portal")
		return
	}
	SuccessResponse(c, portal)
}

func (h *SitePortalAdminHandler) SetEnabled(c *gin.Context) {
	enabled := struct {
		Enabled bool `json:"enabled"`
	}{}
	if err := c.ShouldBindJSON(&enabled); err != nil {
		ValidationErrorResponse(c, err)
		return
	}
	if err := h.portals.SetEnabled(strings.TrimSpace(c.Param("code")), enabled.Enabled); err != nil {
		NotFoundResponse(c, "Site Portal not found")
		return
	}
	SuccessResponse(c, gin.H{"code": c.Param("code"), "enabled": enabled.Enabled})
}

func (h *SitePortalAdminHandler) Delete(c *gin.Context) {
	err := h.portals.Delete(strings.TrimSpace(c.Param("code")))
	switch {
	case err == nil:
		SuccessResponse(c, gin.H{"deleted": true})
	case errors.Is(err, database.ErrSitePortalHasMappings), errors.Is(err, database.ErrSitePortalAssigned):
		c.JSON(409, gin.H{"code": 409, "message": err.Error()})
	default:
		NotFoundResponse(c, "Site Portal not found")
	}
}

func (h *SitePortalAdminHandler) RotateSecret(c *gin.Context) {
	portalCode := strings.TrimSpace(c.Param("code"))
	client, err := h.clients.GetBySitePortalCode(portalCode)
	if err != nil {
		NotFoundResponse(c, "Site Portal OAuth client not found")
		return
	}
	rawSecret, err := auth.GenerateClientSecret()
	if err != nil {
		InternalErrorResponse(c, "failed to generate OAuth secret")
		return
	}
	hash, err := auth.HashClientSecret(rawSecret)
	if err != nil {
		InternalErrorResponse(c, "failed to hash OAuth secret")
		return
	}
	encrypted, err := h.cipher.Encrypt(rawSecret)
	if err != nil {
		InternalErrorResponse(c, "failed to encrypt OAuth secret")
		return
	}
	if err := h.clients.UpdateSecret(client.ID, hash, encrypted); err != nil {
		InternalErrorResponse(c, "failed to rotate OAuth secret")
		return
	}
	c.Header("Cache-Control", "no-store")
	SuccessResponse(c, gin.H{"client_id": client.ClientID, "client_secret": rawSecret})
}
