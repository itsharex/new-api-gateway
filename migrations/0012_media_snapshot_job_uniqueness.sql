CREATE UNIQUE INDEX IF NOT EXISTS idx_media_snapshot_jobs_unique_source
    ON media_snapshot_jobs(trace_id, source_url, source_context, policy_reason);
