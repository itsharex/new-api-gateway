-- Rename employee_no columns to username across all tables and indexes.

-- traces table
ALTER TABLE traces RENAME COLUMN employee_no_snapshot TO username_snapshot;

-- token_identity_cache table
ALTER TABLE token_identity_cache RENAME COLUMN employee_no TO username;

-- Indexes
DROP INDEX IF EXISTS idx_traces_employee_created;
CREATE INDEX idx_traces_username_created ON traces(username_snapshot, created_at);

DROP INDEX IF EXISTS idx_token_identity_employee;
CREATE INDEX idx_token_identity_username ON token_identity_cache(username);
