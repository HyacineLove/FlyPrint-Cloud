ALTER TABLE oauth2_clients ADD COLUMN IF NOT EXISTS edge_node_id VARCHAR(100) REFERENCES edge_nodes(id) ON DELETE RESTRICT;
ALTER TABLE oauth2_clients ADD COLUMN IF NOT EXISTS site_portal_code VARCHAR(64);
CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth2_clients_edge_node_id ON oauth2_clients(edge_node_id) WHERE edge_node_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth2_clients_site_portal_code ON oauth2_clients(site_portal_code) WHERE site_portal_code IS NOT NULL;

CREATE TABLE IF NOT EXISTS terminal_tickets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  ticket_hash CHAR(64) UNIQUE NOT NULL,
  node_id VARCHAR(100) NOT NULL REFERENCES edge_nodes(id) ON DELETE RESTRICT,
  printer_id UUID NOT NULL REFERENCES printers(id) ON DELETE RESTRICT,
  terminal_session_id VARCHAR(128) NOT NULL,
  selected_entry VARCHAR(64),
  status VARCHAR(16) NOT NULL CHECK (status IN ('issued','selected','consumed','expired','cancelled')),
  issued_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  selected_at TIMESTAMP,
  consumed_at TIMESTAMP,
  expires_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_terminal_tickets_expires_at ON terminal_tickets(expires_at);

CREATE TABLE IF NOT EXISTS terminal_upload_sessions (
  upload_token_hash CHAR(64) PRIMARY KEY,
  terminal_ticket_hash CHAR(64) NOT NULL REFERENCES terminal_tickets(ticket_hash),
  node_id VARCHAR(100) NOT NULL,
  printer_id UUID NOT NULL,
  terminal_session_id VARCHAR(128) NOT NULL,
  file_id UUID REFERENCES files(id),
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS edge_terminal_sessions (
  node_id VARCHAR(100) PRIMARY KEY REFERENCES edge_nodes(id) ON DELETE CASCADE,
  terminal_session_id VARCHAR(128),
  terminal_ticket_hash CHAR(64),
  entry_type VARCHAR(64),
  site_portal_code VARCHAR(64),
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
