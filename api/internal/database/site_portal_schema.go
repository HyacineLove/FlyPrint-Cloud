package database

import "fmt"

// initSitePortalSchema keeps direct InitTables callers compatible with the
// forward migration used by normal server startup.
func (db *DB) initSitePortalSchema() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS site_portals (
			code VARCHAR(64) PRIMARY KEY,
			display_name VARCHAR(120) NOT NULL,
			entry_url VARCHAR(1000) NOT NULL,
			claim_base_url VARCHAR(1000) NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`ALTER TABLE edge_nodes ADD COLUMN IF NOT EXISTS default_site_portal_code VARCHAR(64)`,
		`ALTER TABLE edge_nodes ALTER COLUMN default_site_portal_code SET DEFAULT 'official'`,
		`UPDATE edge_nodes SET default_site_portal_code='official' WHERE default_site_portal_code IS NULL`,
		`CREATE TABLE IF NOT EXISTS external_identities (
			site_portal_code VARCHAR(64) NOT NULL REFERENCES site_portals(code),
			external_user_id VARCHAR(255) NOT NULL,
			cloud_user_id UUID NOT NULL REFERENCES users(id),
			display_name VARCHAR(120) NOT NULL,
			last_login_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (site_portal_code, external_user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_external_identities_cloud_user ON external_identities(cloud_user_id)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("initialize site portal schema: %w", err)
		}
	}
	return nil
}
