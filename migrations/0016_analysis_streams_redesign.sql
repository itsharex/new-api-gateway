ALTER TABLE traces
    ADD COLUMN core_status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN enrichment_required BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN enrichment_status TEXT NOT NULL DEFAULT 'not_required',
    ADD COLUMN core_queued_at TIMESTAMPTZ,
    ADD COLUMN core_started_at TIMESTAMPTZ,
    ADD COLUMN core_completed_at TIMESTAMPTZ,
    ADD COLUMN enrichment_queued_at TIMESTAMPTZ,
    ADD COLUMN enrichment_started_at TIMESTAMPTZ,
    ADD COLUMN enrichment_completed_at TIMESTAMPTZ,
    ADD COLUMN last_analysis_error_code TEXT NOT NULL DEFAULT '';

ALTER TABLE analysis_results
    ADD COLUMN stage TEXT NOT NULL DEFAULT 'core',
    ADD COLUMN producer TEXT NOT NULL DEFAULT '',
    ADD COLUMN result_key TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX idx_analysis_results_trace_stage_producer_result_key
    ON analysis_results (trace_id, stage, producer, result_key);

ALTER TABLE raw_evidence_objects
    ADD COLUMN variant TEXT NOT NULL DEFAULT 'original',
    ADD COLUMN derived_from_object_ref TEXT;

CREATE TABLE analysis_tasks (
    trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    stage TEXT NOT NULL,
    status TEXT NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    stream_name TEXT NOT NULL DEFAULT '',
    stream_message_id TEXT NOT NULL DEFAULT '',
    queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '',
    last_error_message TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (trace_id, stage),
    CHECK (stage IN ('core', 'enrichment')),
    CHECK (status IN ('queued', 'leased', 'succeeded', 'failed_retryable', 'failed_terminal'))
);

CREATE TABLE trace_usage_facts (
    trace_id TEXT PRIMARY KEY REFERENCES traces(trace_id) ON DELETE CASCADE,
    token_fingerprint TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    route_pattern TEXT NOT NULL DEFAULT '',
    protocol_family TEXT NOT NULL DEFAULT '',
    request_started_at TIMESTAMPTZ NOT NULL,
    request_count BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    error_count BIGINT NOT NULL DEFAULT 0,
    stream_count BIGINT NOT NULL DEFAULT 0,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    cached_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    request_body_bytes BIGINT NOT NULL DEFAULT 0,
    response_body_bytes BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE analysis_runtime_samples (
    id BIGSERIAL PRIMARY KEY,
    sampled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    stage TEXT NOT NULL,
    queue_depth BIGINT NOT NULL DEFAULT 0,
    pending_count BIGINT NOT NULL DEFAULT 0,
    leased_count BIGINT NOT NULL DEFAULT 0,
    oldest_pending_age_seconds BIGINT NOT NULL DEFAULT 0,
    throughput_per_minute BIGINT NOT NULL DEFAULT 0,
    queue_wait_p50_ms BIGINT NOT NULL DEFAULT 0,
    queue_wait_p95_ms BIGINT NOT NULL DEFAULT 0,
    processing_p50_ms BIGINT NOT NULL DEFAULT 0,
    processing_p95_ms BIGINT NOT NULL DEFAULT 0,
    retryable_fail_count BIGINT NOT NULL DEFAULT 0,
    terminal_fail_count BIGINT NOT NULL DEFAULT 0,
    active_consumers BIGINT NOT NULL DEFAULT 0,
    CHECK (stage IN ('core', 'enrichment'))
);
