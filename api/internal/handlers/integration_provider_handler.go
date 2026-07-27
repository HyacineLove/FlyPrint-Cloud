package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/integration"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/security"

	"github.com/gin-gonic/gin"
)

// IntegrationProviderHandler manages third-party providers. Secret values are
// deliberately returned only from successful create and rotate responses.
type IntegrationProviderHandler struct {
	repo         *database.IntegrationProviderRepository
	printJobRepo *database.PrintJobRepository
	cipher       *security.ClientSecretCipher
	redisURL     string
}

func NewIntegrationProviderHandler(repo *database.IntegrationProviderRepository, printJobRepo *database.PrintJobRepository, cipher *security.ClientSecretCipher, redisURL string) *IntegrationProviderHandler {
	return &IntegrationProviderHandler{repo: repo, printJobRepo: printJobRepo, cipher: cipher, redisURL: redisURL}
}

type providerRequest struct {
	Code                  string `json:"code"`
	DisplayName           string `json:"display_name"`
	EntryURL              string `json:"entry_url"`
	CallbackBaseURL       string `json:"callback_base_url"`
	EntryVisible          bool   `json:"entry_visible"`
	Enabled               bool   `json:"enabled"`
	AllowedIPCIDRs        string `json:"allowed_ip_cidrs"`
	AllowedFileHosts      string `json:"allowed_file_hosts"`
	AllowPrivateFileHosts bool   `json:"allow_private_file_hosts"`
	MaxFileSize           int64  `json:"max_file_size"`
	AllowedMIMETypes      string `json:"allowed_mime_types"`
}

type providerListItem struct {
	*models.IntegrationProvider
	JobCount int `json:"job_count"`
}

var providerCodePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

func (h *IntegrationProviderHandler) List(c *gin.Context) {
	providers, err := h.repo.List()
	if err != nil {
		InternalErrorResponse(c, "failed to list integration providers")
		return
	}
	items := make([]providerListItem, 0, len(providers))
	for _, provider := range providers {
		item := providerListItem{IntegrationProvider: provider}
		if h.printJobRepo != nil {
			if count, countErr := h.printJobRepo.CountPrintJobsFiltered("", "", "", "", provider.Code, "", nil, nil); countErr == nil {
				item.JobCount = count
			}
		}
		items = append(items, item)
	}
	SuccessResponse(c, items)
}

func (h *IntegrationProviderHandler) Get(c *gin.Context) {
	provider, err := h.repo.Get(c.Param("code"), false)
	if err != nil || provider == nil {
		NotFoundResponse(c, "integration provider not found")
		return
	}
	SuccessResponse(c, provider)
}

func (h *IntegrationProviderHandler) Create(c *gin.Context) {
	var request providerRequest
	if err := c.ShouldBindJSON(&request); err != nil || !providerCodePattern.MatchString(request.Code) {
		BadRequestResponse(c, "invalid integration provider configuration")
		return
	}

	provider := providerFromRequest(request)
	// A provider is created disabled. Enabling is a distinct, guarded action
	// after both sides have completed their configuration and contract checks.
	provider.Enabled = false
	if err := h.validate(provider); err != nil {
		BadRequestResponse(c, err.Error())
		return
	}

	inboundSecret, err := newProviderHMACSecret()
	if err != nil {
		InternalErrorResponse(c, "failed to generate inbound HMAC secret")
		return
	}
	outboundSecret, err := newProviderHMACSecret()
	if err != nil {
		InternalErrorResponse(c, "failed to generate outbound HMAC secret")
		return
	}
	provider.InboundSecretEncrypted, err = h.cipher.Encrypt(inboundSecret)
	if err != nil {
		InternalErrorResponse(c, "failed to encrypt inbound HMAC secret")
		return
	}
	provider.OutboundSecretEncrypted, err = h.cipher.Encrypt(outboundSecret)
	if err != nil {
		InternalErrorResponse(c, "failed to encrypt outbound HMAC secret")
		return
	}
	if err := h.repo.Create(provider); err != nil {
		BadRequestResponse(c, "integration provider code already exists")
		return
	}

	c.Header("Cache-Control", "no-store")
	CreatedResponse(c, gin.H{
		"provider":             provider,
		"inbound_hmac_secret":  inboundSecret,
		"outbound_hmac_secret": outboundSecret,
	})
}

func (h *IntegrationProviderHandler) Update(c *gin.Context) {
	current, err := h.repo.Get(c.Param("code"), false)
	if err != nil || current == nil {
		NotFoundResponse(c, "integration provider not found")
		return
	}

	var request providerRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		BadRequestResponse(c, "invalid integration provider configuration")
		return
	}
	provider := providerFromRequest(request)
	provider.Code = current.Code // provider code is immutable after creation.
	if err := h.validate(provider); err != nil {
		BadRequestResponse(c, err.Error())
		return
	}
	if err := h.repo.Update(provider); err != nil {
		InternalErrorResponse(c, "failed to update integration provider")
		return
	}
	SuccessResponse(c, provider)
}

// UpdateEnabled changes only the provider traffic switch. It keeps the
// operation independent from editing connection and file policy fields.
func (h *IntegrationProviderHandler) UpdateEnabled(c *gin.Context) {
	h.updateSwitch(c, true)
}

// UpdateEntryVisible changes only whether the provider appears on the public
// terminal entry page.
func (h *IntegrationProviderHandler) UpdateEntryVisible(c *gin.Context) {
	h.updateSwitch(c, false)
}

func (h *IntegrationProviderHandler) updateSwitch(c *gin.Context, enabledSwitch bool) {
	current, err := h.repo.Get(c.Param("code"), false)
	if err != nil || current == nil {
		NotFoundResponse(c, "integration provider not found")
		return
	}

	var request struct {
		Enabled      *bool `json:"enabled"`
		EntryVisible *bool `json:"entry_visible"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		BadRequestResponse(c, "invalid integration provider switch")
		return
	}
	if enabledSwitch && request.Enabled == nil || !enabledSwitch && request.EntryVisible == nil {
		BadRequestResponse(c, "the requested switch value is required")
		return
	}

	if enabledSwitch {
		current.Enabled = *request.Enabled
		if err := h.validate(current); err != nil {
			BadRequestResponse(c, err.Error())
			return
		}
	} else {
		current.EntryVisible = *request.EntryVisible
	}

	updated, err := h.repo.UpdateFlags(current.Code, current.EntryVisible, current.Enabled)
	if err != nil {
		InternalErrorResponse(c, "failed to update integration provider switch")
		return
	}
	SuccessResponse(c, updated)
}

func (h *IntegrationProviderHandler) Rotate(c *gin.Context) {
	provider, err := h.repo.Get(c.Param("code"), false)
	if err != nil || provider == nil {
		NotFoundResponse(c, "integration provider not found")
		return
	}

	inboundSecret, err := newProviderHMACSecret()
	if err != nil {
		InternalErrorResponse(c, "failed to generate inbound HMAC secret")
		return
	}
	outboundSecret, err := newProviderHMACSecret()
	if err != nil {
		InternalErrorResponse(c, "failed to generate outbound HMAC secret")
		return
	}
	inboundEncrypted, err := h.cipher.Encrypt(inboundSecret)
	if err != nil {
		InternalErrorResponse(c, "failed to encrypt inbound HMAC secret")
		return
	}
	outboundEncrypted, err := h.cipher.Encrypt(outboundSecret)
	if err != nil {
		InternalErrorResponse(c, "failed to encrypt outbound HMAC secret")
		return
	}
	if err := h.repo.RotateSecrets(provider.Code, inboundEncrypted, outboundEncrypted); err != nil {
		InternalErrorResponse(c, "failed to rotate integration provider secrets")
		return
	}

	c.Header("Cache-Control", "no-store")
	SuccessResponse(c, gin.H{
		"inbound_hmac_secret":  inboundSecret,
		"outbound_hmac_secret": outboundSecret,
	})
}

func providerFromRequest(request providerRequest) *models.IntegrationProvider {
	return &models.IntegrationProvider{
		Code:                  request.Code,
		DisplayName:           strings.TrimSpace(request.DisplayName),
		EntryURL:              strings.TrimSpace(request.EntryURL),
		CallbackBaseURL:       strings.TrimSpace(request.CallbackBaseURL),
		EntryVisible:          request.EntryVisible,
		Enabled:               request.Enabled,
		AllowedIPCIDRs:        strings.TrimSpace(request.AllowedIPCIDRs),
		AllowedFileHosts:      strings.TrimSpace(request.AllowedFileHosts),
		AllowPrivateFileHosts: request.AllowPrivateFileHosts,
		MaxFileSize:           request.MaxFileSize,
		AllowedMIMETypes:      strings.TrimSpace(request.AllowedMIMETypes),
	}
}

func (h *IntegrationProviderHandler) validate(provider *models.IntegrationProvider) error {
	if provider.DisplayName == "" {
		return fmt.Errorf("display_name is required")
	}
	for _, rawURL := range []string{provider.EntryURL, provider.CallbackBaseURL} {
		parsedURL, err := url.Parse(rawURL)
		if err != nil || !integration.IsHTTPOrHTTPSScheme(parsedURL.Scheme) || parsedURL.Host == "" || parsedURL.User != nil {
			return fmt.Errorf("entry_url and callback_base_url must be HTTP or HTTPS URLs without user info")
		}
	}
	if provider.Enabled {
		if h.redisURL == "" {
			return fmt.Errorf("a Redis nonce store must be configured before enabling a provider")
		}
		if _, err := integration.NewNonceStore(h.redisURL); err != nil {
			return fmt.Errorf("integration Redis nonce store configuration is invalid")
		}
	}
	if provider.Enabled && (provider.AllowedIPCIDRs == "" || provider.AllowedFileHosts == "" || provider.MaxFileSize <= 0 || provider.AllowedMIMETypes == "") {
		return fmt.Errorf("CIDRs, file hosts, maximum file size, and MIME types are required before enabling a provider")
	}
	for _, rawCIDR := range splitCSV(provider.AllowedIPCIDRs) {
		if _, _, err := net.ParseCIDR(rawCIDR); err != nil {
			return fmt.Errorf("invalid allowed_ip_cidrs value")
		}
	}
	for _, host := range splitCSV(provider.AllowedFileHosts) {
		if strings.Contains(host, "://") || strings.Contains(host, "/") || host == "" {
			return fmt.Errorf("allowed_file_hosts must contain host names only")
		}
	}
	return nil
}

func splitCSV(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func newProviderHMACSecret() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(secret), nil
}
