ALTER TABLE site_portals
  ADD COLUMN IF NOT EXISTS provider_config_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS site_portal_providers (
  site_portal_code VARCHAR(64) NOT NULL REFERENCES site_portals(code) ON DELETE CASCADE,
  provider_id VARCHAR(64) NOT NULL,
  display_name VARCHAR(120) NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  sort_order INTEGER NOT NULL DEFAULT 0,
  file_base_url VARCHAR(1000) NOT NULL,
  sign_secret_ref VARCHAR(32) NOT NULL,
  portal_api_base_url VARCHAR(1000),
  upload_enabled BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (site_portal_code, provider_id)
);

CREATE INDEX IF NOT EXISTS idx_site_portal_providers_order
  ON site_portal_providers(site_portal_code, sort_order, provider_id);
