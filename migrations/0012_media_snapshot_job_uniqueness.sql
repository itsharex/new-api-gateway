WITH ranked_duplicates AS (
    SELECT
        id,
        ROW_NUMBER() OVER (
            PARTITION BY trace_id, source_url, source_context, policy_reason
            ORDER BY
                CASE WHEN status = 'downloaded' THEN 0 ELSE 1 END,
                CASE WHEN object_id IS NOT NULL THEN 0 ELSE 1 END,
                updated_at DESC,
                created_at ASC,
                id ASC
        ) AS duplicate_rank
    FROM media_snapshot_jobs
)
DELETE FROM media_snapshot_jobs
WHERE id IN (
    SELECT id
    FROM ranked_duplicates
    WHERE duplicate_rank > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_media_snapshot_jobs_unique_source
    ON media_snapshot_jobs(trace_id, source_url, source_context, policy_reason);
