CREATE TABLE IF NOT EXISTS worker_heartbeats (
    worker_id TEXT PRIMARY KEY,
    worker_kind TEXT NOT NULL,
    status TEXT NOT NULL,
    queue_name TEXT NOT NULL DEFAULT '',
    processed_count BIGINT NOT NULL DEFAULT 0,
    error_count BIGINT NOT NULL DEFAULT 0,
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (worker_kind IN ('analysis')),
    CHECK (status IN ('starting', 'idle', 'processed', 'error', 'stopping'))
);

CREATE INDEX IF NOT EXISTS idx_worker_heartbeats_kind_seen
    ON worker_heartbeats(worker_kind, last_seen_at DESC);

CREATE INDEX IF NOT EXISTS idx_worker_heartbeats_status_seen
    ON worker_heartbeats(status, last_seen_at DESC);
