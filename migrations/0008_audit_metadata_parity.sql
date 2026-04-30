ALTER TABLE traces
    ADD COLUMN IF NOT EXISTS parent_trace_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS request_id_from_client TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS new_api_request_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS route_support_level TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS body_kind TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS response_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS client_ip_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS user_agent_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS audit_subject_display_name_snapshot TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS department_snapshot TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS identity_resolved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS model_upstream TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_message_redacted TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS idx_traces_route_support_created
    ON traces(route_support_level, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_traces_identity_status_created
    ON traces(identity_resolution_status, created_at DESC);

ALTER TABLE raw_evidence_objects
    ADD COLUMN IF NOT EXISTS content_encoding TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS original_filename TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS redaction_status TEXT NOT NULL DEFAULT 'not_redacted',
    ADD COLUMN IF NOT EXISTS encryption_status TEXT NOT NULL DEFAULT 'filesystem_permissions';

ALTER TABLE token_identity_cache
    ADD COLUMN IF NOT EXISTS audit_subject_display_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS department TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'unknown';

CREATE TABLE IF NOT EXISTS audit_subjects (
    employee_no TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    department TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    source TEXT NOT NULL DEFAULT 'manual',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('active', 'inactive'))
);

CREATE INDEX IF NOT EXISTS idx_audit_subjects_department
    ON audit_subjects(department);
