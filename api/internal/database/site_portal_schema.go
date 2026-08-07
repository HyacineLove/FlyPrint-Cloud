package database

import "fmt"

// initSitePortalSchema keeps direct InitTables callers compatible with the
// forward migrations while ensuring the current tables are present.
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
		`CREATE TABLE IF NOT EXISTS edge_site_portals (
			edge_node_id VARCHAR(100) NOT NULL REFERENCES edge_nodes(id) ON DELETE CASCADE,
			site_portal_code VARCHAR(64) NOT NULL REFERENCES site_portals(code),
			is_default BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (edge_node_id, site_portal_code)
		)`,
		`ALTER TABLE edge_site_portals ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT false`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_edge_site_portals_default
			ON edge_site_portals(edge_node_id) WHERE is_default=true`,
		`CREATE INDEX IF NOT EXISTS idx_edge_site_portals_portal ON edge_site_portals(site_portal_code)`,
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
