package database

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"fly-print-cloud/api/internal/models"

	"github.com/lib/pq"
)

var (
	ErrSitePortalUnauthorized     = errors.New("site portal unauthorized")
	ErrSitePortalHasMappings      = errors.New("site portal has identity mappings")
	ErrSitePortalAssigned         = errors.New("site portal is assigned to an edge node")
	ErrEdgeNodeNotFound           = errors.New("edge node not found")
	ErrSitePortalNotFound         = errors.New("site portal not found")
	ErrSitePortalDisabled         = errors.New("site portal is disabled")
	ErrDefaultPortalNotAssigned   = errors.New("default site portal is not assigned to the edge node")
	ErrSitePortalProviderNotFound = errors.New("site portal provider not found")
	ErrSitePortalProviderExists   = errors.New("site portal provider already exists")
)

type SitePortalRepository struct {
	db *DB
}

func NewSitePortalRepository(db *DB) *SitePortalRepository {
	return &SitePortalRepository{db: db}
}

func scanSitePortal(scanner interface{ Scan(...any) error }) (*models.SitePortal, error) {
	portal := &models.SitePortal{}
	if err := scanner.Scan(
		&portal.Code,
		&portal.DisplayName,
		&portal.EntryURL,
		&portal.ClaimBaseURL,
		&portal.Enabled,
		&portal.CreatedAt,
		&portal.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return portal, nil
}

func (r *SitePortalRepository) GetByCode(code string) (*models.SitePortal, error) {
	portal, err := r.getByCode(code, true)
	if err != nil {
		return nil, err
	}
	return portal, nil
}

// GetByCodeAny returns a portal regardless of its enabled state. Admin
// management needs this form so a disabled portal can still be edited or
// re-enabled.
func (r *SitePortalRepository) GetByCodeAny(code string) (*models.SitePortal, error) {
	return r.getByCode(code, false)
}

func (r *SitePortalRepository) getByCode(code string, enabledOnly bool) (*models.SitePortal, error) {
	query := `SELECT code,display_name,entry_url,claim_base_url,enabled,created_at,updated_at
		FROM site_portals WHERE code=$1`
	if enabledOnly {
		query += ` AND enabled=true`
	}
	portal, err := scanSitePortal(r.db.QueryRow(query, code))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("site portal not found")
		}
		return nil, fmt.Errorf("get site portal: %w", err)
	}
	return portal, nil
}

func (r *SitePortalRepository) ListAll() ([]*models.SitePortal, error) {
	rows, err := r.db.Query(`SELECT p.code,p.display_name,p.entry_url,p.claim_base_url,p.enabled,
		p.created_at,p.updated_at,COALESCE(c.client_id,''),COALESCE(c.enabled,false),
		(SELECT COUNT(*) FROM edge_site_portals assignment WHERE assignment.site_portal_code=p.code)
		FROM site_portals p LEFT JOIN oauth2_clients c
			ON c.site_portal_code=p.code AND c.client_type='site_portal'
		ORDER BY p.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list site portals: %w", err)
	}
	defer rows.Close()
	var portals []*models.SitePortal
	for rows.Next() {
		portal := &models.SitePortal{}
		if err := rows.Scan(&portal.Code, &portal.DisplayName, &portal.EntryURL, &portal.ClaimBaseURL,
			&portal.Enabled, &portal.CreatedAt, &portal.UpdatedAt, &portal.OAuthClientID, &portal.OAuthClientEnabled,
			&portal.EdgeNodeCount); err != nil {
			return nil, fmt.Errorf("scan site portal: %w", err)
		}
		portals = append(portals, portal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list site portals rows: %w", err)
	}
	return portals, nil
}

func (r *SitePortalRepository) Update(portal *models.SitePortal) error {
	result, err := r.db.Exec(`UPDATE site_portals SET display_name=$2,entry_url=$3,claim_base_url=$4,updated_at=CURRENT_TIMESTAMP WHERE code=$1`,
		portal.Code, portal.DisplayName, portal.EntryURL, portal.ClaimBaseURL)
	if err != nil {
		return fmt.Errorf("update site portal: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("site portal not found")
	}
	return nil
}

func (r *SitePortalRepository) SetEnabled(code string, enabled bool) error {
	tx, err := r.db.BeginTx()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if !enabled {
		var existingCode string
		if err := tx.QueryRow(`SELECT code FROM site_portals WHERE code=$1 FOR UPDATE`, code).Scan(&existingCode); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("site portal not found")
			}
			return fmt.Errorf("lock Site Portal: %w", err)
		}
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM edge_site_portals WHERE site_portal_code=$1`, code).Scan(&count); err != nil {
			return fmt.Errorf("check Site Portal assignments: %w", err)
		}
		if count > 0 {
			return ErrSitePortalAssigned
		}
	}
	result, err := tx.Exec(`UPDATE site_portals SET enabled=$2,updated_at=CURRENT_TIMESTAMP WHERE code=$1`, code, enabled)
	if err != nil {
		return fmt.Errorf("update site portal state: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("site portal not found")
	}
	if _, err := tx.Exec(`UPDATE oauth2_clients SET enabled=$2,updated_at=CURRENT_TIMESTAMP WHERE client_type='site_portal' AND site_portal_code=$1`, code, enabled); err != nil {
		return fmt.Errorf("update site portal OAuth state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// Delete removes a Site Portal and its bound OAuth client. Deletion is
// intentionally blocked while identities or Edge defaults still reference the
// portal, so an administrator must migrate those references explicitly.
func (r *SitePortalRepository) Delete(code string) error {
	tx, err := r.db.BeginTx()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM external_identities WHERE site_portal_code=$1`, code).Scan(&count); err != nil {
		return fmt.Errorf("check Site Portal mappings: %w", err)
	}
	if count > 0 {
		return ErrSitePortalHasMappings
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM edge_site_portals WHERE site_portal_code=$1`, code).Scan(&count); err != nil {
		return fmt.Errorf("check Site Portal assignments: %w", err)
	}
	if count > 0 {
		return ErrSitePortalAssigned
	}
	if _, err := tx.Exec(`DELETE FROM oauth2_clients WHERE client_type='site_portal' AND site_portal_code=$1`, code); err != nil {
		return fmt.Errorf("delete Site Portal OAuth client: %w", err)
	}
	result, err := tx.Exec(`DELETE FROM site_portals WHERE code=$1`, code)
	if err != nil {
		return fmt.Errorf("delete Site Portal: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("site portal not found")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *SitePortalRepository) CreateWithOAuthClient(portal *models.SitePortal, client *models.OAuth2Client) error {
	tx, err := r.db.BeginTx()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`INSERT INTO site_portals(code,display_name,entry_url,claim_base_url,enabled) VALUES($1,$2,$3,$4,true)`,
		portal.Code, portal.DisplayName, portal.EntryURL, portal.ClaimBaseURL); err != nil {
		return fmt.Errorf("create site portal: %w", err)
	}
	if err := tx.QueryRow(`INSERT INTO oauth2_clients(client_id,client_secret_hash,client_secret_encrypted,client_type,site_portal_code,allowed_scopes,description,enabled)
		VALUES($1,$2,$3,'site_portal',$4,$5,$6,true) RETURNING id,created_at,updated_at`,
		client.ClientID, client.ClientSecretHash, client.ClientSecretEncrypted, portal.Code, client.AllowedScopes, client.Description).
		Scan(&client.ID, &client.CreatedAt, &client.UpdatedAt); err != nil {
		return fmt.Errorf("create site portal OAuth client: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *SitePortalRepository) GetDefaultForNode(nodeID string) (*models.SitePortal, error) {
	portal, err := scanSitePortal(r.db.QueryRow(`SELECT portal.code,portal.display_name,portal.entry_url,portal.claim_base_url,
		portal.enabled,portal.created_at,portal.updated_at
		FROM edge_nodes node
		JOIN edge_site_portals assignment ON assignment.edge_node_id=node.id AND assignment.is_default=true
		JOIN site_portals portal ON portal.code=assignment.site_portal_code
		WHERE node.id=$1 AND node.deleted_at IS NULL AND node.enabled=true AND portal.enabled=true`, nodeID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("default site portal not found")
		}
		return nil, fmt.Errorf("get default site portal: %w", err)
	}
	return portal, nil
}

// UpsertBootstrap installs one explicitly configured portal.
func (r *SitePortalRepository) UpsertBootstrap(portal *models.SitePortal) error {
	if portal == nil || portal.Code == "" {
		return fmt.Errorf("site portal bootstrap configuration is incomplete")
	}
	_, err := r.db.Exec(`INSERT INTO site_portals
		(code,display_name,entry_url,claim_base_url,enabled)
		VALUES ($1,$2,$3,$4,true)
		ON CONFLICT(code) DO UPDATE SET display_name=EXCLUDED.display_name,
			entry_url=EXCLUDED.entry_url,claim_base_url=EXCLUDED.claim_base_url,
			enabled=true,updated_at=CURRENT_TIMESTAMP`,
		portal.Code, portal.DisplayName, portal.EntryURL, portal.ClaimBaseURL)
	if err != nil {
		return fmt.Errorf("upsert bootstrap site portal: %w", err)
	}
	if _, err := r.db.Exec(`INSERT INTO edge_site_portals(edge_node_id,site_portal_code,is_default)
		SELECT node.id,$1,true FROM edge_nodes node
		WHERE node.deleted_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM edge_site_portals assignment WHERE assignment.edge_node_id=node.id)
		ON CONFLICT DO NOTHING`, portal.Code); err != nil {
		return fmt.Errorf("sync bootstrap Site Portal assignments: %w", err)
	}
	return nil
}

func lockEdgeNode(tx *Tx, nodeID string) error {
	var id string
	if err := tx.QueryRow(`SELECT id FROM edge_nodes WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, nodeID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEdgeNodeNotFound
		}
		return fmt.Errorf("lock edge node: %w", err)
	}
	return nil
}

func (r *SitePortalRepository) GetEdgeSitePortalConfig(nodeID string) (*models.EdgeSitePortalConfig, error) {
	config := &models.EdgeSitePortalConfig{EdgeNodeID: nodeID, Portals: []*models.SitePortal{}}
	var edgeNodeID string
	if err := r.db.QueryRow(`SELECT id FROM edge_nodes WHERE id=$1 AND deleted_at IS NULL`, nodeID).Scan(&edgeNodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEdgeNodeNotFound
		}
		return nil, fmt.Errorf("get edge node: %w", err)
	}
	rows, err := r.db.Query(`SELECT p.code,p.display_name,p.entry_url,p.claim_base_url,p.enabled,p.created_at,p.updated_at,assignment.is_default
		FROM edge_site_portals assignment
		JOIN site_portals p ON p.code=assignment.site_portal_code
		WHERE assignment.edge_node_id=$1
		ORDER BY p.display_name,p.code`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list edge Site Portal assignments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		portal := &models.SitePortal{}
		var isDefault bool
		if err := rows.Scan(&portal.Code, &portal.DisplayName, &portal.EntryURL, &portal.ClaimBaseURL,
			&portal.Enabled, &portal.CreatedAt, &portal.UpdatedAt, &isDefault); err != nil {
			return nil, fmt.Errorf("scan edge Site Portal assignment: %w", err)
		}
		if isDefault {
			config.DefaultPortalCode = portal.Code
		}
		config.Portals = append(config.Portals, portal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list edge Site Portal assignment rows: %w", err)
	}
	return config, nil
}

func scanSitePortalFields(scanner interface{ Scan(...any) error }, portal *models.SitePortal) error {
	return scanner.Scan(
		&portal.Code,
		&portal.DisplayName,
		&portal.EntryURL,
		&portal.ClaimBaseURL,
		&portal.Enabled,
		&portal.CreatedAt,
		&portal.UpdatedAt,
	)
}

func normalizePortalCodes(codes []string) ([]string, error) {
	seen := make(map[string]struct{}, len(codes))
	result := make([]string, 0, len(codes))
	for _, raw := range codes {
		code := strings.TrimSpace(raw)
		if code == "" {
			return nil, fmt.Errorf("Site Portal code cannot be empty")
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		result = append(result, code)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("at least one Site Portal is required")
	}
	sort.Strings(result)
	return result, nil
}

func (r *SitePortalRepository) ReplaceEdgeSitePortals(nodeID string, portalCodes []string, defaultPortalCode string) error {
	codes, err := normalizePortalCodes(portalCodes)
	if err != nil {
		return err
	}
	defaultPortalCode = strings.TrimSpace(defaultPortalCode)
	if defaultPortalCode == "" {
		return fmt.Errorf("default Site Portal is required")
	}
	if !containsCode(codes, defaultPortalCode) {
		return ErrDefaultPortalNotAssigned
	}

	tx, err := r.db.BeginTx()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := lockEdgeNode(tx, nodeID); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT code,enabled FROM site_portals WHERE code=ANY($1) FOR SHARE`, pq.Array(codes))
	if err != nil {
		return fmt.Errorf("check Site Portal assignments: %w", err)
	}
	found := make(map[string]bool, len(codes))
	enabledStates := make(map[string]bool, len(codes))
	for rows.Next() {
		var code string
		var enabled bool
		if err := rows.Scan(&code, &enabled); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan Site Portal assignment: %w", err)
		}
		found[code] = true
		enabledStates[code] = enabled
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("check Site Portal assignment rows: %w", err)
	}
	_ = rows.Close()
	for _, code := range codes {
		if _, ok := found[code]; !ok {
			return ErrSitePortalNotFound
		}
	}
	for _, code := range codes {
		if !enabledStates[code] {
			return ErrSitePortalDisabled
		}
	}
	if _, err := tx.Exec(`DELETE FROM edge_site_portals WHERE edge_node_id=$1`, nodeID); err != nil {
		return fmt.Errorf("remove edge Site Portal assignments: %w", err)
	}
	for _, code := range codes {
		if _, err := tx.Exec(`INSERT INTO edge_site_portals(edge_node_id,site_portal_code,is_default) VALUES($1,$2,$3)`, nodeID, code, code == defaultPortalCode); err != nil {
			return fmt.Errorf("assign Site Portal: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func containsCode(codes []string, target string) bool {
	for _, code := range codes {
		if code == target {
			return true
		}
	}
	return false
}

func (r *SitePortalRepository) IsAssignedToNode(nodeID, portalCode string) (bool, error) {
	var assigned bool
	if err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM edge_site_portals WHERE edge_node_id=$1 AND site_portal_code=$2)`, nodeID, portalCode).Scan(&assigned); err != nil {
		return false, fmt.Errorf("check Site Portal assignment: %w", err)
	}
	return assigned, nil
}

// ListProviders returns the complete Provider configuration for an admin. The
// revision is returned separately so an empty list is still distinguishable
// from a missing Portal.
func (r *SitePortalRepository) ListProviders(portalCode string) ([]*models.SitePortalProvider, int64, error) {
	portalCode = strings.TrimSpace(portalCode)
	var revision int64
	if err := r.db.QueryRow(`SELECT provider_config_revision FROM site_portals WHERE code=$1`, portalCode).Scan(&revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, 0, ErrSitePortalNotFound
		}
		return nil, 0, fmt.Errorf("get Site Portal Provider revision: %w", err)
	}
	rows, err := r.db.Query(`SELECT site_portal_code,provider_id,display_name,enabled,sort_order,file_base_url,sign_secret_ref,
		portal_api_base_url,upload_enabled,updated_at
		FROM site_portal_providers WHERE site_portal_code=$1 ORDER BY sort_order,provider_id`, portalCode)
	if err != nil {
		return nil, 0, fmt.Errorf("list Site Portal Providers: %w", err)
	}
	defer rows.Close()
	providers, err := scanSitePortalProviders(rows)
	if err != nil {
		return nil, 0, err
	}
	return providers, revision, nil
}

// ListEnabledProviders is the Portal context view. It never returns disabled
// Provider bindings and never includes a signing key.
func (r *SitePortalRepository) ListEnabledProviders(portalCode string) ([]*models.SitePortalProvider, int64, error) {
	portalCode = strings.TrimSpace(portalCode)
	var revision int64
	if err := r.db.QueryRow(`SELECT provider_config_revision FROM site_portals WHERE code=$1 AND enabled=true`, portalCode).Scan(&revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, 0, ErrSitePortalNotFound
		}
		return nil, 0, fmt.Errorf("get enabled Site Portal Provider revision: %w", err)
	}
	rows, err := r.db.Query(`SELECT site_portal_code,provider_id,display_name,enabled,sort_order,file_base_url,sign_secret_ref,
		portal_api_base_url,upload_enabled,updated_at
		FROM site_portal_providers WHERE site_portal_code=$1 AND enabled=true ORDER BY sort_order,provider_id`, portalCode)
	if err != nil {
		return nil, 0, fmt.Errorf("list enabled Site Portal Providers: %w", err)
	}
	defer rows.Close()
	providers, err := scanSitePortalProviders(rows)
	if err != nil {
		return nil, 0, err
	}
	return providers, revision, nil
}

func scanSitePortalProviders(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*models.SitePortalProvider, error) {
	var providers []*models.SitePortalProvider
	for rows.Next() {
		provider := &models.SitePortalProvider{}
		var portalAPIBaseURL sql.NullString
		if err := rows.Scan(&provider.SitePortalCode, &provider.ProviderID, &provider.DisplayName,
			&provider.Enabled, &provider.SortOrder, &provider.FileBaseURL, &provider.SignSecretRef,
			&portalAPIBaseURL, &provider.UploadEnabled, &provider.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan Site Portal Provider: %w", err)
		}
		provider.PortalAPIBaseURL = portalAPIBaseURL.String
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list Site Portal Provider rows: %w", err)
	}
	return providers, nil
}

func lockSitePortal(tx *Tx, portalCode string) error {
	var code string
	if err := tx.QueryRow(`SELECT code FROM site_portals WHERE code=$1 FOR UPDATE`, portalCode).Scan(&code); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSitePortalNotFound
		}
		return fmt.Errorf("lock Site Portal: %w", err)
	}
	return nil
}

func bumpSitePortalProviderRevision(tx *Tx, portalCode string) error {
	if _, err := tx.Exec(`UPDATE site_portals SET provider_config_revision=provider_config_revision+1,updated_at=CURRENT_TIMESTAMP WHERE code=$1`, portalCode); err != nil {
		return fmt.Errorf("bump Site Portal Provider revision: %w", err)
	}
	return nil
}

func (r *SitePortalRepository) CreateProvider(provider *models.SitePortalProvider) error {
	if provider == nil {
		return fmt.Errorf("Site Portal Provider is required")
	}
	provider.SitePortalCode = strings.TrimSpace(provider.SitePortalCode)
	tx, err := r.db.BeginTx()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := lockSitePortal(tx, provider.SitePortalCode); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO site_portal_providers(site_portal_code,provider_id,display_name,enabled,sort_order,file_base_url,sign_secret_ref,portal_api_base_url,upload_enabled)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, provider.SitePortalCode, provider.ProviderID, provider.DisplayName,
		provider.Enabled, provider.SortOrder, provider.FileBaseURL, provider.SignSecretRef, provider.PortalAPIBaseURL, provider.UploadEnabled)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return ErrSitePortalProviderExists
		}
		return fmt.Errorf("create Site Portal Provider: %w", err)
	}
	if err := bumpSitePortalProviderRevision(tx, provider.SitePortalCode); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *SitePortalRepository) UpdateProvider(provider *models.SitePortalProvider) error {
	if provider == nil {
		return fmt.Errorf("Site Portal Provider is required")
	}
	provider.SitePortalCode = strings.TrimSpace(provider.SitePortalCode)
	tx, err := r.db.BeginTx()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := lockSitePortal(tx, provider.SitePortalCode); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE site_portal_providers SET display_name=$3,enabled=$4,sort_order=$5,file_base_url=$6,sign_secret_ref=$7,portal_api_base_url=$8,upload_enabled=$9,updated_at=CURRENT_TIMESTAMP
		WHERE site_portal_code=$1 AND provider_id=$2`, provider.SitePortalCode, provider.ProviderID, provider.DisplayName,
		provider.Enabled, provider.SortOrder, provider.FileBaseURL, provider.SignSecretRef, provider.PortalAPIBaseURL, provider.UploadEnabled)
	if err != nil {
		return fmt.Errorf("update Site Portal Provider: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrSitePortalProviderNotFound
	}
	if err := bumpSitePortalProviderRevision(tx, provider.SitePortalCode); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (r *SitePortalRepository) SetProviderEnabled(portalCode, providerID string, enabled bool) error {
	return r.mutateProvider(portalCode, providerID, func(tx *Tx) error {
		result, err := tx.Exec(`UPDATE site_portal_providers SET enabled=$3,updated_at=CURRENT_TIMESTAMP WHERE site_portal_code=$1 AND provider_id=$2`, portalCode, providerID, enabled)
		if err != nil {
			return fmt.Errorf("update Site Portal Provider state: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrSitePortalProviderNotFound
		}
		return nil
	})
}

func (r *SitePortalRepository) DeleteProvider(portalCode, providerID string) error {
	return r.mutateProvider(portalCode, providerID, func(tx *Tx) error {
		result, err := tx.Exec(`DELETE FROM site_portal_providers WHERE site_portal_code=$1 AND provider_id=$2`, portalCode, providerID)
		if err != nil {
			return fmt.Errorf("delete Site Portal Provider: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrSitePortalProviderNotFound
		}
		return nil
	})
}

func (r *SitePortalRepository) mutateProvider(portalCode, providerID string, mutate func(*Tx) error) error {
	portalCode, providerID = strings.TrimSpace(portalCode), strings.TrimSpace(providerID)
	tx, err := r.db.BeginTx()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := lockSitePortal(tx, portalCode); err != nil {
		return err
	}
	if err := mutate(tx); err != nil {
		return err
	}
	if err := bumpSitePortalProviderRevision(tx, portalCode); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
