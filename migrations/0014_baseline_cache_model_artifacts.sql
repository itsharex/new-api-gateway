-- 0014: baseline_cache, model_artifacts, pgvector extension, context_catalog embedding column

CREATE TABLE IF NOT EXISTS baseline_cache (
    id              serial PRIMARY KEY,
    fingerprint_key varchar(64) NOT NULL,
    metric_type     varchar(64) NOT NULL,
    metric_value    double precision NOT NULL,
    metadata_json   jsonb DEFAULT '{}',
    computed_at     timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    UNIQUE (fingerprint_key, metric_type)
);

CREATE INDEX IF NOT EXISTS idx_baseline_cache_lookup
    ON baseline_cache (fingerprint_key, metric_type);

CREATE TABLE IF NOT EXISTS model_artifacts (
    id              serial PRIMARY KEY,
    model_name      varchar(64) NOT NULL,
    version         varchar(64) NOT NULL,
    artifact        bytea NOT NULL,
    feature_columns text[] NOT NULL,
    training_stats  jsonb DEFAULT '{}',
    trained_at      timestamptz NOT NULL DEFAULT now(),
    is_active       boolean DEFAULT true,
    UNIQUE (model_name, version)
);

DO $$ BEGIN
    CREATE EXTENSION IF NOT EXISTS vector;
    ALTER TABLE context_catalog ADD COLUMN IF NOT EXISTS embedding vector(1024);
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'pgvector extension not available, skipping embedding column';
END $$;
