package models

import "time"

// SitePortal describes one independently deployed user entry and its Edge claim endpoint.
type SitePortal struct {
	Code               string    `json:"code"`
	DisplayName        string    `json:"display_name"`
	EntryURL           string    `json:"entry_url"`
	ClaimBaseURL       string    `json:"claim_base_url"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	OAuthClientID      string    `json:"oauth_client_id,omitempty"`
	OAuthClientEnabled bool      `json:"oauth_client_enabled,omitempty"`
	EdgeNodeCount      int       `json:"edge_node_count"`
}

// SitePortalProvider is a Cloud-owned Provider binding for one Site Portal.
// It intentionally contains only a secret reference; the signing key itself
// remains local to the Site Portal deployment.
type SitePortalProvider struct {
	SitePortalCode   string    `json:"site_portal_code,omitempty"`
	ProviderID       string    `json:"provider_id"`
	DisplayName      string    `json:"display_name"`
	Enabled          bool      `json:"enabled"`
	SortOrder        int       `json:"sort_order"`
	FileBaseURL      string    `json:"file_base_url"`
	SignSecretRef    string    `json:"sign_secret_ref"`
	PortalAPIBaseURL string    `json:"portal_api_base_url,omitempty"`
	UploadEnabled    bool      `json:"upload_enabled,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// EdgeSitePortalConfig is the Cloud-owned entry configuration for one Edge
// node. The default portal must always be one of Portals.
type EdgeSitePortalConfig struct {
	EdgeNodeID        string        `json:"edge_node_id"`
	Portals           []*SitePortal `json:"portals"`
	DefaultPortalCode string        `json:"default_code"`
}

// ExternalIdentity maps a stable identity from one Site Portal to a Cloud user.
type ExternalIdentity struct {
	SitePortalCode string    `json:"site_portal_code"`
	ExternalUserID string    `json:"external_user_id"`
	CloudUserID    string    `json:"cloud_user_id"`
	DisplayName    string    `json:"display_name"`
	LastLoginAt    time.Time `json:"last_login_at"`
}

// PortalLoginCompletion is the non-secret Cloud result needed to notify an Edge.
type PortalLoginCompletion struct {
	NodeID                string
	TerminalSessionID     string
	CloudUserID           string
	SitePortalCode        string
	SitePortalDisplayName string
	ClaimBaseURL          string
	ReadyEventID          string
}
