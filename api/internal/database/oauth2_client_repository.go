package database

import (
	"database/sql"
	"fmt"

	"fly-print-cloud/api/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// OAuth2ClientRepository OAuth2客户端数据访问层
type OAuth2ClientRepository struct {
	db *DB
}

// NewOAuth2ClientRepository 创建 OAuth2 客户端仓库
func NewOAuth2ClientRepository(db *DB) *OAuth2ClientRepository {
	return &OAuth2ClientRepository{db: db}
}

// GetByClientID 根据 client_id 获取客户端
func (r *OAuth2ClientRepository) GetByClientID(clientID string) (*models.OAuth2Client, error) {
	client := &models.OAuth2Client{}
	query := `
		SELECT id, client_id, client_secret_hash, client_secret_encrypted, client_type, edge_node_id, site_portal_code, allowed_scopes,
		       description, enabled, created_at, updated_at
		FROM oauth2_clients WHERE client_id = $1`

	err := r.db.QueryRow(query, clientID).Scan(
		&client.ID, &client.ClientID, &client.ClientSecretHash, &client.ClientSecretEncrypted,
		&client.ClientType, &client.EdgeNodeID, &client.SitePortalCode, &client.AllowedScopes, &client.Description,
		&client.Enabled, &client.CreatedAt, &client.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("oauth2 client not found")
		}
		return nil, fmt.Errorf("failed to get oauth2 client: %w", err)
	}
	return client, nil
}

// GetByID 根据 UUID 获取客户端
func (r *OAuth2ClientRepository) GetByID(id string) (*models.OAuth2Client, error) {
	client := &models.OAuth2Client{}
	query := `
		SELECT id, client_id, client_secret_hash, client_secret_encrypted, client_type, edge_node_id, site_portal_code, allowed_scopes,
		       description, enabled, created_at, updated_at
		FROM oauth2_clients WHERE id = $1`

	err := r.db.QueryRow(query, id).Scan(
		&client.ID, &client.ClientID, &client.ClientSecretHash, &client.ClientSecretEncrypted,
		&client.ClientType, &client.EdgeNodeID, &client.SitePortalCode, &client.AllowedScopes, &client.Description,
		&client.Enabled, &client.CreatedAt, &client.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("oauth2 client not found")
		}
		return nil, fmt.Errorf("failed to get oauth2 client: %w", err)
	}
	return client, nil
}

func (r *OAuth2ClientRepository) GetBySitePortalCode(code string) (*models.OAuth2Client, error) {
	client := &models.OAuth2Client{}
	err := r.db.QueryRow(`SELECT id,client_id,client_secret_hash,client_secret_encrypted,client_type,edge_node_id,site_portal_code,allowed_scopes,description,enabled,created_at,updated_at
		FROM oauth2_clients WHERE client_type='site_portal' AND site_portal_code=$1`, code).Scan(
		&client.ID, &client.ClientID, &client.ClientSecretHash, &client.ClientSecretEncrypted, &client.ClientType,
		&client.EdgeNodeID, &client.SitePortalCode, &client.AllowedScopes, &client.Description, &client.Enabled, &client.CreatedAt, &client.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("oauth2 client not found")
		}
		return nil, fmt.Errorf("failed to get site portal oauth2 client: %w", err)
	}
	return client, nil
}

// Create 创建客户端（client_secret_hash 必须预先通过 bcrypt 哈希）
func (r *OAuth2ClientRepository) Create(client *models.OAuth2Client) error {
	query := `
		INSERT INTO oauth2_clients (client_id, client_secret_hash, client_secret_encrypted, client_type, edge_node_id, site_portal_code, allowed_scopes, description, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at`

	err := r.db.QueryRow(query,
		client.ClientID, client.ClientSecretHash, client.ClientSecretEncrypted, client.ClientType,
		client.EdgeNodeID, client.SitePortalCode, client.AllowedScopes, client.Description, client.Enabled,
	).Scan(&client.ID, &client.CreatedAt, &client.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create oauth2 client: %w", err)
	}
	return nil
}

// Update 更新客户端信息（不含密钥）
func (r *OAuth2ClientRepository) Update(client *models.OAuth2Client) error {
	query := `
		UPDATE oauth2_clients
		SET allowed_scopes = $2, description = $3, enabled = $4
		WHERE id = $1
		RETURNING updated_at`

	err := r.db.QueryRow(query,
		client.ID, client.AllowedScopes, client.Description, client.Enabled,
	).Scan(&client.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to update oauth2 client: %w", err)
	}
	return nil
}

// UpdateSecret 更新客户端密钥
func (r *OAuth2ClientRepository) UpdateSecret(id, secretHash, encryptedSecret string) error {
	query := `UPDATE oauth2_clients SET client_secret_hash = $2, client_secret_encrypted = $3 WHERE id = $1`
	result, err := r.db.Exec(query, id, secretHash, encryptedSecret)
	if err != nil {
		return fmt.Errorf("failed to update oauth2 client secret: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("oauth2 client not found")
	}
	return nil
}

// Delete 删除客户端
func (r *OAuth2ClientRepository) Delete(id string) error {
	query := `DELETE FROM oauth2_clients WHERE id = $1`
	result, err := r.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to delete oauth2 client: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("oauth2 client not found")
	}
	return nil
}

// List 获取客户端列表（分页）
func (r *OAuth2ClientRepository) List(offset, limit int) ([]*models.OAuth2Client, int, error) {
	var total int
	countQuery := `SELECT COUNT(*) FROM oauth2_clients`
	if err := r.db.QueryRow(countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count oauth2 clients: %w", err)
	}

	query := `
		SELECT id, client_id, client_type, edge_node_id, site_portal_code, allowed_scopes,
		       description, enabled, created_at, updated_at
		FROM oauth2_clients
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`

	rows, err := r.db.Query(query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query oauth2 clients: %w", err)
	}
	defer rows.Close()

	var clients []*models.OAuth2Client
	for rows.Next() {
		client := &models.OAuth2Client{}
		if err := rows.Scan(
			&client.ID, &client.ClientID, &client.ClientType, &client.EdgeNodeID, &client.SitePortalCode,
			&client.AllowedScopes, &client.Description, &client.Enabled,
			&client.CreatedAt, &client.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan oauth2 client: %w", err)
		}
		clients = append(clients, client)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows error: %w", err)
	}
	return clients, total, nil
}

// VerifySecret 验证客户端密钥
func (r *OAuth2ClientRepository) VerifySecret(client *models.OAuth2Client, rawSecret string) bool {
	return bcrypt.CompareHashAndPassword([]byte(client.ClientSecretHash), []byte(rawSecret)) == nil
}

// ClientIDExists 检查 client_id 是否已存在
func (r *OAuth2ClientRepository) ClientIDExists(clientID string) (bool, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM oauth2_clients WHERE client_id = $1`, clientID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check client_id existence: %w", err)
	}
	return count > 0, nil
}

// IsBoundEdgeNodeUsable verifies that the one-machine credential still belongs
// to a non-deleted, enabled node. A newly activated node is allowed to obtain a
// token for its one-time profile report before it becomes active.
func (r *OAuth2ClientRepository) IsBoundEdgeNodeUsable(clientID string) (bool, error) {
	var usable bool
	err := r.db.QueryRow(`
		SELECT n.enabled AND n.deleted_at IS NULL
			AND n.registration_state IN ('registered', 'active')
		FROM oauth2_clients c
		JOIN edge_nodes n ON n.id = c.edge_node_id
		WHERE c.client_id = $1 AND c.client_type = 'edge_node'`, clientID).Scan(&usable)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("check bound edge node: %w", err)
	}
	return usable, nil
}

func (r *OAuth2ClientRepository) IsBoundSitePortalUsable(clientID string) (bool, error) {
	var usable bool
	err := r.db.QueryRow(`
		SELECT p.enabled
		FROM oauth2_clients c
		JOIN site_portals p ON p.code = c.site_portal_code
		WHERE c.client_id = $1 AND c.client_type = 'site_portal'`, clientID).Scan(&usable)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("check bound site portal: %w", err)
	}
	return usable, nil
}
