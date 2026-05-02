-- Rename employee_no columns to username across all tables and indexes.

-- traces table
ALTER TABLE traces RENAME COLUMN employee_no_snapshot TO username_snapshot;

-- token_identity_cache table
ALTER TABLE token_identity_cache RENAME COLUMN employee_no TO username;

-- usage_aggregates table
ALTER TABLE usage_aggregates RENAME COLUMN employee_no TO username;

-- usage_anomalies table
ALTER TABLE usage_anomalies RENAME COLUMN employee_no TO username;

-- audit_subjects table
ALTER TABLE audit_subjects RENAME COLUMN employee_no TO username;

-- Indexes
DROP INDEX IF EXISTS idx_traces_employee_created;
CREATE INDEX idx_traces_username_created ON traces(username_snapshot, created_at);

DROP INDEX IF EXISTS idx_token_identity_employee;
CREATE INDEX idx_token_identity_username ON token_identity_cache(username);

DROP INDEX IF EXISTS idx_usage_aggregates_employee_bucket;
CREATE INDEX idx_usage_aggregates_username_bucket ON usage_aggregates(username, bucket_size, bucket_start);

DROP INDEX IF EXISTS idx_usage_anomalies_employee_created;
CREATE INDEX idx_usage_anomalies_username_created ON usage_anomalies(username, created_at DESC);
