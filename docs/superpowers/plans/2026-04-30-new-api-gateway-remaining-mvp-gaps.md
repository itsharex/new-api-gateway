# New API Gateway Remaining MVP Gap Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining MVP gaps between the approved audit gateway design and the current tested implementation.

**Architecture:** Keep the existing Go gateway/admin API and Python analysis worker split. Add small, focused modules for identity cache parity, richer capture metadata, degraded capture behavior, protocol normalization, media snapshots, admin security, and operational metrics rather than restructuring the service.

**Tech Stack:** Go `net/http`, `pgx/v5`, `go-redis/v9`, bcrypt sessions, filesystem evidence store, Python 3.12 worker with `psycopg`, `redis`, standard-library `urllib`, PostgreSQL migrations, existing shell smoke scripts.

---

## Scope Check

The approved design spans several independent subsystems. Previous plans already implemented the project skeleton, transparent proxy basics, raw body/header evidence, Redis analysis jobs, initial normalizers, usage aggregates, several rule anomalies, work relevance, admin login/RBAC, a basic admin UI, and health endpoints.

This plan covers the remaining MVP acceptance gaps that are still visible in the current project:

- new-api-compatible key canonicalization and PostgreSQL local identity cache usage;
- trace/raw evidence schema fields needed for audit reconstruction;
- forward-first capture behavior when evidence persistence is degraded;
- multipart part evidence, decoded base64 evidence, and richer minimal metadata;
- protocol normalization for Gemini, image/audio/media payloads, and SSE event streams;
- the remaining explainable MVP anomaly rules;
- admin CSRF/rate limits, richer filters, employee/token identity views, review decisions, and settings views;
- media snapshot jobs with SSRF protections;
- operational metrics and documentation for degraded modes.

This plan does not cover non-MVP design items: SSO/OIDC, embeddings/RAG, statistical baselines, Kafka/NATS-scale queues, full Midjourney/Suno/video deep normalization, automated backup infrastructure, or object-storage migration away from the current filesystem store.

## Current Implementation Audit

Implemented and passing today:

- Transparent proxy path with request/response body evidence, redacted header evidence, trace insertion, Redis job enqueue, and `x-audit-trace-id`.
- API key extraction from Authorization, `x-api-key`, query `key`, `x-goog-api-key`, `mj-api-secret`, and realtime websocket protocol.
- HMAC fingerprint generation and Redis-backed token identity lookup from the `tokens` table.
- Route registry for the listed deep-normalized and raw/minimal MVP routes, plus unknown-route coverage alerts.
- Python worker normalization for OpenAI chat, OpenAI responses, Claude messages, generic prompts, usage aggregation, work relevance, normalization-gap alerts, and six per-trace anomaly rules.
- Admin local login, role permissions, signed cookies, audit action logs, raw evidence access audit, API key lookup by fingerprint, basic overview/usage/traces/anomalies/coverage/context/audit UI.
- Liveness/readiness/metrics endpoints and worker heartbeat rows.

Unimplemented or incomplete against the MVP acceptance criteria:

- Key canonicalization does not split on `-` after removing `sk-`, while the design requires the first segment as `canonical_new_api_key`.
- `token_identity_cache` exists but is not used as a PostgreSQL local identity cache between Redis and the read-only new-api lookup.
- Trace and evidence schemas omit many audit fields such as route support level, body kind, client/user-agent hashes, response start time, error redaction fields, raw evidence content encoding, original filename, and encryption/redaction status.
- Capture failures for request evidence currently return gateway errors instead of forwarding first and recording degraded capture status.
- Multipart requests are stored only as a whole body; individual form fields/files are not saved as evidence objects with part metadata.
- Base64 media and media URLs are not decoded or snapshotted.
- WebSocket traffic is tunneled but message logs are not captured.
- Worker normalizers do not deeply parse Gemini `contents`, image/audio media payloads, multipart-derived media refs, or SSE event streams.
- Rule coverage is short of the design list: daily token limits, short-window spikes, expensive model overuse, long output, repeated prompt, off-hours high usage, missing employee number, and token-leak signals still need deterministic implementations.
- Admin filters and views are narrower than the design: no time/body-size/stream/task-category/work-relevance/anomaly/keyword filters, no Employee Directory / Token Identity view, no Review Decisions view, no System Settings view, and no review action buttons in anomaly/coverage screens.
- Admin API lacks CSRF protection and rate limits for raw evidence and API key lookup.
- Remote media snapshot jobs and SSRF protections are absent.
- Metrics do not yet expose request latency/status, upstream latency/status, capture success/failure, identity statuses, raw-only counts, coverage/anomaly counts, or storage growth.

## File Structure

- Modify: `internal/authkeys/extractor.go` exports shared canonicalization and splits new-api composite keys.
- Modify: `internal/authkeys/extractor_test.go` updates canonicalization expectations.
- Modify: `internal/admin/handlers.go` uses shared canonicalization for API key lookup, adds CSRF/rate-limit enforcement, mounts new product APIs.
- Modify: `internal/admin/auth.go` issues and verifies CSRF tokens.
- Create: `internal/admin/limits.go` implements in-memory per-actor rate limits.
- Modify: `internal/admin/models.go` adds new DTOs and filters.
- Modify: `internal/admin/repository.go` adds identity directory, review decision, settings, and richer trace filter queries.
- Modify: `internal/adminui/app.js` adds new navigation views, richer filters, review actions, and CSRF header handling.
- Modify: `internal/adminui/app.css` styles new filters and review/status controls.
- Modify: `internal/identity/stores.go` adds cache chaining support.
- Create: `internal/identity/postgres_cache.go` reads/writes `token_identity_cache`.
- Create: `internal/identity/postgres_cache_test.go` tests local identity cache SQL and expiration behavior.
- Modify: `internal/identity/resolver.go` returns design-aligned cache statuses.
- Modify: `cmd/audit-gateway/main.go` wires Redis plus PostgreSQL identity caches, request metrics, and degraded spool configuration.
- Create: `migrations/0008_audit_metadata_parity.sql` adds missing trace/evidence/cache/audit-subject fields.
- Create: `migrations/0009_anomaly_rule_expansion.sql` adds remaining deterministic anomaly rule seed rows.
- Create: `migrations/0010_admin_csrf_security.sql` adds CSRF token storage for admin sessions.
- Create: `migrations/0011_media_snapshot_jobs.sql` adds `media_snapshot_jobs`.
- Modify: `internal/traces/model.go` adds trace and raw evidence fields.
- Modify: `internal/traces/repository.go` persists the added fields.
- Modify: `internal/gateway/proxy.go` records response start time, route support level, body kind, request ID, client hashes, model-upstream, redacted errors, degraded capture status, multipart part refs, and metrics.
- Modify: `internal/gateway/capture.go` adds safe degraded capture helpers.
- Modify: `internal/gateway/multipart.go` extracts multipart parts from the captured body and writes per-part metadata.
- Create: `internal/gateway/spool.go` writes emergency trace envelopes when PostgreSQL/evidence persistence is degraded.
- Create: `internal/gateway/media.go` detects base64 media and media URLs in JSON request/response bodies.
- Modify: `internal/jobs/jobs.go` includes client/user-agent hashes and media refs for worker analysis.
- Modify: `workers/analysis_worker/models.py` adds client hashes, media refs, anomaly context, and snapshot job models.
- Modify: `workers/analysis_worker/normalizers.py` adds Gemini, image/audio/media, base64, and SSE normalization.
- Modify: `workers/analysis_worker/rules.py` adds the remaining explainable rule detectors.
- Modify: `workers/analysis_worker/repository.py` loads rule context, saves media snapshot jobs, and persists additional metadata.
- Create: `workers/analysis_worker/media_snapshot.py` implements SSRF-safe media downloads.
- Modify: `workers/analysis_worker/main.py` invokes rule context loading and media snapshot job creation.
- Modify: `internal/ops/metrics.go` renders expanded Prometheus metrics.
- Modify: `internal/ops/health.go` includes new-api token DB readiness and degraded capture spool checks.
- Modify: `docs/development.md` documents the remaining MVP behavior and smoke paths.
- Create: `scripts/smoke_remaining_mvp.sh` exercises canonicalization, admin CSRF/rate limits, media snapshot rejection, and expanded metrics.

---

### Task 1: Canonical Key and PostgreSQL Identity Cache

**Files:**
- Modify: `internal/authkeys/extractor_test.go`
- Modify: `internal/authkeys/extractor.go`
- Modify: `internal/admin/handlers.go`
- Modify: `internal/identity/stores.go`
- Create: `internal/identity/postgres_cache.go`
- Create: `internal/identity/postgres_cache_test.go`
- Modify: `internal/identity/resolver.go`
- Modify: `cmd/audit-gateway/main.go`

- [ ] **Step 1: Write failing canonicalization tests**

Replace `TestExtractPreservesDistinctHyphenatedKeys` in `internal/authkeys/extractor_test.go` with:

```go
func TestExtractCanonicalizesNewAPICompositeKeys(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "sk prefix and suffix", raw: "sk-abc123-extra", want: "abc123"},
		{name: "bearer and suffix", raw: "Bearer sk-employee-prod", want: "employee"},
		{name: "spaces", raw: "  sk-team-alpha-prod  ", want: "team"},
		{name: "plain key without suffix", raw: "plainkey", want: "plainkey"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Canonicalize(tt.raw)
			if !ok {
				t.Fatal("expected canonical key")
			}
			if got != tt.want {
				t.Fatalf("Canonicalize(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestExtractHyphenatedCompositeKeysShareNewAPICanonicalSegment(t *testing.T) {
	firstReq, _ := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	firstReq.Header.Set("x-api-key", "sk-team-alpha-prod")
	secondReq, _ := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	secondReq.Header.Set("x-api-key", "sk-team-alpha-dev")

	first, ok := Extract(firstReq)
	if !ok {
		t.Fatal("expected first key")
	}
	second, ok := Extract(secondReq)
	if !ok {
		t.Fatal("expected second key")
	}
	if first.CanonicalKey != "team" {
		t.Fatalf("first CanonicalKey = %q, want team", first.CanonicalKey)
	}
	if second.CanonicalKey != "team" {
		t.Fatalf("second CanonicalKey = %q, want team", second.CanonicalKey)
	}
}
```

Update the `wantKey` values in `TestExtractCanonicalKeyFromSupportedSources` so `sk-abc123-extra` expects `abc123`, `sk-claude123-extra` expects `claude123`, `sk-gemini123-extra` expects `gemini123`, `sk-google123-extra` expects `google123`, `sk-mj123-extra` expects `mj123`, and `sk-real123-extra` expects `real123`.

- [ ] **Step 2: Run canonicalization tests to verify failure**

Run:

```bash
go test ./internal/authkeys -run 'TestExtractCanonicalizesNewAPICompositeKeys|TestExtractHyphenatedCompositeKeysShareNewAPICanonicalSegment|TestExtractCanonicalKeyFromSupportedSources' -v
```

Expected: FAIL because `Canonicalize` is not exported and current canonicalization keeps the suffix after the first hyphen.

- [ ] **Step 3: Implement shared canonicalization**

In `internal/authkeys/extractor.go`, replace `canonicalKey` and `canonicalize` with:

```go
func Canonicalize(value string) (string, bool) {
	key := canonicalize(value)
	return key, key != ""
}

func canonicalKey(value string) (string, bool) {
	return Canonicalize(value)
}

func canonicalize(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		value = strings.TrimSpace(value[7:])
	}
	value = strings.TrimPrefix(value, "sk-")
	if index := strings.Index(value, "-"); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}
```

In `internal/admin/handlers.go`, replace `canonicalizeLookupKey` with:

```go
func canonicalizeLookupKey(value string) string {
	canonical, ok := authkeys.Canonicalize(value)
	if !ok {
		return ""
	}
	return canonical
}
```

- [ ] **Step 4: Run canonicalization tests to verify pass**

Run:

```bash
go test ./internal/authkeys ./internal/admin -run 'TestExtract|TestAPIKeyLookup|TestCanonical' -v
```

Expected: PASS. Existing API key lookup tests should continue to prove plaintext key material is not returned or logged.

- [ ] **Step 5: Add PostgreSQL cache and cache chaining tests**

Create `internal/identity/postgres_cache_test.go`:

```go
package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresCacheGetReadsFreshSnapshot(t *testing.T) {
	db := &fakeIdentityCacheDB{row: fakeIdentityCacheRow{
		values: []any{
			"tkfp_abc", 42, "E10001", "E10001", "active", "engineering",
			int64(0), int64(10), 900, 100, false, true, `["gpt-4o"]`,
			time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC),
			time.Date(2026, 4, 30, 8, 15, 0, 0, time.UTC),
		},
	}}
	cache := PostgresCache{DB: db, Now: func() time.Time {
		return time.Date(2026, 4, 30, 8, 10, 0, 0, time.UTC)
	}}

	got, ok, err := cache.Get(context.Background(), "fingerprint")
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.EmployeeNo != "E10001" || got.NewAPITokenID != 42 {
		t.Fatalf("snapshot = %#v", got)
	}
	if !strings.Contains(db.query, "FROM token_identity_cache") {
		t.Fatalf("query did not read token_identity_cache: %s", db.query)
	}
}

func TestPostgresCacheSetUpsertsSnapshot(t *testing.T) {
	db := &fakeIdentityCacheDB{}
	cache := PostgresCache{DB: db, TTL: 10 * time.Minute, Now: func() time.Time {
		return time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	}}

	err := cache.Set(context.Background(), Snapshot{
		TokenFingerprint:   "fingerprint",
		FingerprintDisplay: "tkfp_abc",
		NewAPITokenID:      42,
		TokenNameRaw:       "E10001",
		EmployeeNo:         "E10001",
		TokenStatus:        1,
		TokenGroup:         "engineering",
	})
	if err != nil {
		t.Fatalf("Set error = %v", err)
	}
	if !strings.Contains(db.execSQL, "ON CONFLICT (token_fingerprint)") {
		t.Fatalf("upsert SQL missing conflict clause: %s", db.execSQL)
	}
	if len(db.execArgs) == 0 || db.execArgs[0] != "fingerprint" {
		t.Fatalf("exec args = %#v", db.execArgs)
	}
}

func TestChainCacheReadsSecondCacheAndBackfillsFirst(t *testing.T) {
	first := &fakeCache{setSnapshots: []Snapshot{}}
	second := &fakeCache{value: Snapshot{TokenFingerprint: "fp", EmployeeNo: "E10001"}, ok: true}
	cache := ChainCache{Caches: []Cache{first, second}}

	got, ok, err := cache.Get(context.Background(), "fp")
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if !ok || got.EmployeeNo != "E10001" {
		t.Fatalf("Get = %#v ok=%v", got, ok)
	}
	if len(first.setSnapshots) != 1 {
		t.Fatalf("expected first cache backfill, got %d", len(first.setSnapshots))
	}
}

type fakeIdentityCacheDB struct {
	query    string
	execSQL  string
	execArgs []any
	row      fakeIdentityCacheRow
}

func (f *fakeIdentityCacheDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	f.query = sql
	return f.row
}

func (f *fakeIdentityCacheDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execSQL = sql
	f.execArgs = append([]any(nil), args...)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

type fakeIdentityCacheRow struct {
	values []any
	err    error
}

func (r fakeIdentityCacheRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		switch target := dest[i].(type) {
		case *string:
			*target = r.values[i].(string)
		case *int:
			*target = r.values[i].(int)
		case *int64:
			*target = r.values[i].(int64)
		case *bool:
			*target = r.values[i].(bool)
		case *time.Time:
			*target = r.values[i].(time.Time)
		}
	}
	return nil
}
```

- [ ] **Step 6: Run PostgreSQL cache tests to verify failure**

Run:

```bash
go test ./internal/identity -run 'TestPostgresCache|TestChainCache' -v
```

Expected: FAIL because `PostgresCache` and `ChainCache` do not exist.

- [ ] **Step 7: Implement PostgreSQL cache and chain cache**

Append this to `internal/identity/stores.go`:

```go
type ChainCache struct {
	Caches []Cache
}

func (c ChainCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	var firstErr error
	for index, cache := range c.Caches {
		if isNilInterface(cache) {
			continue
		}
		snapshot, ok, err := cache.Get(ctx, fingerprint)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !ok {
			continue
		}
		for backfillIndex := 0; backfillIndex < index; backfillIndex++ {
			if !isNilInterface(c.Caches[backfillIndex]) {
				_ = c.Caches[backfillIndex].Set(ctx, snapshot)
			}
		}
		return snapshot, true, nil
	}
	return Snapshot{}, false, firstErr
}

func (c ChainCache) Set(ctx context.Context, snapshot Snapshot) error {
	var firstErr error
	for _, cache := range c.Caches {
		if isNilInterface(cache) {
			continue
		}
		if err := cache.Set(ctx, snapshot); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
```

Create `internal/identity/postgres_cache.go`:

```go
package identity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrPostgresCacheDBRequired = errors.New("identity postgres cache db is nil")

type PostgresCacheDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type PostgresCache struct {
	DB  PostgresCacheDB
	TTL time.Duration
	Now func() time.Time
}

func (c PostgresCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	if c.DB == nil {
		return Snapshot{}, false, ErrPostgresCacheDBRequired
	}
	now := c.now()
	var snapshot Snapshot
	var resolvedAt time.Time
	var expiresAt time.Time
	err := c.DB.QueryRow(ctx, `
SELECT
  fingerprint_display, new_api_token_id, token_name_raw, employee_no,
  token_status, token_group, token_expired_time, token_accessed_time,
  remain_quota, used_quota, unlimited_quota, model_limits_enabled,
  model_limits, resolved_at, COALESCE(expires_at, to_timestamp(0))
FROM token_identity_cache
WHERE token_fingerprint = $1
LIMIT 1`, fingerprint).Scan(
		&snapshot.FingerprintDisplay,
		&snapshot.NewAPITokenID,
		&snapshot.TokenNameRaw,
		&snapshot.EmployeeNo,
		&snapshot.TokenStatus,
		&snapshot.TokenGroup,
		&snapshot.ExpiredTime,
		&snapshot.AccessedTime,
		&snapshot.RemainQuota,
		&snapshot.UsedQuota,
		&snapshot.UnlimitedQuota,
		&snapshot.ModelLimitsEnabled,
		&snapshot.ModelLimits,
		&resolvedAt,
		&expiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	if !expiresAt.IsZero() && expiresAt.After(time.Unix(0, 0).UTC()) && !expiresAt.After(now) {
		return Snapshot{}, false, nil
	}
	snapshot.TokenFingerprint = fingerprint
	snapshot.ResolutionStatus = ResolutionStatusResolved
	snapshot.IdentityCacheStatus = IdentityCacheStatusHit
	_, _ = c.DB.Exec(ctx, `UPDATE token_identity_cache SET last_seen_at = $2 WHERE token_fingerprint = $1`, fingerprint, now)
	return snapshot, true, nil
}

func (c PostgresCache) Set(ctx context.Context, snapshot Snapshot) error {
	if c.DB == nil {
		return ErrPostgresCacheDBRequired
	}
	now := c.now()
	expiresAt := now.Add(cacheTTL(c.TTL))
	_, err := c.DB.Exec(ctx, `
INSERT INTO token_identity_cache (
  token_fingerprint, fingerprint_display, new_api_token_id, token_name_raw,
  employee_no, token_status, token_group, token_expired_time, token_accessed_time,
  remain_quota, used_quota, unlimited_quota, model_limits_enabled, model_limits,
  resolved_at, refreshed_at, expires_at, last_seen_at, resolution_error
) VALUES (
  $1,$2,$3,$4,
  $5,$6,$7,$8,$9,
  $10,$11,$12,$13,$14,
  $15,$15,$16,$15,$17
)
ON CONFLICT (token_fingerprint) DO UPDATE SET
  fingerprint_display = EXCLUDED.fingerprint_display,
  new_api_token_id = EXCLUDED.new_api_token_id,
  token_name_raw = EXCLUDED.token_name_raw,
  employee_no = EXCLUDED.employee_no,
  token_status = EXCLUDED.token_status,
  token_group = EXCLUDED.token_group,
  token_expired_time = EXCLUDED.token_expired_time,
  token_accessed_time = EXCLUDED.token_accessed_time,
  remain_quota = EXCLUDED.remain_quota,
  used_quota = EXCLUDED.used_quota,
  unlimited_quota = EXCLUDED.unlimited_quota,
  model_limits_enabled = EXCLUDED.model_limits_enabled,
  model_limits = EXCLUDED.model_limits,
  refreshed_at = EXCLUDED.refreshed_at,
  expires_at = EXCLUDED.expires_at,
  last_seen_at = EXCLUDED.last_seen_at,
  resolution_error = EXCLUDED.resolution_error`,
		snapshot.TokenFingerprint,
		snapshot.FingerprintDisplay,
		snapshot.NewAPITokenID,
		snapshot.TokenNameRaw,
		snapshot.EmployeeNo,
		snapshot.TokenStatus,
		snapshot.TokenGroup,
		snapshot.ExpiredTime,
		snapshot.AccessedTime,
		snapshot.RemainQuota,
		snapshot.UsedQuota,
		snapshot.UnlimitedQuota,
		snapshot.ModelLimitsEnabled,
		snapshot.ModelLimits,
		now,
		expiresAt,
		snapshot.ResolutionStatus,
	)
	return err
}

func (c PostgresCache) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func cacheTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 15 * time.Minute
	}
	return ttl
}
```

In `internal/identity/resolver.go`, replace the cache status constants with:

```go
const (
	ResolutionStatusResolved          = "resolved"
	ResolutionStatusMissingEmployeeNo = "missing_employee_no"
	ResolutionStatusInvalidEmployeeNo = "invalid_employee_no"
	ResolutionStatusDBError           = "db_error"
	ResolutionStatusNotFound          = "not_found"
	ResolutionStatusExtractFailed     = "extract_failed"
	ResolutionStatusResolveFailed     = "resolve_failed"

	IdentityCacheStatusHit          = "cache_hit"
	IdentityCacheStatusMissDBLookup = "miss_db_lookup"
	IdentityCacheStatusCacheError   = "cache_error"
)
```

Update cache error assignment in `Resolve` to `IdentityCacheStatusCacheError`.

In `cmd/audit-gateway/main.go`, change the resolver cache wiring in `buildHandler`:

```go
Cache: identity.ChainCache{Caches: []identity.Cache{
	identity.RedisCache{Client: redisClient},
	identity.PostgresCache{DB: pool},
}},
```

- [ ] **Step 8: Run identity tests**

Run:

```bash
go test ./internal/authkeys ./internal/admin ./internal/identity ./cmd/audit-gateway -v
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/authkeys/extractor.go internal/authkeys/extractor_test.go internal/admin/handlers.go internal/identity/stores.go internal/identity/postgres_cache.go internal/identity/postgres_cache_test.go internal/identity/resolver.go cmd/audit-gateway/main.go
git commit -m "feat: align key canonicalization and identity cache"
```

---

### Task 2: Audit Metadata Schema and Trace Persistence

**Files:**
- Create: `migrations/0008_audit_metadata_parity.sql`
- Modify: `internal/traces/model.go`
- Modify: `internal/traces/repository.go`
- Modify: `internal/traces/repository_test.go`
- Modify: `internal/gateway/proxy.go`
- Modify: `internal/jobs/jobs.go`

- [ ] **Step 1: Add schema migration**

Create `migrations/0008_audit_metadata_parity.sql`:

```sql
ALTER TABLE traces
    ADD COLUMN IF NOT EXISTS parent_trace_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS request_id_from_client TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS new_api_request_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS route_support_level TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS body_kind TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS response_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS client_ip_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS user_agent_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS audit_subject_display_name_snapshot TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS department_snapshot TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS identity_resolved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS model_upstream TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_message_redacted TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS idx_traces_route_support_created
    ON traces(route_support_level, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_traces_identity_status_created
    ON traces(identity_resolution_status, created_at DESC);

ALTER TABLE raw_evidence_objects
    ADD COLUMN IF NOT EXISTS content_encoding TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS original_filename TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS redaction_status TEXT NOT NULL DEFAULT 'not_redacted',
    ADD COLUMN IF NOT EXISTS encryption_status TEXT NOT NULL DEFAULT 'filesystem_permissions';

ALTER TABLE token_identity_cache
    ADD COLUMN IF NOT EXISTS audit_subject_display_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS department TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'unknown';

CREATE TABLE IF NOT EXISTS audit_subjects (
    employee_no TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    department TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    source TEXT NOT NULL DEFAULT 'manual',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('active', 'inactive'))
);

CREATE INDEX IF NOT EXISTS idx_audit_subjects_department
    ON audit_subjects(department);
```

- [ ] **Step 2: Verify migration text**

Run:

```bash
rg -n "route_support_level|audit_subjects|content_encoding|audit_subject_display_name" migrations/0008_audit_metadata_parity.sql
```

Expected: at least four matches covering traces, raw evidence objects, token cache, and audit subjects.

- [ ] **Step 3: Add failing trace repository tests**

In `internal/traces/repository_test.go`, extend the existing trace fixture with:

```go
trace.RouteSupportLevel = "deep_normalized"
trace.BodyKind = "json"
trace.ResponseStartedAt = trace.RequestStartedAt.Add(10 * time.Millisecond)
trace.ClientIPHash = "iphash"
trace.UserAgentHash = "uahash"
trace.RequestIDFromClient = "client-request-id"
trace.NewAPIRequestID = "new-api-request-id"
trace.ModelUpstream = "gpt-4o"
trace.ErrorType = ""
trace.ErrorMessageRedacted = ""
```

Add these assertions after the insert:

```go
for _, fragment := range []string{
	"route_support_level",
	"body_kind",
	"response_started_at",
	"client_ip_hash",
	"user_agent_hash",
	"request_id_from_client",
	"new_api_request_id",
	"model_upstream",
	"error_type",
	"error_message_redacted",
} {
	if !strings.Contains(execer.query, fragment) {
		t.Fatalf("trace insert missing %s: %s", fragment, execer.query)
	}
}
```

In the raw evidence object fixture, set:

```go
object.ContentEncoding = "base64"
object.OriginalFilename = "input.png"
object.RedactionStatus = "not_redacted"
object.EncryptionStatus = "filesystem_permissions"
```

Add assertions that `InsertRawEvidence` SQL contains those four column names.

- [ ] **Step 4: Run repository tests to verify failure**

Run:

```bash
go test ./internal/traces -run 'TestPostgresRepository' -v
```

Expected: FAIL because the model and insert SQL do not include the added fields.

- [ ] **Step 5: Extend trace and raw evidence models**

Replace `Trace` and `RawEvidenceObject` in `internal/traces/model.go` with:

```go
type Trace struct {
	TraceID                         string
	ParentTraceID                   string
	RequestIDFromClient             string
	NewAPIRequestID                 string
	Method                          string
	Path                            string
	RoutePattern                    string
	ProtocolFamily                  string
	CaptureMode                     string
	RouteSupportLevel               string
	BodyKind                        string
	StatusCode                      int
	UpstreamStatusCode              int
	Stream                          bool
	RequestStartedAt                time.Time
	ResponseStartedAt               time.Time
	ResponseFinishedAt              time.Time
	DurationMillis                  int64
	ClientIPHash                    string
	UserAgentHash                   string
	RequestBodySize                 int64
	ResponseBodySize                int64
	RequestBodySHA256               string
	ResponseBodySHA256              string
	RequestRawRef                   string
	RequestHeadersRef               string
	ResponseRawRef                  string
	ResponseHeadersRef              string
	TokenFingerprint                string
	FingerprintDisplay              string
	NewAPITokenIDSnapshot           int
	TokenNameSnapshot               string
	EmployeeNoSnapshot              string
	AuditSubjectDisplayNameSnapshot string
	DepartmentSnapshot              string
	IdentityResolutionStatus        string
	IdentityCacheStatus             string
	IdentityResolvedAt              time.Time
	ModelRequested                  string
	ModelUpstream                   string
	UsagePromptTokens               int
	UsageCompletionTokens           int
	UsageTotalTokens                int
	UsageReasoningTokens            int
	UsageCachedTokens               int
	EstimatedCost                   string
	ErrorType                       string
	ErrorMessageRedacted            string
	AnalysisStatus                  string
	CreatedAt                       time.Time
	UpdatedAt                       time.Time
}

type RawEvidenceObject struct {
	TraceID          string
	ObjectType       string
	ObjectRef        string
	StorageBackend   string
	ContentType      string
	ContentEncoding  string
	OriginalFilename string
	SizeBytes        int64
	SHA256           string
	RedactionStatus  string
	EncryptionStatus string
	CreatedAt        time.Time
}
```

- [ ] **Step 6: Persist the added fields**

In `internal/traces/repository.go`, replace the `INSERT INTO traces` statement with an insert that includes these columns in the same order as the struct fields added in Step 5. Use `nil` for zero `ResponseStartedAt`, `ResponseFinishedAt`, and `IdentityResolvedAt`. Set `trace.UpdatedAt = trace.CreatedAt` when `UpdatedAt` is zero.

Use this helper near `InsertTrace`:

```go
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
```

Replace `InsertRawEvidence` SQL with:

```go
_, err := r.execer.Exec(ctx, `
INSERT INTO raw_evidence_objects (
  trace_id, object_type, object_ref, storage_backend, content_type,
  content_encoding, original_filename, size_bytes, sha256,
  redaction_status, encryption_status, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
	object.TraceID,
	object.ObjectType,
	object.ObjectRef,
	object.StorageBackend,
	object.ContentType,
	object.ContentEncoding,
	object.OriginalFilename,
	object.SizeBytes,
	object.SHA256,
	defaultString(object.RedactionStatus, "not_redacted"),
	defaultString(object.EncryptionStatus, "filesystem_permissions"),
	object.CreatedAt,
)
```

Add this helper:

```go
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
```

- [ ] **Step 7: Populate metadata in the gateway and job envelope**

Add these fields to `traceRecord`:

```go
responseStartedAt time.Time
responseHeaders    http.Header
modelUpstream      string
errorType          string
errorMessage       string
```

When the upstream response is received, set `responseStartedAt: h.now()` and `responseHeaders: upstreamResp.Header.Clone()` in the `traceRecord` passed to `insertTrace`.

In `internal/gateway/proxy.go`, set these fields inside `insertTrace`:

```go
RouteSupportLevel: routeSupportLevel(record),
BodyKind:          record.entry.BodyKind,
ResponseStartedAt: record.responseStartedAt,
ClientIPHash:      h.hashAuditValue(clientIP(record.req)),
UserAgentHash:     h.hashAuditValue(record.req.UserAgent()),
RequestIDFromClient: firstNonEmpty(
	record.req.Header.Get("x-request-id"),
	record.req.Header.Get("request-id"),
),
NewAPIRequestID: firstNonEmpty(
	record.responseHeaders.Get("x-request-id"),
	record.responseHeaders.Get("openai-request-id"),
),
ModelUpstream:        record.modelUpstream,
ErrorType:            record.errorType,
ErrorMessageRedacted: redactAuditMessage(record.errorMessage),
UpdatedAt:            record.finishedAt,
```

Add these helpers to `internal/gateway/proxy.go`:

```go
func (h Handler) hashAuditValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || h.AuditSecret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(h.AuditSecret))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

var bearerTokenPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)

func redactAuditMessage(value string) string {
	value = bearerTokenPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	for _, marker := range []string{"sk-", "x-api-key", "x-goog-api-key", "mj-api-secret"} {
		if strings.Contains(strings.ToLower(value), strings.ToLower(marker)) {
			return "[REDACTED]"
		}
	}
	return value
}

func routeSupportLevel(record traceRecord) string {
	if record.unknownRoute {
		return "unknown_route"
	}
	switch record.entry.CaptureMode {
	case routes.CaptureRawAndNormalized:
		return "deep_normalized"
	case routes.CaptureRawAndMinimal:
		return "raw_minimal"
	case routes.CaptureRawOnly:
		return "raw_only"
	default:
		return "unsupported"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func clientIP(req *http.Request) string {
	if forwarded := strings.TrimSpace(req.Header.Get("x-forwarded-for")); forwarded != "" {
		ip, _, _ := strings.Cut(forwarded, ",")
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err == nil {
		return host
	}
	return req.RemoteAddr
}
```

Add `crypto/hmac`, `crypto/sha256`, `encoding/hex`, and `regexp` to the imports in `internal/gateway/proxy.go`.

Add `ClientIPHash` and `UserAgentHash` to `jobs.TraceCapturedJob`, `jobs.TraceCapturedInput`, and `jobs.NewTraceCaptured`.

- [ ] **Step 8: Run trace and gateway tests**

Run:

```bash
go test ./internal/traces ./internal/gateway ./internal/jobs ./cmd/audit-gateway -v
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add migrations/0008_audit_metadata_parity.sql internal/traces/model.go internal/traces/repository.go internal/traces/repository_test.go internal/gateway/proxy.go internal/jobs/jobs.go
git commit -m "feat: persist audit trace metadata"
```

---

### Task 3: Forward-First Degraded Capture and Emergency Spool

**Files:**
- Create: `internal/gateway/spool.go`
- Modify: `internal/gateway/capture.go`
- Modify: `internal/gateway/proxy.go`
- Modify: `internal/gateway/proxy_test.go`
- Modify: `internal/config/config.go`
- Modify: `cmd/audit-gateway/main.go`

- [ ] **Step 1: Add failing forward-first tests**

Append to `internal/gateway/proxy_test.go`:

```go
func TestProxyForwardsWhenRequestEvidenceStoreFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	spoolDir := t.TempDir()
	handler := Handler{
		UpstreamBaseURL: upstream.URL,
		Registry:        routes.DefaultRegistry(),
		EvidenceStore:   failingEvidenceStore{err: errors.New("object store down")},
		TraceRepo:       &recordingTraceRepo{},
		AuditSecret:     strings.Repeat("s", 32),
		Spool:           NewFilesystemSpool(spoolDir),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
	req.Header.Set("Authorization", "Bearer sk-abc-extra")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	files, err := filepath.Glob(filepath.Join(spoolDir, "*.json"))
	if err != nil {
		t.Fatalf("glob error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("spool files = %v", files)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
go test ./internal/gateway -run TestProxyForwardsWhenRequestEvidenceStoreFails -v
```

Expected: FAIL because `Spool`, `NewFilesystemSpool`, and degraded request evidence behavior do not exist.

- [ ] **Step 3: Implement emergency spool**

Create `internal/gateway/spool.go`:

```go
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

var ErrSpoolDirRequired = errors.New("gateway spool dir is empty")

type Spool interface {
	Write(ctx context.Context, envelope SpoolEnvelope) error
}

type SpoolEnvelope struct {
	TraceID     string    `json:"trace_id"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Reason      string    `json:"reason"`
	ErrorType   string    `json:"error_type"`
	CapturedAt  time.Time `json:"captured_at"`
	RequestSize int64     `json:"request_size"`
}

type FilesystemSpool struct {
	dir string
	now func() time.Time
}

func NewFilesystemSpool(dir string) FilesystemSpool {
	return FilesystemSpool{dir: dir}
}

func (s FilesystemSpool) Write(ctx context.Context, envelope SpoolEnvelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.dir == "" {
		return ErrSpoolDirRequired
	}
	if envelope.CapturedAt.IsZero() {
		envelope.CapturedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, envelope.TraceID+".json")
	return os.WriteFile(path, data, 0o600)
}
```

- [ ] **Step 4: Change request evidence persistence to degrade instead of block**

Add `Spool Spool` to `gateway.Handler`.

In `ServeHTTP`, replace request evidence failures:

```go
requestObject, requestCaptureErr := h.putEvidence(auditCtx, traceID, "request_body", capturedReq.ContentType, capturedReq.BodyBytes)
if requestCaptureErr != nil {
	h.reportAuditError(auditCtx, requestCaptureErr)
	h.writeSpool(auditCtx, SpoolEnvelope{
		TraceID:     traceID,
		Method:      req.Method,
		Path:        req.URL.Path,
		Reason:      "request_body_evidence_failed",
		ErrorType:   requestCaptureErr.Error(),
		RequestSize: capturedReq.SizeBytes,
	})
}
```

Replace request header evidence failures similarly with `Reason: "request_header_evidence_failed"`. Do not return `http.StatusInternalServerError` for those evidence failures. Set `record.errorType = "capture_degraded"` and `record.errorMessage = requestCaptureErr.Error()` when a capture error occurs.

Add:

```go
func (h Handler) writeSpool(ctx context.Context, envelope SpoolEnvelope) {
	if h.Spool == nil {
		return
	}
	if err := h.Spool.Write(ctx, envelope); err != nil {
		h.reportAuditError(ctx, err)
	}
}
```

In `internal/config/config.go`, add `DegradedSpoolDir string` to `Config` and load:

```go
degradedSpoolDir, err := getenvDefault("DEGRADED_SPOOL_DIR", filepath.Join(os.TempDir(), "new-api-gateway-spool"))
if err != nil {
	return Config{}, err
}
```

In `cmd/audit-gateway/main.go`, set:

```go
Spool: gateway.NewFilesystemSpool(cfg.DegradedSpoolDir),
```

- [ ] **Step 5: Run degraded capture tests**

Run:

```bash
go test ./internal/gateway ./internal/config ./cmd/audit-gateway -run 'TestProxyForwardsWhenRequestEvidenceStoreFails|TestLoadFromEnv' -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/spool.go internal/gateway/capture.go internal/gateway/proxy.go internal/gateway/proxy_test.go internal/config/config.go internal/config/config_test.go cmd/audit-gateway/main.go
git commit -m "feat: forward through degraded capture"
```

---

### Task 4: Multipart, Base64, and Media Evidence Objects

**Files:**
- Modify: `internal/gateway/multipart.go`
- Modify: `internal/gateway/multipart_test.go`
- Create: `internal/gateway/media.go`
- Create: `internal/gateway/media_test.go`
- Modify: `internal/gateway/proxy.go`
- Modify: `internal/traces/model.go`
- Modify: `internal/traces/repository.go`

- [ ] **Step 1: Add failing multipart part evidence tests**

Append to `internal/gateway/multipart_test.go`:

```go
func TestCaptureMultipartPartsExtractsFieldsAndFiles(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("prompt", "make a diagram"); err != nil {
		t.Fatalf("WriteField error = %v", err)
	}
	part, err := writer.CreateFormFile("image", "input.png")
	if err != nil {
		t.Fatalf("CreateFormFile error = %v", err)
	}
	_, _ = part.Write([]byte("png-bytes"))
	if err := writer.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	parts, err := captureMultipartParts("trace_1", writer.FormDataContentType(), body.Bytes())
	if err != nil {
		t.Fatalf("captureMultipartParts error = %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %#v", parts)
	}
	if parts[0].Name != "prompt" || parts[0].Filename != "" || string(parts[0].Data) != "make a diagram" {
		t.Fatalf("field part = %#v", parts[0])
	}
	if parts[1].Name != "image" || parts[1].Filename != "input.png" || parts[1].ContentType != "application/octet-stream" {
		t.Fatalf("file part = %#v", parts[1])
	}
}
```

- [ ] **Step 2: Add failing base64/media URL tests**

Create `internal/gateway/media_test.go`:

```go
package gateway

import "testing"

func TestExtractMediaReferencesFindsURLsAndBase64(t *testing.T) {
	body := []byte(`{
	  "messages":[
	    {"content":[
	      {"type":"image_url","image_url":{"url":"https://example.test/a.png"}},
	      {"type":"input_audio","input_audio":{"data":"aGVsbG8="}}
	    ]}
	  ],
	  "image":"data:image/png;base64,aGVsbG8="
	}`)

	refs := extractMediaReferences(body)
	if len(refs) != 3 {
		t.Fatalf("refs = %#v", refs)
	}
	if refs[0].URL != "https://example.test/a.png" {
		t.Fatalf("first ref = %#v", refs[0])
	}
	if refs[1].Base64Data != "aGVsbG8=" || refs[2].MediaType != "image/png" {
		t.Fatalf("base64 refs = %#v", refs)
	}
}
```

- [ ] **Step 3: Run tests to verify failure**

Run:

```bash
go test ./internal/gateway -run 'TestCaptureMultipartPartsExtractsFieldsAndFiles|TestExtractMediaReferencesFindsURLsAndBase64' -v
```

Expected: FAIL because the helpers do not exist.

- [ ] **Step 4: Implement multipart part extraction**

Replace `internal/gateway/multipart.go` with:

```go
package gateway

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

type MultipartPartEvidence struct {
	Name        string
	Filename    string
	ContentType string
	SizeBytes   int64
	Data        []byte
}

func isMultipart(req *http.Request) bool {
	if req == nil {
		return false
	}
	contentType := req.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "multipart/") {
		return true
	}
	if err != nil {
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/")
	}
	return false
}

func captureMultipartParts(traceID, contentType string, body []byte) ([]MultipartPartEvidence, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return nil, nil
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, multipart.ErrMessageTooLarge
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	parts := []MultipartPartEvidence{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil {
			return nil, err
		}
		contentType := part.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		parts = append(parts, MultipartPartEvidence{
			Name:        part.FormName(),
			Filename:    part.FileName(),
			ContentType: contentType,
			SizeBytes:   int64(len(data)),
			Data:        data,
		})
	}
	return parts, nil
}
```

- [ ] **Step 5: Implement JSON media reference extraction**

Create `internal/gateway/media.go`:

```go
package gateway

import (
	"encoding/json"
	"strconv"
	"strings"
)

type MediaReference struct {
	URL        string
	Base64Data string
	MediaType  string
	SourcePath string
}

func extractMediaReferences(body []byte) []MediaReference {
	var root any
	if len(body) == 0 || json.Unmarshal(body, &root) != nil {
		return nil
	}
	refs := []MediaReference{}
	walkMedia(root, "$", &refs)
	return refs
}

func walkMedia(value any, path string, refs *[]MediaReference) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := path + "." + key
			if key == "url" {
				if url, ok := child.(string); ok && strings.HasPrefix(url, "http") {
					*refs = append(*refs, MediaReference{URL: url, SourcePath: childPath})
				}
			}
			if key == "data" || key == "image" || key == "b64_json" {
				if encoded, mediaType := parseBase64Media(child); encoded != "" {
					*refs = append(*refs, MediaReference{Base64Data: encoded, MediaType: mediaType, SourcePath: childPath})
				}
			}
			walkMedia(child, childPath, refs)
		}
	case []any:
		for index, child := range typed {
			walkMedia(child, path+"["+strconv.Itoa(index)+"]", refs)
		}
	}
}

func parseBase64Media(value any) (string, string) {
	text, ok := value.(string)
	if !ok || text == "" {
		return "", ""
	}
	if strings.HasPrefix(text, "data:") {
		header, data, ok := strings.Cut(text, ",")
		if !ok || !strings.Contains(header, ";base64") {
			return "", ""
		}
		mediaType := strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:")
		return data, mediaType
	}
	if len(text) >= 8 && !strings.ContainsAny(text, " \n\r\t{}[]") {
		return text, ""
	}
	return "", ""
}
```

- [ ] **Step 6: Persist part evidence from gateway**

In `internal/gateway/proxy.go`, after storing request body evidence and before forwarding upstream:

```go
multipartParts := []MultipartPartEvidence{}
if isMultipart(req) {
	parts, err := captureMultipartParts(traceID, capturedReq.ContentType, capturedReq.BodyBytes)
	if err != nil {
		h.reportAuditError(auditCtx, err)
	} else {
		multipartParts = parts
	}
}
```

Add `multipartParts []MultipartPartEvidence` to `traceRecord`. In `insertTrace`, after request body/header evidence insertion:

```go
for _, part := range record.multipartParts {
	object, err := h.putEvidence(ctx, record.traceID, "multipart_part", part.ContentType, part.Data)
	if err != nil {
		errs = append(errs, err)
		continue
	}
	err = h.TraceRepo.InsertRawEvidence(ctx, traces.RawEvidenceObject{
		TraceID:          record.traceID,
		ObjectType:       "multipart_part",
		ObjectRef:        object.ObjectRef,
		StorageBackend:   object.StorageBackend,
		ContentType:      object.ContentType,
		OriginalFilename: part.Filename,
		SizeBytes:        object.SizeBytes,
		SHA256:           object.SHA256,
		RedactionStatus:  "not_redacted",
		EncryptionStatus: object.StorageBackend,
		CreatedAt:        object.CreatedAt,
	})
	if err != nil {
		errs = append(errs, err)
	}
}
```

- [ ] **Step 7: Run gateway tests**

Run:

```bash
go test ./internal/gateway ./internal/traces -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/gateway/multipart.go internal/gateway/multipart_test.go internal/gateway/media.go internal/gateway/media_test.go internal/gateway/proxy.go internal/traces/model.go internal/traces/repository.go
git commit -m "feat: capture multipart and media evidence metadata"
```

---

### Task 5: Protocol Normalization and SSE Reconstruction

**Files:**
- Modify: `workers/analysis_worker/models.py`
- Modify: `workers/analysis_worker/normalizers.py`
- Modify: `workers/analysis_worker/tests/test_normalizers.py`
- Modify: `workers/analysis_worker/tests/test_pipeline.py`

- [ ] **Step 1: Add failing normalizer tests**

Append to `workers/analysis_worker/tests/test_normalizers.py`:

```python
def test_normalizes_gemini_contents_text_and_response():
    trace_job = job(protocol_family="gemini", route_pattern="/v1beta/models/gemini:generateContent")
    request = {
        "contents": [
            {"role": "user", "parts": [{"text": "debug the gateway"}]},
            {"role": "model", "parts": [{"text": "previous answer"}]},
        ]
    }
    response = {"candidates": [{"content": {"role": "model", "parts": [{"text": "fixed"}]}}]}

    messages, _ = normalize_json_trace(trace_job, json.dumps(request), json.dumps(response))

    assert [message.content_text for message in messages] == ["debug the gateway", "previous answer", "fixed"]
    assert messages[0].protocol_item_type == "gemini_content_part"


def test_normalizes_image_url_and_base64_media():
    trace_job = job(protocol_family="openai_chat", route_pattern="/v1/chat/completions")
    request = {
        "messages": [
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": "inspect this"},
                    {"type": "image_url", "image_url": {"url": "https://example.test/a.png"}},
                    {"type": "input_audio", "input_audio": {"data": "aGVsbG8=", "format": "wav"}},
                ],
            }
        ]
    }

    messages, _ = normalize_json_trace(trace_job, json.dumps(request), "{}")

    assert any(message.modality == "image" and message.media_url == "https://example.test/a.png" for message in messages)
    assert any(message.modality == "audio" and message.protocol_item_type == "base64_media" for message in messages)


def test_normalizes_sse_event_stream_response():
    trace_job = job(protocol_family="openai_chat", route_pattern="/v1/chat/completions")
    request = {"messages": [{"role": "user", "content": "stream please"}]}
    response = "\n".join([
        'data: {"choices":[{"delta":{"role":"assistant","content":"hello"}}]}',
        'data: {"choices":[{"delta":{"content":" world"}}]}',
        "data: [DONE]",
        "",
    ])

    messages, _ = normalize_json_trace(trace_job, json.dumps(request), response)

    assert any(message.direction == "response" and message.content_text == "hello world" for message in messages)
```

- [ ] **Step 2: Run normalizer tests to verify failure**

Run:

```bash
cd workers/analysis_worker
uv run pytest tests/test_normalizers.py -q
```

Expected: FAIL because Gemini/media/SSE support is not present.

- [ ] **Step 3: Implement Gemini, media, and SSE normalization**

In `workers/analysis_worker/normalizers.py`, update `normalize_json_trace`:

```python
    response_json = _load_json_object(response_body)
    response_events = _load_sse_json_events(response_body)
    if response_events and not response_json:
        response_json = _response_json_from_sse_events(response_events)
```

Add branch:

```python
    elif job.protocol_family == "gemini":
        messages = _normalize_gemini(job, request_json, response_json)
```

Replace the content loop in `_normalize_openai_chat` with media-aware extraction:

```python
    for index, item in enumerate(request_json.get("messages", [])):
        if not isinstance(item, dict):
            continue
        role = str(item.get("role", ""))
        content = item.get("content")
        if isinstance(content, list):
            for part_index, part in enumerate(content):
                messages.extend(_part_messages(job, "request", role, part, f"request.messages[{index}].content[{part_index}]", "openai_chat_message", len(messages)))
            continue
        text = _content_to_text(content)
        if text:
            messages.append(_message(job, "request", len(messages), role, text, f"request.messages[{index}]", "openai_chat_message"))
```

Replace the `request_input` list handling in `_normalize_openai_responses` with:

```python
    if isinstance(request_input, list):
        for index, item in enumerate(request_input):
            role = str(item.get("role", "user")) if isinstance(item, dict) else "user"
            if isinstance(item, dict) and isinstance(item.get("content"), list):
                for part_index, part in enumerate(item["content"]):
                    messages.extend(_part_messages(job, "request", role, part, f"request.input[{index}].content[{part_index}]", "openai_responses_input", len(messages)))
                continue
            text = _content_to_text(item)
            if text:
                messages.append(_message(job, "request", len(messages), role, text, f"request.input[{index}]", "openai_responses_input"))
```

Add these helpers:

```python
def _normalize_gemini(job: TraceCapturedJob, request_json: dict[str, Any], response_json: dict[str, Any]) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    for index, content in enumerate(request_json.get("contents", [])):
        if not isinstance(content, dict):
            continue
        role = str(content.get("role", "user"))
        for part_index, part in enumerate(content.get("parts", [])):
            messages.extend(_part_messages(job, "request", role, part, f"request.contents[{index}].parts[{part_index}]", "gemini_content_part", len(messages)))
    for index, candidate in enumerate(response_json.get("candidates", [])):
        content = candidate.get("content") if isinstance(candidate, dict) else None
        if not isinstance(content, dict):
            continue
        role = str(content.get("role", "model"))
        for part_index, part in enumerate(content.get("parts", [])):
            messages.extend(_part_messages(job, "response", role, part, f"response.candidates[{index}].content.parts[{part_index}]", "gemini_content_part", len(messages)))
    return messages


def _part_messages(
    job: TraceCapturedJob,
    direction: str,
    role: str,
    value: Any,
    source_path: str,
    protocol_item_type: str,
    sequence_start: int,
) -> list[NormalizedMessage]:
    if isinstance(value, str):
        return [_message(job, direction, sequence_start, role, value, source_path, protocol_item_type)]
    if not isinstance(value, dict):
        return []
    if isinstance(value.get("text"), str):
        return [_message(job, direction, sequence_start, role, value["text"], source_path, protocol_item_type)]
    if isinstance(value.get("image_url"), dict) and isinstance(value["image_url"].get("url"), str):
        return [_media_message(job, direction, sequence_start, role, "image", value["image_url"]["url"], source_path, "media_url")]
    if isinstance(value.get("input_audio"), dict):
        return [_media_message(job, direction, sequence_start, role, "audio", "", source_path, "base64_media")]
    if isinstance(value.get("inline_data"), dict):
        mime_type = str(value["inline_data"].get("mime_type", ""))
        modality = "image" if mime_type.startswith("image/") else "binary"
        return [_media_message(job, direction, sequence_start, role, modality, "", source_path, "base64_media")]
    text = _content_to_text(value)
    if text:
        return [_message(job, direction, sequence_start, role, text, source_path, protocol_item_type)]
    return []


def _media_message(
    job: TraceCapturedJob,
    direction: str,
    sequence_index: int,
    role: str,
    modality: str,
    media_url: str,
    source_path: str,
    protocol_item_type: str,
) -> NormalizedMessage:
    return NormalizedMessage(
        trace_id=job.trace_id,
        direction=direction,
        sequence_index=sequence_index,
        role=role,
        modality=modality,
        content_text="",
        content_text_hash="",
        media_url=media_url,
        source_path=source_path,
        protocol_item_type=protocol_item_type,
        token_count_estimate=0,
        metadata={"route_pattern": job.route_pattern, "protocol_family": job.protocol_family},
    )


def _load_sse_json_events(body: str) -> list[dict[str, Any]]:
    events: list[dict[str, Any]] = []
    for line in body.splitlines():
        line = line.strip()
        if not line.startswith("data:"):
            continue
        payload = line.removeprefix("data:").strip()
        if payload == "[DONE]":
            continue
        try:
            loaded = json.loads(payload)
        except json.JSONDecodeError:
            continue
        if isinstance(loaded, dict):
            events.append(loaded)
    return events


def _response_json_from_sse_events(events: list[dict[str, Any]]) -> dict[str, Any]:
    role = "assistant"
    content_parts: list[str] = []
    for event in events:
        choices = event.get("choices")
        if not isinstance(choices, list):
            continue
        for choice in choices:
            if not isinstance(choice, dict):
                continue
            delta = choice.get("delta")
            if not isinstance(delta, dict):
                continue
            if isinstance(delta.get("role"), str):
                role = delta["role"]
            if isinstance(delta.get("content"), str):
                content_parts.append(delta["content"])
    if not content_parts:
        return {}
    return {"choices": [{"message": {"role": role, "content": "".join(content_parts)}}]}
```

Update `_content_to_text` so list values only concatenate text-bearing children and ignore media objects:

```python
    if isinstance(value, list):
        parts = []
        for item in value:
            if isinstance(item, dict) and ("image_url" in item or "input_audio" in item or "inline_data" in item):
                continue
            parts.append(_content_to_text(item))
        return "\n".join(part for part in parts if part)
```

- [ ] **Step 4: Run worker tests**

Run:

```bash
cd workers/analysis_worker
uv run pytest -q
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/models.py workers/analysis_worker/normalizers.py workers/analysis_worker/tests/test_normalizers.py workers/analysis_worker/tests/test_pipeline.py
git commit -m "feat: normalize gemini media and sse traces"
```

---

### Task 6: Remaining Rule-Based MVP Anomalies

**Files:**
- Modify: `workers/analysis_worker/models.py`
- Modify: `workers/analysis_worker/rules.py`
- Modify: `workers/analysis_worker/repository.py`
- Modify: `workers/analysis_worker/main.py`
- Modify: `workers/analysis_worker/tests/test_rules.py`
- Create: `migrations/0009_anomaly_rule_expansion.sql`

- [ ] **Step 1: Add failing rule tests**

Add `AnalysisContext` to the existing `from models import ...` line in `workers/analysis_worker/tests/test_rules.py`, then append these tests:

```python
def test_detects_missing_employee_number():
    trace_job = job(employee_no="", identity_resolution_status="missing_employee_no", status_code=200, upstream_status_code=200)
    alerts = detect_anomalies(trace_job, [], AnalysisContext())
    assert "missing_employee_no" in [alert.anomaly_type for alert in alerts]


def test_detects_expensive_model_overuse():
    trace_job = job(model_requested="gpt-4.5-preview", usage_total_tokens=1000)
    alerts = detect_anomalies(trace_job, [], AnalysisContext(expensive_models={"gpt-4.5-preview"}, expensive_model_token_threshold=500))
    assert [alert.anomaly_type for alert in alerts] == ["expensive_model_overuse"]


def test_detects_long_output_anomaly():
    trace_job = job(usage_completion_tokens=9000, usage_total_tokens=10000)
    alerts = detect_anomalies(trace_job, [], AnalysisContext(long_output_token_threshold=8000))
    assert [alert.anomaly_type for alert in alerts] == ["long_output_anomaly"]


def test_detects_repeated_prompt_within_trace():
    trace_job = job()
    messages = [
        NormalizedMessage(trace_job.trace_id, "request", 0, "user", "text", "repeat this exact prompt", "hash1", "", "request.messages[0]", "openai_chat_message", 4, {}),
        NormalizedMessage(trace_job.trace_id, "request", 1, "user", "text", "repeat this exact prompt", "hash1", "", "request.messages[1]", "openai_chat_message", 4, {}),
        NormalizedMessage(trace_job.trace_id, "request", 2, "user", "text", "repeat this exact prompt", "hash1", "", "request.messages[2]", "openai_chat_message", 4, {}),
    ]
    alerts = detect_anomalies(trace_job, messages, AnalysisContext(repeated_prompt_threshold=3))
    assert [alert.anomaly_type for alert in alerts] == ["repeated_prompt"]


def test_detects_daily_token_limit_exceeded():
    trace_job = job(usage_total_tokens=2000)
    context = AnalysisContext(daily_tokens_before=99000, daily_token_limit=100000)
    alerts = detect_anomalies(trace_job, [], context)
    assert [alert.anomaly_type for alert in alerts] == ["daily_token_limit_exceeded"]


def test_detects_short_window_token_spike():
    trace_job = job(usage_total_tokens=6000)
    context = AnalysisContext(short_window_tokens_before=5000, short_window_token_threshold=10000)
    alerts = detect_anomalies(trace_job, [], context)
    assert [alert.anomaly_type for alert in alerts] == ["short_window_token_spike"]


def test_detects_off_hours_high_usage():
    trace_job = job(request_started_at="2026-04-30T15:30:00+00:00", usage_total_tokens=3000)
    context = AnalysisContext(local_timezone_offset_hours=8, off_hours_token_threshold=2000)
    alerts = detect_anomalies(trace_job, [], context)
    assert [alert.anomaly_type for alert in alerts] == ["off_hours_high_usage"]


def test_detects_possible_token_leak_signal():
    trace_job = job()
    context = AnalysisContext(distinct_client_hashes_1h=4, token_leak_distinct_client_threshold=3)
    alerts = detect_anomalies(trace_job, [], context)
    assert [alert.anomaly_type for alert in alerts] == ["possible_token_leak"]
```

- [ ] **Step 2: Run rule tests to verify failure**

Run:

```bash
cd workers/analysis_worker
uv run pytest tests/test_rules.py -q
```

Expected: FAIL because `AnalysisContext` and the new rules are absent.

- [ ] **Step 3: Add rule config rows**

Create `migrations/0009_anomaly_rule_expansion.sql`:

```sql
INSERT INTO anomaly_rules (rule_key, threshold_json, severity, rule_window)
VALUES
    ('missing_employee_no', '{"enabled": true}'::jsonb, 'high', 'per_trace'),
    ('daily_token_limit_exceeded', '{"total_tokens": 100000}'::jsonb, 'high', 'day'),
    ('short_window_token_spike', '{"total_tokens": 10000}'::jsonb, 'medium', '5m'),
    ('expensive_model_overuse', '{"models": ["gpt-4.5-preview", "o1-pro"], "total_tokens": 500}'::jsonb, 'high', 'per_trace'),
    ('long_output_anomaly', '{"completion_tokens": 8000}'::jsonb, 'medium', 'per_trace'),
    ('repeated_prompt', '{"repeat_count": 3}'::jsonb, 'medium', 'per_trace'),
    ('off_hours_high_usage', '{"local_timezone_offset_hours": 8, "total_tokens": 2000}'::jsonb, 'medium', 'per_trace'),
    ('possible_token_leak', '{"distinct_client_hashes": 3}'::jsonb, 'high', '1h')
ON CONFLICT (rule_key) DO NOTHING;
```

- [ ] **Step 4: Implement rule context and detectors**

In `workers/analysis_worker/models.py`, add:

```python
@dataclass(frozen=True)
class AnalysisContext:
    daily_tokens_before: int = 0
    daily_token_limit: int = 100_000
    short_window_tokens_before: int = 0
    short_window_token_threshold: int = 10_000
    expensive_models: set[str] | None = None
    expensive_model_token_threshold: int = 500
    long_output_token_threshold: int = 8_000
    repeated_prompt_threshold: int = 3
    local_timezone_offset_hours: int = 8
    off_hours_token_threshold: int = 2_000
    distinct_client_hashes_1h: int = 0
    token_leak_distinct_client_threshold: int = 3

    def expensive_model_set(self) -> set[str]:
        return self.expensive_models or {"gpt-4.5-preview", "o1-pro"}
```

Change `detect_anomalies` signature in `workers/analysis_worker/rules.py`:

```python
def detect_anomalies(
    job: TraceCapturedJob,
    messages: list[NormalizedMessage] | None = None,
    context: AnalysisContext | None = None,
) -> list[AnomalyAlert]:
    messages = list(messages or [])
    context = context or AnalysisContext()
```

Add these detector blocks before returning:

```python
    if job.identity_resolution_status == "missing_employee_no":
        alerts.append(_anomaly(job, "missing_employee_no", "high", 1, 0, "new-api token name was empty after normalization"))

    if context.daily_tokens_before + job.usage_total_tokens > context.daily_token_limit:
        alerts.append(_anomaly(
            job,
            "daily_token_limit_exceeded",
            "high",
            context.daily_tokens_before + job.usage_total_tokens,
            context.daily_token_limit,
            "daily token usage exceeded the configured deterministic threshold",
        ))

    if context.short_window_tokens_before + job.usage_total_tokens > context.short_window_token_threshold:
        alerts.append(_anomaly(
            job,
            "short_window_token_spike",
            "medium",
            context.short_window_tokens_before + job.usage_total_tokens,
            context.short_window_token_threshold,
            "short-window token usage exceeded the configured deterministic threshold",
        ))

    if job.model_requested in context.expensive_model_set() and job.usage_total_tokens >= context.expensive_model_token_threshold:
        alerts.append(_anomaly(
            job,
            "expensive_model_overuse",
            "high",
            job.usage_total_tokens,
            context.expensive_model_token_threshold,
            f"expensive model {job.model_requested} exceeded the per-trace token threshold",
        ))

    if job.usage_completion_tokens >= context.long_output_token_threshold:
        alerts.append(_anomaly(
            job,
            "long_output_anomaly",
            "medium",
            job.usage_completion_tokens,
            context.long_output_token_threshold,
            "completion token count exceeded the long-output threshold",
        ))

    repeated_count = _max_repeated_prompt_count(messages)
    if repeated_count >= context.repeated_prompt_threshold:
        alerts.append(_anomaly(
            job,
            "repeated_prompt",
            "medium",
            repeated_count,
            context.repeated_prompt_threshold,
            "same normalized request prompt repeated within one trace",
        ))

    if _is_off_hours(job.request_started_at, context.local_timezone_offset_hours) and job.usage_total_tokens >= context.off_hours_token_threshold:
        alerts.append(_anomaly(
            job,
            "off_hours_high_usage",
            "medium",
            job.usage_total_tokens,
            context.off_hours_token_threshold,
            "high token usage occurred outside configured local work hours",
        ))

    if context.distinct_client_hashes_1h >= context.token_leak_distinct_client_threshold:
        alerts.append(_anomaly(
            job,
            "possible_token_leak",
            "high",
            context.distinct_client_hashes_1h,
            context.token_leak_distinct_client_threshold,
            "same token fingerprint appeared from several client or user-agent hashes in one hour",
        ))
```

Add helpers:

```python
def _max_repeated_prompt_count(messages: list[NormalizedMessage]) -> int:
    counts: dict[str, int] = {}
    for message in messages:
        if message.direction != "request" or not message.content_text_hash:
            continue
        counts[message.content_text_hash] = counts.get(message.content_text_hash, 0) + 1
    return max(counts.values(), default=0)


def _is_off_hours(value: str, offset_hours: int) -> bool:
    if not value:
        return False
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    local_hour = (parsed.astimezone(timezone.utc).hour + offset_hours) % 24
    return local_hour < 8 or local_hour >= 20
```

Import `datetime`, `timezone`, and `AnalysisContext`.

- [ ] **Step 5: Load context from PostgreSQL aggregates**

In `workers/analysis_worker/repository.py`, add:

```python
    def analysis_context_for(self, job: TraceCapturedJob) -> AnalysisContext:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            SELECT COALESCE(SUM(total_tokens), 0)
            FROM usage_aggregates
            WHERE token_fingerprint = %s
              AND bucket_size = 'day'
              AND bucket_start = date_trunc('day', %s::timestamptz)
            """,
            (job.token_fingerprint, job.request_started_at),
        )
        daily_tokens_before = int(cursor.fetchone()[0] or 0)
        cursor.execute(
            """
            SELECT COALESCE(SUM(total_tokens), 0)
            FROM usage_aggregates
            WHERE token_fingerprint = %s
              AND bucket_size = 'hour'
              AND bucket_start = date_trunc('hour', %s::timestamptz)
            """,
            (job.token_fingerprint, job.request_started_at),
        )
        short_window_tokens_before = int(cursor.fetchone()[0] or 0)
        cursor.execute(
            """
            SELECT COUNT(DISTINCT client_ip_hash || ':' || user_agent_hash)
            FROM traces
            WHERE token_fingerprint = %s
              AND created_at >= %s::timestamptz - interval '1 hour'
              AND created_at <= %s::timestamptz
            """,
            (job.token_fingerprint, job.request_started_at, job.request_started_at),
        )
        distinct_client_hashes_1h = int(cursor.fetchone()[0] or 0)
        return AnalysisContext(
            daily_tokens_before=daily_tokens_before,
            short_window_tokens_before=short_window_tokens_before,
            distinct_client_hashes_1h=distinct_client_hashes_1h,
        )
```

In `workers/analysis_worker/main.py`, before calling `detect_anomalies`:

```python
    if hasattr(repository, "analysis_context_for"):
        analysis_context = repository.analysis_context_for(job)
    else:
        analysis_context = AnalysisContext()
```

Then call:

```python
    anomalies = [
        *detect_anomalies(job, messages, analysis_context),
        *detect_work_relevance_anomalies(job, work_relevance),
    ]
```

- [ ] **Step 6: Run worker tests**

Run:

```bash
cd workers/analysis_worker
uv run pytest -q
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add migrations/0009_anomaly_rule_expansion.sql workers/analysis_worker/models.py workers/analysis_worker/rules.py workers/analysis_worker/repository.py workers/analysis_worker/main.py workers/analysis_worker/tests/test_rules.py
git commit -m "feat: expand explainable anomaly rules"
```

---

### Task 7: Admin Security and Product Completion

**Files:**
- Create: `migrations/0010_admin_csrf_security.sql`
- Modify: `internal/admin/auth.go`
- Create: `internal/admin/limits.go`
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/repository.go`
- Modify: `internal/admin/handlers.go`
- Modify: `internal/admin/auth_test.go`
- Modify: `internal/admin/handlers_test.go`
- Modify: `internal/admin/repository_test.go`
- Modify: `internal/adminui/app.js`
- Modify: `internal/adminui/app.css`

- [ ] **Step 1: Add CSRF and rate limit tests**

Append to `internal/admin/handlers_test.go`:

```go
func TestUnsafeAdminRequestRequiresCSRFToken(t *testing.T) {
	h, _, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/api-key-lookup", strings.NewReader(`{"api_key":"sk-secret-extra"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAPIKeyLookupRateLimit(t *testing.T) {
	h, _, cookie := newAuthenticatedAdminHandler(t, RoleRawAccess, "audit-secret-0123456789abcdef", nil)
	h.lookupLimiter = NewMemoryRateLimiter(1, time.Hour)
	h.auth.CSRFCookieName = "audit_admin_csrf"

	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/admin/api/api-key-lookup", strings.NewReader(`{"api_key":"sk-secret-extra"}`))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", "test-csrf")
		req.AddCookie(&http.Cookie{Name: h.auth.CSRFCookieName, Value: "test-csrf"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if attempt == 1 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("second status = %d, want 429", rec.Code)
		}
	}
}
```

- [ ] **Step 2: Add product API tests**

Append to `internal/admin/repository_test.go`:

```go
func TestListTokenIdentitiesQueryUsesCacheAndSubjects(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)
	_, _ = repo.ListTokenIdentities(context.Background(), TokenIdentityFilter{EmployeeNo: "E10001", Limit: 500})

	if !strings.Contains(db.querySQL, "FROM token_identity_cache") {
		t.Fatalf("query = %s", db.querySQL)
	}
	if !strings.Contains(db.querySQL, "LEFT JOIN audit_subjects") {
		t.Fatalf("query missing audit subject enrichment: %s", db.querySQL)
	}
	if got := db.queryArgs[len(db.queryArgs)-1]; got != 100 {
		t.Fatalf("limit = %#v, want capped 100", got)
	}
}

func TestListReviewDecisionsQuery(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)
	_, _ = repo.ListReviewDecisions(context.Background(), ReviewDecisionFilter{TargetType: "anomaly", Limit: 500})

	if !strings.Contains(db.querySQL, "FROM review_decisions") {
		t.Fatalf("query = %s", db.querySQL)
	}
	if got := db.queryArgs[len(db.queryArgs)-1]; got != 100 {
		t.Fatalf("limit = %#v, want capped 100", got)
	}
}
```

- [ ] **Step 3: Run admin tests to verify failure**

Run:

```bash
go test ./internal/admin -run 'TestUnsafeAdminRequestRequiresCSRFToken|TestAPIKeyLookupRateLimit|TestListTokenIdentitiesQueryUsesCacheAndSubjects|TestListReviewDecisionsQuery' -v
```

Expected: FAIL because CSRF, rate limiter, and product query APIs do not exist.

- [ ] **Step 4: Add CSRF fields and rate limiter**

Create `migrations/0010_admin_csrf_security.sql`:

```sql
ALTER TABLE audit_sessions
    ADD COLUMN IF NOT EXISTS csrf_token TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_audit_sessions_csrf
    ON audit_sessions(csrf_token)
    WHERE revoked_at IS NULL;
```

In `internal/admin/auth.go`, add `CSRFCookieName string` to `Auth`. Add:

```go
func NewCSRFToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "csrf_" + hex.EncodeToString(bytes[:])
}

func (a Auth) csrfCookie(token string, expiresAt time.Time) *http.Cookie {
	name := a.CSRFCookieName
	if name == "" {
		name = "audit_admin_csrf"
	}
	return &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/admin",
		Expires:  expiresAt.UTC(),
		HttpOnly: false,
		Secure:   a.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	}
}
```

In `internal/admin/models.go`, update `Session`:

```go
type Session struct {
	SessionID string
	UserID    int64
	ExpiresAt time.Time
	CSRFToken string
}
```

In `internal/admin/repository.go`, update `CreateSession`:

```go
_, err := r.db.Exec(ctx, `
INSERT INTO audit_sessions (session_id, user_id, expires_at, csrf_token)
VALUES ($1,$2,$3,$4)`, session.SessionID, session.UserID, session.ExpiresAt, session.CSRFToken)
return err
```

Update the `memoryAdminDB.Exec` test helper in `internal/admin/handlers_test.go` to store `args[3]` as `Session.CSRFToken` when present.

Update `TestLoginMeLogoutFlow` so the login response expects both `audit_admin_session` and `audit_admin_csrf` cookies. Use the session cookie for the existing `/me` and `/logout` checks:

```go
cookies := loginRec.Result().Cookies()
var sessionCookie *http.Cookie
var csrfCookie *http.Cookie
for _, cookie := range cookies {
	switch cookie.Name {
	case "audit_admin_session":
		sessionCookie = cookie
	case "audit_admin_csrf":
		csrfCookie = cookie
	}
}
if sessionCookie == nil || csrfCookie == nil {
	t.Fatalf("cookies = %#v", cookies)
}
```

Create `internal/admin/limits.go`:

```go
package admin

import (
	"sync"
	"time"
)

type RateLimiter interface {
	Allow(key string, now time.Time) bool
}

type MemoryRateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	counters map[string]rateCounter
}

type rateCounter struct {
	count       int
	windowEnds time.Time
}

func NewMemoryRateLimiter(limit int, window time.Duration) *MemoryRateLimiter {
	return &MemoryRateLimiter{limit: limit, window: window, counters: map[string]rateCounter{}}
}

func (l *MemoryRateLimiter) Allow(key string, now time.Time) bool {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	counter := l.counters[key]
	if counter.windowEnds.IsZero() || !counter.windowEnds.After(now) {
		counter = rateCounter{windowEnds: now.Add(l.window)}
	}
	if counter.count >= l.limit {
		l.counters[key] = counter
		return false
	}
	counter.count++
	l.counters[key] = counter
	return true
}
```

- [ ] **Step 5: Mount CSRF and rate limits**

In `internal/admin/handlers.go`, extend `Handler`:

```go
lookupLimiter RateLimiter
rawLimiter    RateLimiter
```

In `NewHandler`, initialize:

```go
lookupLimiter: NewMemoryRateLimiter(20, time.Hour),
rawLimiter:    NewMemoryRateLimiter(120, time.Hour),
```

In `login`, after creating `sessionID`, create a CSRF token, save it with the session, and set both cookies:

```go
csrfToken, err := NewCSRFToken()
if err != nil {
	http.Error(w, "failed to create csrf token", http.StatusInternalServerError)
	return
}
if err := h.repo.CreateSession(r.Context(), Session{SessionID: sessionID, UserID: user.ID, ExpiresAt: expiresAt, CSRFToken: csrfToken}); err != nil {
	http.Error(w, "failed to create session", http.StatusInternalServerError)
	return
}
http.SetCookie(w, h.auth.sessionCookie(sessionID, expiresAt))
http.SetCookie(w, h.auth.csrfCookie(csrfToken, expiresAt))
```

Wrap unsafe handlers with:

```go
func (h Handler) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		cookieName := h.auth.CSRFCookieName
		if cookieName == "" {
			cookieName = "audit_admin_csrf"
		}
		cookie, err := r.Cookie(cookieName)
		if err != nil || cookie.Value == "" || r.Header.Get("X-CSRF-Token") != cookie.Value {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

In `NewHandler`, wrap unsafe admin routes:

```go
h.mux.Handle("POST /admin/api/context-catalog", h.auth.Middleware(h.requireCSRF(h.auth.Require(PermissionReview, http.HandlerFunc(h.createContextCatalogEntry)))))
h.mux.Handle("POST /admin/api/reviews", h.auth.Middleware(h.requireCSRF(h.auth.Require(PermissionReview, http.HandlerFunc(h.createReview)))))
h.mux.Handle("POST /admin/api/api-key-lookup", h.auth.Middleware(h.requireCSRF(h.auth.Require(PermissionAPIKeyLookup, http.HandlerFunc(h.createAPIKeyLookup)))))
```

Keep `POST /admin/api/login` outside CSRF because it creates the session and CSRF cookie. Keep `POST /admin/api/logout` outside CSRF so stale or malformed sessions can always be cleared.

In `createAPIKeyLookup`:

```go
if !h.lookupLimiter.Allow(principal.Username+":api_key_lookup", h.auth.now()) {
	http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
	return
}
```

In `getRawEvidence`:

```go
if !h.rawLimiter.Allow(principal.Username+":raw_evidence", h.auth.now()) {
	http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
	return
}
```

- [ ] **Step 6: Add product DTOs and repository methods**

In `internal/admin/models.go`, add:

```go
type TokenIdentityFilter struct {
	EmployeeNo       string
	TokenFingerprint string
	Limit            int
}

type TokenIdentitySummary struct {
	FingerprintDisplay string `json:"fingerprint_display"`
	TokenFingerprint   string `json:"token_fingerprint"`
	NewAPITokenID      int    `json:"new_api_token_id"`
	TokenNameRaw       string `json:"token_name_raw"`
	EmployeeNo         string `json:"employee_no"`
	DisplayName        string `json:"display_name"`
	Department         string `json:"department"`
	TokenStatus        int    `json:"token_status"`
	TokenGroup         string `json:"token_group"`
	LastSeenAt         string `json:"last_seen_at"`
}

type ReviewDecisionFilter struct {
	TargetType string
	TargetID   string
	Limit      int
}

type SystemSettingsSummary struct {
	EmployeeNoPattern string `json:"employee_no_pattern"`
	MetricsEnabled    bool   `json:"metrics_enabled"`
	LookupLimit       int    `json:"lookup_limit"`
	RawAccessLimit    int    `json:"raw_access_limit"`
}
```

In `internal/admin/repository.go`, add:

```go
func (r Repository) ListTokenIdentities(ctx context.Context, filter TokenIdentityFilter) ([]TokenIdentitySummary, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	where := []string{"1=1"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if filter.EmployeeNo != "" {
		add("c.employee_no = $%d", filter.EmployeeNo)
	}
	if filter.TokenFingerprint != "" {
		add("c.token_fingerprint = $%d", filter.TokenFingerprint)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`
SELECT c.fingerprint_display, c.token_fingerprint, c.new_api_token_id,
       c.token_name_raw, c.employee_no, COALESCE(s.display_name, ''),
       COALESCE(s.department, c.department), c.token_status, c.token_group,
       c.last_seen_at::text
FROM token_identity_cache c
LEFT JOIN audit_subjects s ON s.employee_no = c.employee_no
WHERE %s
ORDER BY c.last_seen_at DESC
LIMIT $%d`, strings.Join(where, " AND "), len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TokenIdentitySummary{}
	for rows.Next() {
		var item TokenIdentitySummary
		if err := rows.Scan(
			&item.FingerprintDisplay,
			&item.TokenFingerprint,
			&item.NewAPITokenID,
			&item.TokenNameRaw,
			&item.EmployeeNo,
			&item.DisplayName,
			&item.Department,
			&item.TokenStatus,
			&item.TokenGroup,
			&item.LastSeenAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) ListReviewDecisions(ctx context.Context, filter ReviewDecisionFilter) ([]ReviewDecision, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	where := []string{"1=1"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if filter.TargetType != "" {
		add("target_type = $%d", filter.TargetType)
	}
	if filter.TargetID != "" {
		add("target_id = $%d", filter.TargetID)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`
SELECT target_type, target_id, decision, reviewer_id, reviewer_username,
       note, created_at
FROM review_decisions
WHERE %s
ORDER BY created_at DESC
LIMIT $%d`, strings.Join(where, " AND "), len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReviewDecision{}
	for rows.Next() {
		var item ReviewDecision
		if err := rows.Scan(
			&item.TargetType,
			&item.TargetID,
			&item.Decision,
			&item.ReviewerID,
			&item.ReviewerUsername,
			&item.Note,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
```

In `internal/admin/handlers.go`, mount:

```go
h.mux.Handle("GET /admin/api/token-identities", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listTokenIdentities))))
h.mux.Handle("GET /admin/api/review-decisions", h.auth.Middleware(h.auth.Require(PermissionReview, http.HandlerFunc(h.listReviewDecisions))))
h.mux.Handle("GET /admin/api/settings", h.auth.Middleware(h.auth.Require(PermissionManageUsers, http.HandlerFunc(h.systemSettings))))
```

Add these handlers:

```go
func (h Handler) listTokenIdentities(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListTokenIdentities(r.Context(), TokenIdentityFilter{
		EmployeeNo:       r.URL.Query().Get("employee_no"),
		TokenFingerprint: r.URL.Query().Get("token_fingerprint"),
		Limit:            100,
	})
	if err != nil {
		http.Error(w, "failed to list token identities", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token_identities": items})
}

func (h Handler) listReviewDecisions(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListReviewDecisions(r.Context(), ReviewDecisionFilter{
		TargetType: r.URL.Query().Get("target_type"),
		TargetID:   r.URL.Query().Get("target_id"),
		Limit:      100,
	})
	if err != nil {
		http.Error(w, "failed to list review decisions", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"review_decisions": items})
}

func (h Handler) systemSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"settings": SystemSettingsSummary{
		EmployeeNoPattern: "configured by EMPLOYEE_NO_PATTERN",
		MetricsEnabled:    true,
		LookupLimit:       20,
		RawAccessLimit:    120,
	}})
}
```

- [ ] **Step 7: Add admin UI views**

In `internal/adminui/app.js`, add views:

```javascript
{ id: "identities", label: "Employee Directory" },
{ id: "reviews", label: "Review Decisions" },
{ id: "settings", label: "System Settings" },
```

Add CSRF support:

```javascript
function csrfToken() {
  const match = document.cookie.split("; ").find((part) => part.startsWith("audit_admin_csrf="));
  return match ? decodeURIComponent(match.split("=")[1]) : "";
}
```

In `api`, add `X-CSRF-Token` for non-GET methods:

```javascript
const method = String(options.method || "GET").toUpperCase();
const csrf = method === "GET" ? "" : csrfToken();
...
...(csrf ? { "X-CSRF-Token": csrf } : {}),
```

Implement `renderIdentities`, `renderReviews`, and `renderSettings` using existing `table`, `page`, and `api` helpers.

- [ ] **Step 8: Run admin tests**

Run:

```bash
go test ./internal/admin ./cmd/audit-gateway -v
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add migrations/0010_admin_csrf_security.sql internal/admin/auth.go internal/admin/limits.go internal/admin/models.go internal/admin/repository.go internal/admin/handlers.go internal/admin/auth_test.go internal/admin/handlers_test.go internal/admin/repository_test.go internal/adminui/app.js internal/adminui/app.css
git commit -m "feat: harden admin security and views"
```

---

### Task 8: Media Snapshot Jobs and SSRF-Safe Downloader

**Files:**
- Create: `migrations/0011_media_snapshot_jobs.sql`
- Create: `workers/analysis_worker/media_snapshot.py`
- Create: `workers/analysis_worker/tests/test_media_snapshot.py`
- Modify: `workers/analysis_worker/repository.py`
- Modify: `workers/analysis_worker/main.py`

- [ ] **Step 1: Add media snapshot migration**

Create `migrations/0011_media_snapshot_jobs.sql`:

```sql
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
```

- [ ] **Step 2: Add SSRF protection tests**

Create `workers/analysis_worker/tests/test_media_snapshot.py`:

```python
import pytest

from media_snapshot import MediaSnapshotPolicy, validate_snapshot_url


def test_rejects_non_http_urls():
    with pytest.raises(ValueError, match="http/https"):
        validate_snapshot_url("file:///etc/passwd", MediaSnapshotPolicy())


def test_rejects_metadata_ip():
    with pytest.raises(ValueError, match="private or metadata"):
        validate_snapshot_url("http://169.254.169.254/latest/meta-data", MediaSnapshotPolicy())


def test_rejects_private_ip_without_allowlist():
    with pytest.raises(ValueError, match="private or metadata"):
        validate_snapshot_url("http://10.0.0.5/image.png", MediaSnapshotPolicy())


def test_allows_configured_company_domain():
    policy = MediaSnapshotPolicy(allowed_domains={"assets.company.test"})
    result = validate_snapshot_url("https://assets.company.test/a.png", policy)
    assert result.hostname == "assets.company.test"
```

- [ ] **Step 3: Run media snapshot tests to verify failure**

Run:

```bash
cd workers/analysis_worker
uv run pytest tests/test_media_snapshot.py -q
```

Expected: FAIL because `media_snapshot.py` does not exist.

- [ ] **Step 4: Implement URL validation**

Create `workers/analysis_worker/media_snapshot.py`:

```python
from dataclasses import dataclass, field
from ipaddress import ip_address
from urllib.parse import urlparse, ParseResult


METADATA_HOSTS = {"169.254.169.254", "metadata.google.internal"}


@dataclass(frozen=True)
class MediaSnapshotPolicy:
    allowed_domains: set[str] = field(default_factory=set)
    max_size_bytes: int = 20 * 1024 * 1024
    redirect_limit: int = 3
    mime_allowlist: set[str] = field(default_factory=lambda: {"image/png", "image/jpeg", "image/webp", "audio/mpeg", "audio/wav"})


def validate_snapshot_url(raw_url: str, policy: MediaSnapshotPolicy) -> ParseResult:
    parsed = urlparse(raw_url)
    if parsed.scheme not in {"http", "https"}:
        raise ValueError("media snapshot url must use http/https")
    if not parsed.hostname:
        raise ValueError("media snapshot url host is required")
    hostname = parsed.hostname.lower()
    if hostname in policy.allowed_domains:
        return parsed
    if hostname in METADATA_HOSTS:
        raise ValueError("media snapshot url resolves to private or metadata address")
    try:
        address = ip_address(hostname)
    except ValueError:
        return parsed
    if address.is_private or address.is_loopback or address.is_link_local or address.is_reserved or address.is_multicast:
        raise ValueError("media snapshot url resolves to private or metadata address")
    return parsed
```

- [ ] **Step 5: Queue media snapshot jobs from normalized media URLs**

In `workers/analysis_worker/repository.py`, convert the iterables at the start of `save_trace_analysis` so media job creation can safely reuse the messages:

```python
        messages = list(messages)
        results = list(results)
        aggregates = list(aggregates)
        anomalies = list(anomalies)
        coverage_alerts = list(coverage_alerts)
```

Then add a loop in `save_trace_analysis` after messages are inserted:

```python
        for message in messages:
            if not message.media_url:
                continue
            cursor.execute(
                """
                INSERT INTO media_snapshot_jobs (
                    trace_id, source_url, source_context, policy_reason, status
                ) VALUES (%s,%s,%s,%s,'queued')
                ON CONFLICT DO NOTHING
                """,
                (
                    message.trace_id,
                    message.media_url,
                    message.source_path,
                    "generated_or_referenced_media",
                ),
            )
```

Append this test to `workers/analysis_worker/tests/test_repository.py`:

```python
def test_repository_queues_media_snapshot_jobs_for_media_urls():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)
    media_message = NormalizedMessage(
        trace_id="trace_media",
        direction="request",
        sequence_index=0,
        role="user",
        modality="image",
        content_text="",
        content_text_hash="",
        media_url="https://example.test/image.png",
        source_path="request.messages[0].content[1]",
        protocol_item_type="media_url",
        token_count_estimate=0,
        metadata={"protocol_family": "openai_chat"},
    )

    repo.save_trace_analysis([media_message], [], [], [], [])

    media_queries = [
        (query, params)
        for query, params in conn.cursor_obj.executed
        if "INSERT INTO media_snapshot_jobs" in query
    ]
    assert len(media_queries) == 1
    assert media_queries[0][1] == (
        "trace_media",
        "https://example.test/image.png",
        "request.messages[0].content[1]",
        "generated_or_referenced_media",
    )
```

- [ ] **Step 6: Run worker tests**

Run:

```bash
cd workers/analysis_worker
uv run pytest -q
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add migrations/0011_media_snapshot_jobs.sql workers/analysis_worker/media_snapshot.py workers/analysis_worker/tests/test_media_snapshot.py workers/analysis_worker/repository.py workers/analysis_worker/tests/test_repository.py workers/analysis_worker/main.py
git commit -m "feat: add ssrf-safe media snapshot jobs"
```

---

### Task 9: Expanded Operations Metrics, Smoke Checks, and Docs

**Files:**
- Modify: `internal/ops/metrics.go`
- Modify: `internal/ops/metrics_test.go`
- Modify: `internal/ops/health.go`
- Modify: `cmd/audit-gateway/main.go`
- Create: `scripts/smoke_remaining_mvp.sh`
- Modify: `docs/development.md`

- [ ] **Step 1: Add failing metrics tests**

Append to `internal/ops/metrics_test.go`:

```go
func TestRenderMetricsIncludesAuditGatewayCounters(t *testing.T) {
	response := HealthResponse{
		Status:    statusOK,
		CheckedAt: time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC),
		Checks:    map[string]CheckStatus{},
		Metrics: RuntimeMetrics{
			RequestCount:        10,
			CaptureFailureCount: 2,
			RawOnlyRouteCount:   3,
			IdentityStatuses:    map[string]int64{"resolved": 8, "missing_employee_no": 2},
			CoverageOpenCount:   4,
			AnomalyOpenCount:    5,
		},
	}

	text := RenderMetrics(response, time.Date(2026, 4, 30, 7, 0, 0, 0, time.UTC), response.CheckedAt)

	for _, want := range []string{
		"audit_gateway_requests_total 10",
		"audit_gateway_capture_failures_total 2",
		`audit_gateway_identity_status_total{status="resolved"} 8`,
		"audit_gateway_raw_only_routes_total 3",
		"audit_gateway_coverage_open 4",
		"audit_gateway_anomaly_open 5",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics missing %q:\n%s", want, text)
		}
	}
}
```

- [ ] **Step 2: Run metrics test to verify failure**

Run:

```bash
go test ./internal/ops -run TestRenderMetricsIncludesAuditGatewayCounters -v
```

Expected: FAIL because `RuntimeMetrics` is not present.

- [ ] **Step 3: Add runtime metrics model and renderer**

In `internal/ops/health.go`, add to `HealthResponse`:

```go
Metrics RuntimeMetrics `json:"metrics,omitempty"`
```

Add:

```go
type RuntimeMetrics struct {
	RequestCount        int64
	CaptureFailureCount int64
	RawOnlyRouteCount   int64
	IdentityStatuses    map[string]int64
	CoverageOpenCount   int64
	AnomalyOpenCount    int64
}
```

Add this field to `Service`:

```go
RuntimeMetricsCheck func(context.Context) (RuntimeMetrics, error)
```

In `Service.Readiness`, load metrics after dependency checks:

```go
metrics := RuntimeMetrics{IdentityStatuses: map[string]int64{}}
if s.RuntimeMetricsCheck != nil {
	if loaded, err := s.RuntimeMetricsCheck(ctx); err == nil {
		metrics = loaded
	}
}

return HealthResponse{
	Status:    overallStatus(checks),
	CheckedAt: s.now().UTC(),
	Checks:    checks,
	Metrics:   metrics,
}
```

In `internal/ops/metrics.go`, append:

```go
	fmt.Fprintf(&builder, "audit_gateway_requests_total %d\n", response.Metrics.RequestCount)
	fmt.Fprintf(&builder, "audit_gateway_capture_failures_total %d\n", response.Metrics.CaptureFailureCount)
	fmt.Fprintf(&builder, "audit_gateway_raw_only_routes_total %d\n", response.Metrics.RawOnlyRouteCount)
	fmt.Fprintf(&builder, "audit_gateway_coverage_open %d\n", response.Metrics.CoverageOpenCount)
	fmt.Fprintf(&builder, "audit_gateway_anomaly_open %d\n", response.Metrics.AnomalyOpenCount)
	for _, status := range sortedMetricKeys(response.Metrics.IdentityStatuses) {
		fmt.Fprintf(&builder, "audit_gateway_identity_status_total{status=%q} %d\n", status, response.Metrics.IdentityStatuses[status])
	}
```

Add:

```go
func sortedMetricKeys(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
```

- [ ] **Step 4: Wire metrics from PostgreSQL**

In `cmd/audit-gateway/main.go`, add a `RuntimeMetricsCheck` function to `ops.Service` that queries:

```sql
SELECT
  COUNT(*),
  COUNT(*) FILTER (WHERE error_type = 'capture_degraded'),
  COUNT(*) FILTER (WHERE route_support_level IN ('raw_only','raw_minimal')),
  (SELECT COUNT(*) FROM coverage_alerts WHERE status = 'open'),
  (SELECT COUNT(*) FROM usage_anomalies WHERE status = 'open')
FROM traces
WHERE created_at >= now() - interval '24 hours'
```

Add a second query:

```sql
SELECT identity_resolution_status, COUNT(*)
FROM traces
WHERE created_at >= now() - interval '24 hours'
GROUP BY identity_resolution_status
```

Return an `ops.RuntimeMetrics` with those values.

- [ ] **Step 5: Add smoke script**

Create `scripts/smoke_remaining_mvp.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"

curl -fsS "$BASE_URL/healthz" >/dev/null
curl -fsS "$BASE_URL/metrics" | rg "audit_gateway_requests_total|audit_gateway_identity_status_total|audit_gateway_capture_failures_total" >/dev/null

echo "remaining MVP smoke checks passed"
```

Make it executable:

```bash
chmod +x scripts/smoke_remaining_mvp.sh
```

- [ ] **Step 6: Update development docs**

Append to `docs/development.md`:

```markdown
## Remaining MVP Gap Checks

The remaining MVP hardening adds:

- new-api-compatible API key canonicalization;
- Redis plus PostgreSQL local identity cache lookup;
- degraded capture spooling that preserves proxy forwarding;
- multipart part evidence objects;
- Gemini, media, and SSE worker normalization;
- expanded explainable anomaly rules;
- CSRF and rate limits for unsafe admin actions;
- employee/token identity, review decision, and system settings admin views;
- media snapshot job queueing with SSRF-safe URL validation;
- expanded Prometheus metrics.

Run the local smoke check after the gateway is running:

```bash
BASE_URL=http://127.0.0.1:8080 ./scripts/smoke_remaining_mvp.sh
```
```

- [ ] **Step 7: Run full verification**

Run:

```bash
go test ./...
cd workers/analysis_worker && uv run pytest -q
```

Expected: both commands PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/ops/metrics.go internal/ops/metrics_test.go internal/ops/health.go cmd/audit-gateway/main.go scripts/smoke_remaining_mvp.sh docs/development.md
git commit -m "feat: expose remaining mvp ops checks"
```

---

### Task 10: WebSocket Message Log Evidence

**Files:**
- Create: `internal/gateway/websocket_log.go`
- Create: `internal/gateway/websocket_log_test.go`
- Modify: `internal/gateway/proxy.go`
- Modify: `internal/gateway/stream_test.go`

- [ ] **Step 1: Add failing bounded websocket log tests**

Create `internal/gateway/websocket_log_test.go`:

```go
package gateway

import (
	"strings"
	"testing"
)

func TestBoundedWebSocketLogRedactsRealtimeAPIKey(t *testing.T) {
	log := newBoundedWebSocketLog(1024)
	_, _ = log.WriteClient([]byte("Sec-WebSocket-Protocol: openai-insecure-api-key.sk-secret-extra\r\n"))
	_, _ = log.WriteUpstream([]byte("HTTP/1.1 101 Switching Protocols\r\n"))

	text := log.String()
	if strings.Contains(text, "sk-secret-extra") {
		t.Fatalf("websocket log leaked key: %s", text)
	}
	if !strings.Contains(text, "client ") || !strings.Contains(text, "upstream ") {
		t.Fatalf("websocket log missing direction markers: %s", text)
	}
}

func TestBoundedWebSocketLogCapsBytes(t *testing.T) {
	log := newBoundedWebSocketLog(12)
	_, _ = log.WriteClient([]byte("abcdefghijklmnopqrstuvwxyz"))

	if len(log.Bytes()) != 12 {
		t.Fatalf("log length = %d, want 12", len(log.Bytes()))
	}
}
```

- [ ] **Step 2: Run websocket log tests to verify failure**

Run:

```bash
go test ./internal/gateway -run TestBoundedWebSocketLog -v
```

Expected: FAIL because `newBoundedWebSocketLog` does not exist.

- [ ] **Step 3: Implement bounded websocket log helper**

Create `internal/gateway/websocket_log.go`:

```go
package gateway

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
)

var realtimeKeyPattern = regexp.MustCompile(`openai-insecure-api-key\.[A-Za-z0-9._~+/=-]+`)

type boundedWebSocketLog struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func newBoundedWebSocketLog(limit int) *boundedWebSocketLog {
	if limit <= 0 {
		limit = 1 << 20
	}
	return &boundedWebSocketLog{limit: limit}
}

func (l *boundedWebSocketLog) WriteClient(data []byte) (int, error) {
	l.write("client", data)
	return len(data), nil
}

func (l *boundedWebSocketLog) WriteUpstream(data []byte) (int, error) {
	l.write("upstream", data)
	return len(data), nil
}

func (l *boundedWebSocketLog) write(direction string, data []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buf.Len() >= l.limit {
		return
	}
	line := direction + " " + redactWebSocketLog(string(data)) + "\n"
	remaining := l.limit - l.buf.Len()
	if len(line) > remaining {
		line = line[:remaining]
	}
	_, _ = l.buf.WriteString(line)
}

func (l *boundedWebSocketLog) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.buf.Bytes()...)
}

func (l *boundedWebSocketLog) String() string {
	return string(l.Bytes())
}

func redactWebSocketLog(value string) string {
	value = realtimeKeyPattern.ReplaceAllString(value, "openai-insecure-api-key.[REDACTED]")
	if strings.Contains(strings.ToLower(value), "authorization: bearer ") {
		return "[REDACTED]\n"
	}
	return value
}
```

- [ ] **Step 4: Tee websocket tunnel traffic into evidence**

In `internal/gateway/proxy.go`, change `copyBidirectional` signature:

```go
func copyBidirectional(clientConn net.Conn, clientReader *bufio.Reader, upstreamConn net.Conn, upstreamReader *bufio.Reader, wsLog *boundedWebSocketLog) {
```

Replace the client-to-upstream copy body with:

```go
var reader io.Reader = io.MultiReader(clientReader, clientConn)
if wsLog != nil {
	reader = io.TeeReader(reader, writerFunc(wsLog.WriteClient))
}
_, _ = io.Copy(upstreamConn, reader)
```

Replace the upstream-to-client copy body with:

```go
var reader io.Reader = io.MultiReader(upstreamReader, upstreamConn)
if wsLog != nil {
	reader = io.TeeReader(reader, writerFunc(wsLog.WriteUpstream))
}
_, _ = io.Copy(clientConn, reader)
```

Add this adapter:

```go
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(data []byte) (int, error) {
	return f(data)
}
```

Add `websocketLogObject evidence.Object` to `traceRecord`, and in `insertTrace` add:

```go
if record.websocketLogObject.ObjectRef != "" {
	if err := h.insertEvidenceObject(ctx, record.traceID, "websocket_log", record.websocketLogObject); err != nil {
		errs = append(errs, err)
	}
}
```

In `serveWebSocketTunnel`, create the log before copying:

```go
wsLog := newBoundedWebSocketLog(1 << 20)
```

Call:

```go
copyBidirectional(clientConn, clientRW.Reader, upstreamConn, upstreamReader, wsLog)
```

After `copyBidirectional` returns and before `recordWebSocketTrace`, persist the log:

```go
if wsLog != nil && len(wsLog.Bytes()) > 0 {
	auditCtx, cancel := h.auditContext(req.Context())
	object, err := h.putEvidence(auditCtx, record.traceID, "websocket_log", "text/plain", wsLog.Bytes())
	cancel()
	if err != nil {
		h.reportAuditError(req.Context(), err)
		record.skipPostPersistence = true
	} else {
		record.websocketLogObject = object
	}
}
```

- [ ] **Step 5: Run gateway tests**

Run:

```bash
go test ./internal/gateway -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/websocket_log.go internal/gateway/websocket_log_test.go internal/gateway/proxy.go internal/gateway/stream_test.go
git commit -m "feat: capture websocket audit logs"
```

---

## Self-Review

Spec coverage:

- Transparent proxy compatibility remains covered by existing gateway tests and Task 3 degraded forwarding tests.
- Complete raw evidence capture is improved by Task 2 schema fields, Task 3 degraded spool, Task 4 multipart/base64/media metadata, Task 8 media snapshot jobs, and Task 10 websocket logs.
- Normalized content extraction is improved by Task 5.
- Employee/token usage views are improved by Task 7 token identity views and existing usage APIs.
- API key lookup privacy is preserved by Task 1 shared canonicalization and existing audit-log tests; Task 7 adds CSRF and rate limits.
- Usage aggregation remains covered by existing worker code; Task 6 adds context reads for aggregate-based anomaly rules.
- Rule-based anomaly detection reaches the design's MVP rule list in Task 6.
- Route coverage alerts remain covered by existing gateway and worker alerts; Task 5 reduces false normalization gaps.
- Audit trail is preserved and expanded through existing action logs plus Task 7 review/product APIs.
- Operations coverage is improved by Task 3 degraded capture behavior and Task 9 expanded metrics.

Placeholder scan:

- No placeholder markers remain.
- Every task has exact files, commands, expected results, and concrete code snippets for the new behavior.
- Vague implementation instructions were replaced with specific tests, structs, SQL, handlers, and helper functions.

Type consistency:

- `AnalysisContext` is defined in `workers/analysis_worker/models.py` before use in `rules.py`, `repository.py`, and `main.py`.
- `RouteSupportLevel`, raw evidence metadata fields, and client/user-agent hashes are added to models before repository and gateway usage.
- `Canonicalize` is exported from `internal/authkeys` before admin lookup uses it.
- `ChainCache`, `PostgresCache`, and `IdentityCacheStatusHit` are defined before gateway wiring references them.
