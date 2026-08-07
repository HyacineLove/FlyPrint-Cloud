-- Replace the legacy per-node login_source/default_site_portal_code fields
-- with an explicit Edge-to-Site-Portal assignment set.

ALTER TABLE edge_nodes
    ADD COLUMN IF NOT EXISTS login_source VARCHAR(100);

ALTER TABLE edge_nodes
    ADD COLUMN IF NOT EXISTS default_site_portal_code VARCHAR(64);

CREATE TABLE IF NOT EXISTS edge_site_portals (
    edge_node_id VARCHAR(100) NOT NULL REFERENCES edge_nodes(id) ON DELETE CASCADE,
    site_portal_code VARCHAR(64) NOT NULL REFERENCES site_portals(code),
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (edge_node_id, site_portal_code)
);

ALTER TABLE edge_site_portals
    ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT false;

-- The runtime field wins when it names an enabled Portal. The legacy admin
-- field is used only when the runtime field is absent or invalid. Existing
-- rows with neither value fall back to the enabled official Portal.
UPDATE edge_site_portals SET is_default=false;

WITH choices AS (
    SELECT node.id AS edge_node_id,
        COALESCE(
            (SELECT portal.code FROM site_portals portal
             WHERE portal.code=NULLIF(node.default_site_portal_code,'') AND portal.enabled=true),
            (SELECT portal.code FROM site_portals portal
             WHERE portal.code=NULLIF(node.login_source,'') AND portal.enabled=true),
            (SELECT portal.code FROM site_portals portal
             WHERE portal.code='official' AND portal.enabled=true)
        ) AS site_portal_code
    FROM edge_nodes node
    WHERE node.deleted_at IS NULL
), valid_choices AS (
    SELECT edge_node_id, site_portal_code
    FROM choices
    WHERE site_portal_code IS NOT NULL
)
INSERT INTO edge_site_portals(edge_node_id, site_portal_code, is_default)
SELECT edge_node_id, site_portal_code, true
FROM valid_choices
ON CONFLICT (edge_node_id, site_portal_code)
DO UPDATE SET is_default=EXCLUDED.is_default;

CREATE INDEX IF NOT EXISTS idx_edge_site_portals_portal
    ON edge_site_portals(site_portal_code);

CREATE UNIQUE INDEX IF NOT EXISTS idx_edge_site_portals_default
    ON edge_site_portals(edge_node_id)
    WHERE is_default=true;

ALTER TABLE edge_nodes DROP COLUMN IF EXISTS login_source;
ALTER TABLE edge_nodes DROP COLUMN IF EXISTS default_site_portal_code;
