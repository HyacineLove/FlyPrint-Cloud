package models

import "time"

// OAuth2Client OAuth2客户端凭证
type OAuth2Client struct {
	ID                    string    `json:"id"`
	ClientID              string    `json:"client_id"`
	ClientSecretHash      string    `json:"-"` // 不返回密钥哈希
	ClientSecretEncrypted string    `json:"-"`
	ClientType            string    `json:"client_type"`    // edge_node / site_portal
	// EdgeNodeID binds an Edge client credential to exactly one node. It is
	// intentionally immutable after creation so a leaked credential cannot be
	// repointed to another terminal.
	EdgeNodeID           *string   `json:"edge_node_id,omitempty"`
	SitePortalCode       *string   `json:"site_portal_code,omitempty"`
	AllowedScopes         string    `json:"allowed_scopes"` // 空格分隔的权限列表
	Description           string    `json:"description"`
	Enabled               bool      `json:"enabled"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}
