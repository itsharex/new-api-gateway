CREATE TABLE IF NOT EXISTS normalized_messages (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    direction TEXT NOT NULL,
    sequence_index INTEGER NOT NULL,
    role TEXT NOT NULL DEFAULT '',
    modality TEXT NOT NULL DEFAULT 'text',
    content_text TEXT NOT NULL DEFAULT '',
    content_text_hash TEXT NOT NULL DEFAULT '',
    media_object_id BIGINT,
    media_url TEXT NOT NULL DEFAULT '',
    source_path TEXT NOT NULL DEFAULT '',
    protocol_item_type TEXT NOT NULL DEFAULT '',
    token_count_estimate INTEGER NOT NULL DEFAULT 0,
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_normalized_messages_trace_sequence
    ON normalized_messages(trace_id, direction, sequence_index, source_path);

CREATE INDEX IF NOT EXISTS idx_normalized_messages_trace
    ON normalized_messages(trace_id);

CREATE TABLE IF NOT EXISTS analysis_results (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    analyzer_name TEXT NOT NULL,
    analyzer_version TEXT NOT NULL,
    policy_version TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL,
    label TEXT NOT NULL,
    score NUMERIC NOT NULL DEFAULT 0,
    confidence NUMERIC NOT NULL DEFAULT 0,
    severity TEXT NOT NULL DEFAULT '',
    evidence_message_ids BIGINT[] NOT NULL DEFAULT '{}',
    evidence_spans_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    result_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_analysis_results_trace
    ON analysis_results(trace_id);

CREATE INDEX IF NOT EXISTS idx_analysis_results_category_label
    ON analysis_results(category, label);

CREATE TABLE IF NOT EXISTS usage_aggregates (
    id BIGSERIAL PRIMARY KEY,
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_size TEXT NOT NULL,
    token_fingerprint TEXT NOT NULL DEFAULT '',
    new_api_token_id INTEGER NOT NULL DEFAULT 0,
    employee_no TEXT NOT NULL DEFAULT '',
    token_name_snapshot TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    route_pattern TEXT NOT NULL DEFAULT '',
    protocol_family TEXT NOT NULL DEFAULT '',
    request_count BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    error_count BIGINT NOT NULL DEFAULT 0,
    stream_count BIGINT NOT NULL DEFAULT 0,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    cached_tokens BIGINT NOT NULL DEFAULT 0,
    estimated_cost TEXT NOT NULL DEFAULT '',
    request_body_bytes BIGINT NOT NULL DEFAULT 0,
    response_body_bytes BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (
        bucket_start, bucket_size, token_fingerprint, employee_no,
        model, route_pattern, protocol_family
    )
);

CREATE INDEX IF NOT EXISTS idx_usage_aggregates_employee_bucket
    ON usage_aggregates(employee_no, bucket_size, bucket_start);

CREATE INDEX IF NOT EXISTS idx_usage_aggregates_token_bucket
    ON usage_aggregates(token_fingerprint, bucket_size, bucket_start);
