-- Separate operator accounts from SSO-created printing users and give every
-- external identity a stable connector/issuer/subject key.  The legacy
-- Site Portal columns remain during the compatibility phase.
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS account_kind VARCHAR(20) NOT NULL DEFAULT 'operator';

UPDATE users user_account
SET account_kind = 'external'
WHERE EXISTS (
  SELECT 1 FROM external_identities identity
  WHERE identity.cloud_user_id = user_account.id
);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'users_account_kind_check'
  ) THEN
    ALTER TABLE users
      ADD CONSTRAINT users_account_kind_check
      CHECK (account_kind IN ('operator', 'external'));
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'users_external_role_check'
  ) THEN
    ALTER TABLE users
      ADD CONSTRAINT users_external_role_check
      CHECK (account_kind <> 'external' OR role = 'viewer');
  END IF;
END $$;

ALTER TABLE external_identities
  ADD COLUMN IF NOT EXISTS identity_connector_id VARCHAR(128),
  ADD COLUMN IF NOT EXISTS issuer VARCHAR(512),
  ADD COLUMN IF NOT EXISTS subject VARCHAR(255);

UPDATE external_identities
SET identity_connector_id = 'site-portal:' || site_portal_code,
    issuer = 'urn:flyprint:site-portal:' || site_portal_code,
    subject = external_user_id
WHERE identity_connector_id IS NULL OR issuer IS NULL OR subject IS NULL;

ALTER TABLE external_identities
  ALTER COLUMN identity_connector_id SET NOT NULL,
  ALTER COLUMN issuer SET NOT NULL,
  ALTER COLUMN subject SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_external_identities_principal
  ON external_identities(identity_connector_id, issuer, subject);
