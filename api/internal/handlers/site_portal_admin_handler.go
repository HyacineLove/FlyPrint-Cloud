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
	Code         string `json:"code"`
	DisplayName  string `json:"display_name" binding:"required"`
	EntryURL     string `json:"entry_url" binding:"required"`
	ClaimBaseURL string `json:"claim_base_url" binding:"required"`
}

type edgeSitePortalConfigRequest struct {
	PortalCodes       []string `json:"portal_codes"`
	DefaultPortalCode string   `json:"default_code"`
}

var sitePortalProviderIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)
var sitePortalProviderSecretRefPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)

type sitePortalProviderRequest struct {
	ProviderID       string `json:"provider_id"`
	DisplayName      string `json:"display_name"`
	Enabled          *bool  `json:"enabled"`
	SortOrder        int    `json:"sort_order"`
	FileBaseURL      string `json:"file_base_url"`
	SignSecretRef    string `json:"sign_secret_ref"`
	PortalAPIBaseURL string `json:"portal_api_base_url"`
	UploadEnabled    bool   `json:"upload_enabled"`
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

func validateSitePortalProviderRequest(req sitePortalProviderRequest) error {
	if !sitePortalProviderIDPattern.MatchString(strings.TrimSpace(req.ProviderID)) {
		return fmt.Errorf("invalid Provider ID")
	}
	if value := strings.TrimSpace(req.DisplayName); value == "" || len([]rune(value)) > 120 {
		return fmt.Errorf("display_name is required and must be at most 120 characters")
	}
	if req.SortOrder < 0 {
		return fmt.Errorf("sort_order must be non-negative")
	}
	if !sitePortalProviderSecretRefPattern.MatchString(strings.TrimSpace(req.SignSecretRef)) {
		return fmt.Errorf("sign_secret_ref must match [A-Z][A-Z0-9_]{0,31}")
	}
	for name, raw := range map[string]string{"file_base_url": req.FileBaseURL} {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("%s must be an absolute HTTP(S) URL without credentials or query", name)
		}
	}
	if req.UploadEnabled {
		parsed, err := url.Parse(strings.TrimSpace(req.PortalAPIBaseURL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("portal_api_base_url must be an absolute HTTP(S) URL when upload_enabled")
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
		if errors.Is(err, database.ErrSitePortalAssigned) {
			c.JSON(409, gin.H{"code": 409, "message": "请先移除使用该 Portal 的 Edge 关联"})
			return
		}
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

func (h *SitePortalAdminHandler) ListProviders(c *gin.Context) {
	portalCode := strings.TrimSpace(c.Param("code"))
	providers, revision, err := h.portals.ListProviders(portalCode)
	if err != nil {
		if errors.Is(err, database.ErrSitePortalNotFound) {
			NotFoundResponse(c, "Site Portal not found")
			return
		}
		InternalErrorResponse(c, "failed to list Site Portal Providers")
		return
	}
	SuccessResponse(c, gin.H{"provider_config_revision": revision, "providers": providers})
}

func (h *SitePortalAdminHandler) CreateProvider(c *gin.Context) {
	portalCode := strings.TrimSpace(c.Param("code"))
	var req sitePortalProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}
	if req.Enabled == nil {
		enabled := true
		req.Enabled = &enabled
	}
	req.ProviderID, req.DisplayName, req.FileBaseURL, req.SignSecretRef, req.PortalAPIBaseURL = strings.TrimSpace(req.ProviderID), strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.FileBaseURL), strings.TrimSpace(req.SignSecretRef), strings.TrimSpace(req.PortalAPIBaseURL)
	if err := validateSitePortalProviderRequest(req); err != nil {
		BadRequestResponse(c, err.Error())
		return
	}
	provider := &models.SitePortalProvider{SitePortalCode: portalCode, ProviderID: req.ProviderID, DisplayName: req.DisplayName, Enabled: *req.Enabled, SortOrder: req.SortOrder, FileBaseURL: req.FileBaseURL, SignSecretRef: req.SignSecretRef, PortalAPIBaseURL: req.PortalAPIBaseURL, UploadEnabled: req.UploadEnabled}
	if err := h.portals.CreateProvider(provider); err != nil {
		switch {
		case errors.Is(err, database.ErrSitePortalNotFound):
			NotFoundResponse(c, "Site Portal not found")
		case errors.Is(err, database.ErrSitePortalProviderExists):
			BadRequestResponse(c, "Provider ID already exists in this Site Portal")
		default:
			InternalErrorResponse(c, "failed to create Site Portal Provider")
		}
		return
	}
	SuccessResponse(c, provider)
}

func (h *SitePortalAdminHandler) UpdateProvider(c *gin.Context) {
	portalCode, providerID := strings.TrimSpace(c.Param("code")), strings.TrimSpace(c.Param("provider_id"))
	var req sitePortalProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}
	if req.Enabled == nil {
		BadRequestResponse(c, "enabled is required for Provider replacement")
		return
	}
	req.ProviderID, req.DisplayName, req.FileBaseURL, req.SignSecretRef, req.PortalAPIBaseURL = providerID, strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.FileBaseURL), strings.TrimSpace(req.SignSecretRef), strings.TrimSpace(req.PortalAPIBaseURL)
	if err := validateSitePortalProviderRequest(req); err != nil {
		BadRequestResponse(c, err.Error())
		return
	}
	enabled := *req.Enabled
	provider := &models.SitePortalProvider{SitePortalCode: portalCode, ProviderID: providerID, DisplayName: req.DisplayName, Enabled: enabled, SortOrder: req.SortOrder, FileBaseURL: req.FileBaseURL, SignSecretRef: req.SignSecretRef, PortalAPIBaseURL: req.PortalAPIBaseURL, UploadEnabled: req.UploadEnabled}
	if err := h.portals.UpdateProvider(provider); err != nil {
		switch {
		case errors.Is(err, database.ErrSitePortalNotFound), errors.Is(err, database.ErrSitePortalProviderNotFound):
			NotFoundResponse(c, "Site Portal Provider not found")
		default:
			InternalErrorResponse(c, "failed to update Site Portal Provider")
		}
		return
	}
	SuccessResponse(c, provider)
}

func (h *SitePortalAdminHandler) SetProviderEnabled(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}
	portalCode, providerID := strings.TrimSpace(c.Param("code")), strings.TrimSpace(c.Param("provider_id"))
	if err := h.portals.SetProviderEnabled(portalCode, providerID, req.Enabled); err != nil {
		if errors.Is(err, database.ErrSitePortalNotFound) || errors.Is(err, database.ErrSitePortalProviderNotFound) {
			NotFoundResponse(c, "Site Portal Provider not found")
			return
		}
		InternalErrorResponse(c, "failed to update Site Portal Provider state")
		return
	}
	SuccessResponse(c, gin.H{"provider_id": providerID, "enabled": req.Enabled})
}

func (h *SitePortalAdminHandler) DeleteProvider(c *gin.Context) {
	portalCode, providerID := strings.TrimSpace(c.Param("code")), strings.TrimSpace(c.Param("provider_id"))
	if err := h.portals.DeleteProvider(portalCode, providerID); err != nil {
		if errors.Is(err, database.ErrSitePortalNotFound) || errors.Is(err, database.ErrSitePortalProviderNotFound) {
			NotFoundResponse(c, "Site Portal Provider not found")
			return
		}
		InternalErrorResponse(c, "failed to delete Site Portal Provider")
		return
	}
	SuccessResponse(c, gin.H{"deleted": true, "provider_id": providerID})
}

func validateEdgeSitePortalConfig(req edgeSitePortalConfigRequest) error {
	if len(req.PortalCodes) == 0 {
		return fmt.Errorf("至少选择一个 Site Portal")
	}
	seen := make(map[string]struct{}, len(req.PortalCodes))
	for _, raw := range req.PortalCodes {
		code := strings.TrimSpace(raw)
		if !sitePortalCodePattern.MatchString(code) {
			return fmt.Errorf("Site Portal 编码无效")
		}
		if _, exists := seen[code]; exists {
			return fmt.Errorf("Site Portal 不能重复选择")
		}
		seen[code] = struct{}{}
	}
	if !sitePortalCodePattern.MatchString(strings.TrimSpace(req.DefaultPortalCode)) {
		return fmt.Errorf("默认 Site Portal 编码无效")
	}
	if _, ok := seen[strings.TrimSpace(req.DefaultPortalCode)]; !ok {
		return database.ErrDefaultPortalNotAssigned
	}
	return nil
}

func (h *SitePortalAdminHandler) GetEdgeSitePortals(c *gin.Context) {
	config, err := h.portals.GetEdgeSitePortalConfig(strings.TrimSpace(c.Param("id")))
	if err != nil {
		if errors.Is(err, database.ErrEdgeNodeNotFound) {
			NotFoundResponse(c, "Edge Node not found")
			return
		}
		InternalErrorResponse(c, "failed to get Edge Site Portal configuration")
		return
	}
	SuccessResponse(c, config)
}

func (h *SitePortalAdminHandler) UpdateEdgeSitePortals(c *gin.Context) {
	var req edgeSitePortalConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}
	req.DefaultPortalCode = strings.TrimSpace(req.DefaultPortalCode)
	for i := range req.PortalCodes {
		req.PortalCodes[i] = strings.TrimSpace(req.PortalCodes[i])
	}
	if err := validateEdgeSitePortalConfig(req); err != nil {
		switch {
		case errors.Is(err, database.ErrDefaultPortalNotAssigned):
			c.JSON(409, gin.H{"code": 409, "message": "默认 Site Portal 必须属于可选列表"})
		default:
			BadRequestResponse(c, err.Error())
		}
		return
	}
	if err := h.portals.ReplaceEdgeSitePortals(strings.TrimSpace(c.Param("id")), req.PortalCodes, req.DefaultPortalCode); err != nil {
		switch {
		case errors.Is(err, database.ErrEdgeNodeNotFound):
			NotFoundResponse(c, "Edge Node not found")
		case errors.Is(err, database.ErrSitePortalNotFound):
			BadRequestResponse(c, "Site Portal 不存在")
		case errors.Is(err, database.ErrSitePortalDisabled):
			BadRequestResponse(c, "Site Portal 未启用")
		case errors.Is(err, database.ErrDefaultPortalNotAssigned):
			c.JSON(409, gin.H{"code": 409, "message": "默认 Site Portal 必须属于可选列表"})
		default:
			InternalErrorResponse(c, "failed to update Edge Site Portal configuration")
		}
		return
	}
	config, err := h.portals.GetEdgeSitePortalConfig(strings.TrimSpace(c.Param("id")))
	if err != nil {
		InternalErrorResponse(c, "failed to read Edge Site Portal configuration")
		return
	}
	SuccessResponse(c, config)
}
