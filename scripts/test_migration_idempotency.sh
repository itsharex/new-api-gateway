#!/usr/bin/env bash
set -euo pipefail

# Test that migration 0013 is idempotent — running it twice produces the same result.
# Requires a running Postgres with the schema loaded.

DSN="${POSTGRES_DSN:-postgres://localhost:5432/audit_gateway}"

# Insert test data without prefix
psql "$DSN" -c "
INSERT INTO raw_evidence_objects (trace_id, object_type, object_ref, storage_backend, content_type, size_bytes)
VALUES ('test-idempotency-1', 'request_body', 'raw/2026/01/01/trace_1/request_body.bin', 'filesystem', 'application/json', 0)
ON CONFLICT DO NOTHING;
"

# Run migration once
psql "$DSN" -f migrations/0013_object_ref_scheme_prefix.sql

# Verify prefix added
count=$(psql "$DSN" -t -c "
SELECT COUNT(*) FROM raw_evidence_objects
WHERE trace_id = 'test-idempotency-1' AND object_ref = 'file:///raw/2026/01/01/trace_1/request_body.bin';
")
echo "After first run: $count rows with prefix (expect 1)"

# Run migration again (should be no-op)
psql "$DSN" -f migrations/0013_object_ref_scheme_prefix.sql

# Verify no double-prefix
double=$(psql "$DSN" -t -c "
SELECT COUNT(*) FROM raw_evidence_objects
WHERE object_ref LIKE 'file:///file:///%';
")
echo "After second run: $double double-prefixed rows (expect 0)"

# Cleanup
psql "$DSN" -c "DELETE FROM raw_evidence_objects WHERE trace_id = 'test-idempotency-1';"
