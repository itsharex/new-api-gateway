CREATE TABLE IF NOT EXISTS usage_anomalies (
    id BIGSERIAL PRIMARY KEY,
    anomaly_id TEXT NOT NULL UNIQUE,
    anomaly_type TEXT NOT NULL,
    severity TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    token_fingerprint TEXT NOT NULL DEFAULT '',
    fingerprint_display TEXT NOT NULL DEFAULT '',
    new_api_token_id INTEGER NOT NULL DEFAULT 0,
    employee_no TEXT NOT NULL DEFAULT '',
    token_name_snapshot TEXT NOT NULL DEFAULT '',
    window_start TIMESTAMPTZ,
    window_end TIMESTAMPTZ,
    observed_value NUMERIC NOT NULL DEFAULT 0,
    threshold_value NUMERIC NOT NULL DEFAULT 0,
    baseline_value NUMERIC,
    model TEXT NOT NULL DEFAULT '',
    route_pattern TEXT NOT NULL DEFAULT '',
    sample_trace_ids TEXT[] NOT NULL DEFAULT '{}',
    reason TEXT NOT NULL DEFAULT '',
    detector_version TEXT NOT NULL,
    reviewer_id INTEGER,
    review_note TEXT NOT NULL DEFAULT '',
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_usage_anomalies_status_created
    ON usage_anomalies(status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_usage_anomalies_employee_created
    ON usage_anomalies(employee_no, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_usage_anomalies_token_created
    ON usage_anomalies(token_fingerprint, created_at DESC);

CREATE TABLE IF NOT EXISTS anomaly_rules (
    id BIGSERIAL PRIMARY KEY,
    rule_key TEXT NOT NULL UNIQUE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    scope_type TEXT NOT NULL DEFAULT 'global',
    scope_value TEXT NOT NULL DEFAULT '',
    rule_window TEXT NOT NULL DEFAULT '',
    threshold_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    severity TEXT NOT NULL DEFAULT 'medium',
    cooldown TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO anomaly_rules (rule_key, threshold_json, severity, rule_window)
VALUES
    ('identity_unresolved_success', '{"enabled": true}'::jsonb, 'high', 'per_trace'),
    ('invalid_employee_no', '{"enabled": true}'::jsonb, 'high', 'per_trace'),
    ('high_trace_tokens', '{"total_tokens": 20000}'::jsonb, 'medium', 'per_trace'),
    ('raw_only_large_response', '{"response_body_bytes": 1048576}'::jsonb, 'medium', 'per_trace'),
    ('retry_storm_trace', '{"status_code_min": 500}'::jsonb, 'medium', 'per_trace')
ON CONFLICT (rule_key) DO NOTHING;

ALTER TABLE coverage_alerts
    ADD COLUMN IF NOT EXISTS payload_shape_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS normalizer TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS normalizer_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS affected_trace_count BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS affected_token_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS affected_employee_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS owner_note TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_coverage_alerts_status_last_seen
    ON coverage_alerts(status, last_seen_at DESC);
