CREATE TABLE IF NOT EXISTS portal_session_ready_outbox (
    id UUID PRIMARY KEY,
    node_id VARCHAR(128) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at TIMESTAMPTZ,
    CONSTRAINT portal_session_ready_outbox_status CHECK (status IN ('pending','delivered'))
);

CREATE INDEX IF NOT EXISTS idx_portal_session_ready_outbox_due
    ON portal_session_ready_outbox(status, next_attempt_at);
