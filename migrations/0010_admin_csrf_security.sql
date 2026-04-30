ALTER TABLE audit_sessions
    ADD COLUMN IF NOT EXISTS csrf_token TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_audit_sessions_csrf
    ON audit_sessions(csrf_token)
    WHERE revoked_at IS NULL;
