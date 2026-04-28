CREATE TABLE IF NOT EXISTS context_catalog (
    id BIGSERIAL PRIMARY KEY,
    context_type TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    keywords TEXT[] NOT NULL DEFAULT '{}',
    aliases TEXT[] NOT NULL DEFAULT '{}',
    owner TEXT NOT NULL DEFAULT '',
    expected_task_categories TEXT[] NOT NULL DEFAULT '{}',
    expected_models TEXT[] NOT NULL DEFAULT '{}',
    expected_usage_level TEXT NOT NULL DEFAULT '',
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (context_type, name)
);

CREATE INDEX IF NOT EXISTS idx_context_catalog_active_type
    ON context_catalog(active, context_type);

CREATE INDEX IF NOT EXISTS idx_context_catalog_keywords
    ON context_catalog USING GIN(keywords);

CREATE INDEX IF NOT EXISTS idx_context_catalog_aliases
    ON context_catalog USING GIN(aliases);

INSERT INTO anomaly_rules (rule_key, threshold_json, severity, rule_window)
VALUES
    ('low_work_relevance_high_cost', '{"total_tokens": 20000, "personal_use_score": 0.6}'::jsonb, 'high', 'per_trace')
ON CONFLICT (rule_key) DO NOTHING;
