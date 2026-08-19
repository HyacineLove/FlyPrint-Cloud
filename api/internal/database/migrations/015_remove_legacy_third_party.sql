-- Destructive cleanup for databases created before Site Portal became the
-- only external integration boundary. Fresh databases never create these
-- legacy tables, so all statements are safe when they are absent.
CREATE TEMP TABLE IF NOT EXISTS flyprint_legacy_third_party_jobs (id UUID PRIMARY KEY) ON COMMIT DROP;
CREATE TEMP TABLE IF NOT EXISTS flyprint_legacy_third_party_files (id UUID PRIMARY KEY) ON COMMIT DROP;

DO $$
BEGIN
  IF to_regclass('public.integration_print_requests') IS NOT NULL THEN
    INSERT INTO flyprint_legacy_third_party_jobs(id)
      SELECT DISTINCT print_job_id FROM integration_print_requests WHERE print_job_id IS NOT NULL
      ON CONFLICT DO NOTHING;
    INSERT INTO flyprint_legacy_third_party_files(id)
      SELECT DISTINCT file_id FROM integration_print_requests WHERE file_id IS NOT NULL
      ON CONFLICT DO NOTHING;
  END IF;
END $$;

-- The legacy request table owns foreign keys to files and print_jobs. Capture
-- the referenced IDs first, then remove the dependent tables before deleting
-- their target rows so upgrades of populated databases remain transactional.
DROP TABLE IF EXISTS integration_callback_events CASCADE;
DROP TABLE IF EXISTS integration_print_requests CASCADE;
DROP TABLE IF EXISTS integration_providers CASCADE;

DELETE FROM print_quota_transactions
 WHERE print_job_id IN (SELECT id FROM flyprint_legacy_third_party_jobs);
DELETE FROM edge_job_update_receipts
 WHERE job_id IN (SELECT id FROM flyprint_legacy_third_party_jobs);
DELETE FROM print_jobs
 WHERE id IN (SELECT id FROM flyprint_legacy_third_party_jobs);
DELETE FROM files
 WHERE id IN (SELECT id FROM flyprint_legacy_third_party_files)
   AND NOT EXISTS (SELECT 1 FROM print_jobs WHERE print_jobs.local_file_id = files.id::text);

ALTER TABLE edge_terminal_sessions DROP COLUMN IF EXISTS integration_request_id;
ALTER TABLE site_portals DROP COLUMN IF EXISTS api_token_hash;
ALTER TABLE oauth2_clients ADD COLUMN IF NOT EXISTS site_portal_code VARCHAR(64);
ALTER TABLE oauth2_clients DROP CONSTRAINT IF EXISTS oauth2_clients_client_type_check;
ALTER TABLE oauth2_clients DROP CONSTRAINT IF EXISTS oauth2_clients_client_type_fkey;
DELETE FROM oauth2_clients WHERE client_type = 'third_party';
ALTER TABLE oauth2_clients ADD CONSTRAINT oauth2_clients_client_type_check
  CHECK (client_type IN ('edge_node','site_portal'));

CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth2_clients_site_portal_code
  ON oauth2_clients(site_portal_code) WHERE site_portal_code IS NOT NULL;
