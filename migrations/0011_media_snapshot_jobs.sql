CREATE TABLE IF NOT EXISTS media_snapshot_jobs (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    source_url TEXT NOT NULL,
    source_context TEXT NOT NULL DEFAULT '',
    policy_reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    object_id BIGINT REFERENCES raw_evidence_objects(id),
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('queued', 'downloaded', 'skipped', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_media_snapshot_jobs_status_created
    ON media_snapshot_jobs(status, created_at);

CREATE INDEX IF NOT EXISTS idx_media_snapshot_jobs_trace
    ON media_snapshot_jobs(trace_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_media_snapshot_jobs_unique_source
    ON media_snapshot_jobs(trace_id, source_url, source_context, policy_reason);
