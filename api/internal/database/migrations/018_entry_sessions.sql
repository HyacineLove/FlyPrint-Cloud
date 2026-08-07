-- Entry sessions are the authoritative parent for every capability issued by
-- the terminal QR flow.  Individual raw credentials never enter the database.
ALTER TABLE edge_terminal_sessions
  ADD COLUMN IF NOT EXISTS qr_generation BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS entry_sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  t1_hash CHAR(64) UNIQUE NOT NULL,
  acquire_hash CHAR(64),
  t2_hash CHAR(64),
  node_id VARCHAR(100) NOT NULL REFERENCES edge_nodes(id) ON DELETE RESTRICT,
  printer_id UUID NOT NULL REFERENCES printers(id) ON DELETE RESTRICT,
  terminal_session_id VARCHAR(128) NOT NULL,
  qr_generation BIGINT NOT NULL,
  status VARCHAR(20) NOT NULL CHECK (status IN ('qr_issued','mask_pending','entry_active','claim_pending','redeemed','expired','invalidated')),
  mask_command_id UUID,
  mask_confirmed_at TIMESTAMP,
  portal_attempt_version INTEGER NOT NULL DEFAULT 0,
  issued_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP NOT NULL,
  invalidated_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_entry_sessions_node_active
  ON entry_sessions(node_id, status, expires_at);

CREATE TABLE IF NOT EXISTS entry_portal_attempts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  entry_session_id UUID NOT NULL REFERENCES entry_sessions(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  site_portal_code VARCHAR(64) NOT NULL REFERENCES site_portals(code) ON DELETE RESTRICT,
  t3_hash CHAR(64) UNIQUE NOT NULL,
  status VARCHAR(16) NOT NULL CHECK (status IN ('issued','opened','superseded','completed','expired')),
  issued_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP NOT NULL,
  UNIQUE(entry_session_id, version)
);
CREATE INDEX IF NOT EXISTS idx_entry_portal_attempts_session ON entry_portal_attempts(entry_session_id, status);

CREATE TABLE IF NOT EXISTS entry_claims (
  entry_session_id UUID PRIMARY KEY REFERENCES entry_sessions(id) ON DELETE CASCADE,
  claim_hash CHAR(64) UNIQUE NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  redeemed_at TIMESTAMP
);
