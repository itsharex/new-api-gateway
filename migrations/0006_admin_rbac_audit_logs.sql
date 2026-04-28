CREATE TABLE IF NOT EXISTS audit_users (
    id BIGSERIAL PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    auth_provider TEXT NOT NULL DEFAULT 'local',
    external_subject TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (role IN ('viewer', 'auditor', 'raw_access', 'admin')),
    CHECK (status IN ('active', 'disabled'))
);

CREATE INDEX IF NOT EXISTS idx_audit_users_status_role
    ON audit_users(status, role);

CREATE TABLE IF NOT EXISTS audit_sessions (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES audit_users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_sessions_user_expires
    ON audit_sessions(user_id, expires_at DESC);

CREATE TABLE IF NOT EXISTS audit_action_logs (
    id BIGSERIAL PRIMARY KEY,
    actor_user_id BIGINT NOT NULL DEFAULT 0,
    actor_username TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    token_fingerprint TEXT NOT NULL DEFAULT '',
    fingerprint_display TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    ip_hash TEXT NOT NULL DEFAULT '',
    user_agent_hash TEXT NOT NULL DEFAULT '',
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_action_logs_actor_created
    ON audit_action_logs(actor_user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_action_logs_action_created
    ON audit_action_logs(action, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_action_logs_trace
    ON audit_action_logs(trace_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_action_logs_token
    ON audit_action_logs(token_fingerprint, created_at DESC);

CREATE TABLE IF NOT EXISTS review_decisions (
    id BIGSERIAL PRIMARY KEY,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL,
    decision TEXT NOT NULL,
    reviewer_id BIGINT NOT NULL,
    reviewer_username TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (target_type IN ('trace', 'anomaly', 'coverage_alert')),
    CHECK (decision IN ('acknowledge', 'dismiss', 'confirm', 'mark_personal_use', 'mark_abuse', 'needs_normalizer', 'mark_fixed', 'ignore_for_now'))
);

CREATE INDEX IF NOT EXISTS idx_review_decisions_target_created
    ON review_decisions(target_type, target_id, created_at DESC);
