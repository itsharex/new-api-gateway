CREATE TABLE IF NOT EXISTS traces (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL UNIQUE,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    route_pattern TEXT NOT NULL,
    protocol_family TEXT NOT NULL,
    capture_mode TEXT NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    upstream_status_code INTEGER NOT NULL DEFAULT 0,
    stream BOOLEAN NOT NULL DEFAULT FALSE,
    request_started_at TIMESTAMPTZ NOT NULL,
    response_finished_at TIMESTAMPTZ,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    request_body_size BIGINT NOT NULL DEFAULT 0,
    response_body_size BIGINT NOT NULL DEFAULT 0,
    request_body_sha256 TEXT NOT NULL DEFAULT '',
    response_body_sha256 TEXT NOT NULL DEFAULT '',
    request_raw_ref TEXT NOT NULL DEFAULT '',
    response_raw_ref TEXT NOT NULL DEFAULT '',
    token_fingerprint TEXT NOT NULL DEFAULT '',
    fingerprint_display TEXT NOT NULL DEFAULT '',
    new_api_token_id_snapshot INTEGER NOT NULL DEFAULT 0,
    token_name_snapshot TEXT NOT NULL DEFAULT '',
    employee_no_snapshot TEXT NOT NULL DEFAULT '',
    identity_resolution_status TEXT NOT NULL DEFAULT '',
    identity_cache_status TEXT NOT NULL DEFAULT '',
    model_requested TEXT NOT NULL DEFAULT '',
    analysis_status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_traces_created_at ON traces(created_at);
CREATE INDEX IF NOT EXISTS idx_traces_employee_created ON traces(employee_no_snapshot, created_at);
CREATE INDEX IF NOT EXISTS idx_traces_token_created ON traces(token_fingerprint, created_at);
CREATE INDEX IF NOT EXISTS idx_traces_route_created ON traces(route_pattern, created_at);

CREATE TABLE IF NOT EXISTS raw_evidence_objects (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    object_type TEXT NOT NULL,
    object_ref TEXT NOT NULL,
    storage_backend TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    sha256 TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_raw_evidence_trace ON raw_evidence_objects(trace_id);

CREATE TABLE IF NOT EXISTS token_identity_cache (
    token_fingerprint TEXT PRIMARY KEY,
    fingerprint_display TEXT NOT NULL,
    new_api_token_id INTEGER NOT NULL DEFAULT 0,
    token_name_raw TEXT NOT NULL DEFAULT '',
    employee_no TEXT NOT NULL DEFAULT '',
    token_status INTEGER NOT NULL DEFAULT 0,
    token_group TEXT NOT NULL DEFAULT '',
    token_expired_time BIGINT NOT NULL DEFAULT 0,
    token_accessed_time BIGINT NOT NULL DEFAULT 0,
    remain_quota INTEGER NOT NULL DEFAULT 0,
    used_quota INTEGER NOT NULL DEFAULT 0,
    unlimited_quota BOOLEAN NOT NULL DEFAULT FALSE,
    model_limits_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    model_limits TEXT NOT NULL DEFAULT '',
    resolved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    refreshed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolution_error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_token_identity_employee ON token_identity_cache(employee_no);

CREATE TABLE IF NOT EXISTS coverage_alerts (
    id BIGSERIAL PRIMARY KEY,
    alert_id TEXT NOT NULL UNIQUE,
    alert_code TEXT NOT NULL,
    severity TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    method TEXT NOT NULL DEFAULT '',
    route_pattern TEXT NOT NULL DEFAULT '',
    raw_path TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',
    protocol_family TEXT NOT NULL DEFAULT '',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    occurrence_count BIGINT NOT NULL DEFAULT 1,
    sample_trace_ids TEXT[] NOT NULL DEFAULT '{}',
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
