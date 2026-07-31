ALTER TABLE users
    ADD COLUMN IF NOT EXISTS print_quota_balance INTEGER NOT NULL DEFAULT 0;

DO $$ BEGIN
    ALTER TABLE users ADD CONSTRAINT users_print_quota_balance_nonnegative
        CHECK (print_quota_balance >= 0);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE edge_terminal_sessions
    ADD COLUMN IF NOT EXISTS site_portal_code VARCHAR(64) REFERENCES site_portals(code);

ALTER TABLE edge_terminal_sessions
    ADD COLUMN IF NOT EXISTS cloud_user_id UUID REFERENCES users(id);

ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS edge_node_id VARCHAR(100) REFERENCES edge_nodes(id);
ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS site_portal_code VARCHAR(64) REFERENCES site_portals(code);
ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS terminal_session_id VARCHAR(128);
ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS confirmation_id VARCHAR(128);
ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS authorization_request_hash CHAR(64);
ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS local_file_id VARCHAR(128);
ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS quota_reserved INTEGER NOT NULL DEFAULT 0;
ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS quota_consumed INTEGER;
ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS impressions_completed INTEGER;
ALTER TABLE print_jobs
    ADD COLUMN IF NOT EXISTS sheets_completed INTEGER;

CREATE TABLE IF NOT EXISTS print_quota_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    print_job_id UUID REFERENCES print_jobs(id) ON DELETE SET NULL,
    transaction_type VARCHAR(40) NOT NULL,
    delta INTEGER NOT NULL,
    balance_after INTEGER NOT NULL CHECK (balance_after >= 0),
    admin_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    reason VARCHAR(500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_print_quota_initial_grant
    ON print_quota_transactions(user_id)
    WHERE transaction_type='initial_grant';

CREATE UNIQUE INDEX IF NOT EXISTS idx_print_jobs_confirmation
    ON print_jobs(edge_node_id,confirmation_id)
    WHERE confirmation_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_print_quota_transactions_user_created
    ON print_quota_transactions(user_id,created_at DESC);

WITH granted AS (
    UPDATE users user_account
    SET print_quota_balance=50
    WHERE user_account.print_quota_balance=0
        AND EXISTS (
            SELECT 1 FROM external_identities identity
            WHERE identity.cloud_user_id=user_account.id
        )
        AND NOT EXISTS (
            SELECT 1 FROM print_quota_transactions transaction_record
            WHERE transaction_record.user_id=user_account.id
                AND transaction_record.transaction_type='initial_grant'
        )
    RETURNING user_account.id,user_account.print_quota_balance
)
INSERT INTO print_quota_transactions
    (user_id,transaction_type,delta,balance_after)
SELECT id,'initial_grant',50,print_quota_balance FROM granted;
