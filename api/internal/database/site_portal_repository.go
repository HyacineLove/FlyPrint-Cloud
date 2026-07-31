package database

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"fly-print-cloud/api/internal/models"
)

var ErrSitePortalUnauthorized = errors.New("site portal unauthorized")

type SitePortalRepository struct {
	db *DB
}

func NewSitePortalRepository(db *DB) *SitePortalRepository {
	return &SitePortalRepository{db: db}
}

func hashSitePortalToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func scanSitePortal(scanner interface{ Scan(...any) error }) (*models.SitePortal, error) {
	portal := &models.SitePortal{}
	if err := scanner.Scan(
		&portal.Code,
		&portal.DisplayName,
		&portal.EntryURL,
		&portal.ClaimBaseURL,
		&portal.APITokenHash,
		&portal.Enabled,
		&portal.CreatedAt,
		&portal.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return portal, nil
}

func (r *SitePortalRepository) GetByCode(code string) (*models.SitePortal, error) {
	portal, err := scanSitePortal(r.db.QueryRow(`SELECT code,display_name,entry_url,claim_base_url,api_token_hash,enabled,created_at,updated_at
		FROM site_portals WHERE code=$1 AND enabled=true`, code))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("site portal not found")
		}
		return nil, fmt.Errorf("get site portal: %w", err)
	}
	return portal, nil
}

func (r *SitePortalRepository) GetDefaultForNode(nodeID string) (*models.SitePortal, error) {
	portal, err := scanSitePortal(r.db.QueryRow(`SELECT portal.code,portal.display_name,portal.entry_url,portal.claim_base_url,
		portal.api_token_hash,portal.enabled,portal.created_at,portal.updated_at
		FROM edge_nodes node
		JOIN site_portals portal ON portal.code=node.default_site_portal_code
		WHERE node.id=$1 AND node.deleted_at IS NULL AND node.enabled=true AND portal.enabled=true`, nodeID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("default site portal not found")
		}
		return nil, fmt.Errorf("get default site portal: %w", err)
	}
	return portal, nil
}

func (r *SitePortalRepository) Authenticate(code, rawToken string) (*models.SitePortal, error) {
	if code == "" || rawToken == "" {
		return nil, ErrSitePortalUnauthorized
	}
	portal, err := r.GetByCode(code)
	if err != nil {
		return nil, ErrSitePortalUnauthorized
	}
	actual := hashSitePortalToken(rawToken)
	if subtle.ConstantTimeCompare([]byte(actual), []byte(portal.APITokenHash)) != 1 {
		return nil, ErrSitePortalUnauthorized
	}
	return portal, nil
}

// UpsertBootstrap installs one explicitly configured portal without exposing
// management endpoints before Slice 4.
func (r *SitePortalRepository) UpsertBootstrap(portal *models.SitePortal, rawToken string) error {
	if portal == nil || portal.Code == "" || rawToken == "" {
		return fmt.Errorf("site portal bootstrap configuration is incomplete")
	}
	_, err := r.db.Exec(`INSERT INTO site_portals
		(code,display_name,entry_url,claim_base_url,api_token_hash,enabled)
		VALUES ($1,$2,$3,$4,$5,true)
		ON CONFLICT(code) DO UPDATE SET display_name=EXCLUDED.display_name,
			entry_url=EXCLUDED.entry_url,claim_base_url=EXCLUDED.claim_base_url,
			api_token_hash=EXCLUDED.api_token_hash,enabled=true,updated_at=CURRENT_TIMESTAMP`,
		portal.Code, portal.DisplayName, portal.EntryURL, portal.ClaimBaseURL, hashSitePortalToken(rawToken))
	if err != nil {
		return fmt.Errorf("upsert bootstrap site portal: %w", err)
	}
	return nil
}

func (r *SitePortalRepository) SetDefaultForNode(nodeID, portalCode string) error {
	result, err := r.db.Exec(`UPDATE edge_nodes SET default_site_portal_code=$2,updated_at=CURRENT_TIMESTAMP
		WHERE id=$1 AND deleted_at IS NULL`, nodeID, portalCode)
	if err != nil {
		return fmt.Errorf("set default site portal: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("edge node not found")
	}
	return nil
}
