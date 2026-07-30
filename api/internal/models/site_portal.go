package models

import "time"

// SitePortal describes one independently deployed user entry and its Edge claim endpoint.
// APITokenHash is used only for Site Portal -> Cloud service authentication.
type SitePortal struct {
	Code         string    `json:"code"`
	DisplayName  string    `json:"display_name"`
	EntryURL     string    `json:"entry_url"`
	ClaimBaseURL string    `json:"claim_base_url"`
	APITokenHash string    `json:"-"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
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
	NodeID            string
	TerminalSessionID string
	CloudUserID       string
	SitePortalCode    string
	ClaimBaseURL      string
}
