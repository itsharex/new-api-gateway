# Operational Health and Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the first operational hardening slice: health/readiness endpoints, Prometheus-style gateway metrics, Redis queue lag reporting, PostgreSQL/Redis/evidence readiness checks, and Python worker heartbeat storage.

**Architecture:** Keep operational endpoints inside the existing Go gateway process and mount them before admin/proxy routing. The Go `internal/ops` package owns health checks and metrics rendering; the Python worker writes heartbeat rows to PostgreSQL after each Redis poll or processed job, and readiness uses those rows to report stale analysis workers.

**Tech Stack:** Go `net/http`, PostgreSQL via existing `pgx`/`pgxpool`, Redis via existing `go-redis/v9`, Python 3.11 with `psycopg`, existing Docker Compose Postgres/Redis services, shell smoke checks.

---

## Scope Check

The approved design's phase 7 includes operational hardening, retention, metrics, backup, and backfill/reanalysis. Those are independent subsystems, so this plan covers only the first deployable operational slice:

- liveness endpoint for process health;
- readiness endpoint for PostgreSQL, Redis, evidence storage, worker heartbeat freshness, and Redis queue lag;
- Prometheus text metrics for gateway uptime and dependency health;
- worker heartbeat persistence from the Python analysis worker;
- local smoke test documentation.

This plan intentionally does not implement retention deletion, raw evidence lifecycle policies, backup automation, replay/backfill/reanalysis jobs, object storage migration, or alert notification delivery. Those should be separate follow-on plans after the service exposes reliable health and metrics.

## File Structure

- Create: `migrations/0007_operational_health_metrics.sql` defines `worker_heartbeats` for worker freshness checks.
- Modify: `internal/config/config.go` loads operational timeout, heartbeat, queue lag, and metrics settings.
- Modify: `internal/config/config_test.go` tests defaults and validation for the new settings.
- Create: `internal/ops/health.go` implements liveness/readiness models, dependency checks, and HTTP handlers.
- Create: `internal/ops/health_test.go` tests status JSON, stale worker detection, degraded queue lag, and HTTP status codes.
- Create: `internal/ops/metrics.go` renders Prometheus text metrics from the same health snapshot.
- Create: `internal/ops/metrics_test.go` tests metric names, values, and content type.
- Modify: `cmd/audit-gateway/main.go` mounts `/healthz`, `/readyz`, and `/metrics` before admin UI/API and proxy routes.
- Modify: `cmd/audit-gateway/main_test.go` verifies operational routes do not fall through to admin or proxy.
- Create: `workers/analysis_worker/heartbeat.py` persists worker heartbeat rows.
- Create: `workers/analysis_worker/tests/test_heartbeat.py` tests heartbeat upsert SQL and metadata JSON.
- Modify: `workers/analysis_worker/main.py` records heartbeat status for processed, idle, and error Redis poll outcomes.
- Modify: `workers/analysis_worker/tests/test_pipeline.py` adds an in-process Redis poll heartbeat test.
- Create: `scripts/smoke_ops_health.sh` checks `/healthz`, `/readyz`, and `/metrics` against a local gateway.
- Modify: `docs/development.md` documents operational environment variables, endpoints, metrics, and smoke usage.

---

### Task 1: Worker Heartbeat Schema

**Files:**
- Create: `migrations/0007_operational_health_metrics.sql`

- [ ] **Step 1: Write the migration**

Create `migrations/0007_operational_health_metrics.sql`:

```sql
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
```

- [ ] **Step 2: Verify migration text**

Run:

```bash
rg -n "CREATE TABLE IF NOT EXISTS worker_heartbeats|idx_worker_heartbeats_kind_seen|idx_worker_heartbeats_status_seen" migrations/0007_operational_health_metrics.sql
```

Expected: three matches: the table and both indexes.

- [ ] **Step 3: Verify all migrations still apply**

Run:

```bash
E2E_DB=audit_gateway_plan_0007 ./scripts/e2e_worker_anomaly_coverage.sh
```

Expected: PASS. The script recreates a dedicated database and applies every migration including `0007_operational_health_metrics.sql`.

- [ ] **Step 4: Commit**

```bash
git add migrations/0007_operational_health_metrics.sql
git commit -m "feat: add worker heartbeat schema"
```

---

### Task 2: Operational Configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Add failing config tests**

Append these tests to `internal/config/config_test.go`:

```go
func TestLoadFromEnvLoadsOperationalDefaults(t *testing.T) {
	setValidEnv(t)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if cfg.OpsCheckTimeout != 2*time.Second {
		t.Fatalf("OpsCheckTimeout = %v, want 2s", cfg.OpsCheckTimeout)
	}
	if cfg.OpsWorkerHeartbeatMaxAge != 5*time.Minute {
		t.Fatalf("OpsWorkerHeartbeatMaxAge = %v, want 5m", cfg.OpsWorkerHeartbeatMaxAge)
	}
	if cfg.OpsQueueLagWarnThreshold != 1000 {
		t.Fatalf("OpsQueueLagWarnThreshold = %d, want 1000", cfg.OpsQueueLagWarnThreshold)
	}
	if !cfg.OpsMetricsEnabled {
		t.Fatal("OpsMetricsEnabled = false, want true")
	}
}

func TestLoadFromEnvLoadsOperationalOverrides(t *testing.T) {
	setValidEnv(t)
	t.Setenv("OPS_CHECK_TIMEOUT", "750ms")
	t.Setenv("OPS_WORKER_HEARTBEAT_MAX_AGE", "90s")
	t.Setenv("OPS_QUEUE_LAG_WARN_THRESHOLD", "25")
	t.Setenv("OPS_METRICS_ENABLED", "false")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if cfg.OpsCheckTimeout != 750*time.Millisecond {
		t.Fatalf("OpsCheckTimeout = %v", cfg.OpsCheckTimeout)
	}
	if cfg.OpsWorkerHeartbeatMaxAge != 90*time.Second {
		t.Fatalf("OpsWorkerHeartbeatMaxAge = %v", cfg.OpsWorkerHeartbeatMaxAge)
	}
	if cfg.OpsQueueLagWarnThreshold != 25 {
		t.Fatalf("OpsQueueLagWarnThreshold = %d", cfg.OpsQueueLagWarnThreshold)
	}
	if cfg.OpsMetricsEnabled {
		t.Fatal("OpsMetricsEnabled = true, want false")
	}
}

func TestLoadFromEnvRejectsInvalidOperationalSettings(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value string
		want  string
	}{
		{key: "OPS_CHECK_TIMEOUT", value: "0s", want: "OPS_CHECK_TIMEOUT"},
		{key: "OPS_WORKER_HEARTBEAT_MAX_AGE", value: "-1s", want: "OPS_WORKER_HEARTBEAT_MAX_AGE"},
		{key: "OPS_QUEUE_LAG_WARN_THRESHOLD", value: "-1", want: "OPS_QUEUE_LAG_WARN_THRESHOLD"},
		{key: "OPS_METRICS_ENABLED", value: "sometimes", want: "OPS_METRICS_ENABLED"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv(tc.key, tc.value)

			_, err := LoadFromEnv()
			assertErrorContains(t, err, tc.want)
		})
	}
}
```

- [ ] **Step 2: Add the missing import**

Modify the import block in `internal/config/config_test.go` to include `time`:

```go
import (
	"os"
	"strings"
	"testing"
	"time"
)
```

- [ ] **Step 3: Run config tests and verify failure**

Run:

```bash
go test ./internal/config -run 'TestLoadFromEnvLoadsOperational|TestLoadFromEnvRejectsInvalidOperational' -count=1
```

Expected: FAIL with missing fields such as `OpsCheckTimeout`, `OpsWorkerHeartbeatMaxAge`, `OpsQueueLagWarnThreshold`, and `OpsMetricsEnabled`.

- [ ] **Step 4: Add config fields**

Add these fields to `Config` in `internal/config/config.go` after `AdminCookieSecure`:

```go
	OpsCheckTimeout            time.Duration
	OpsWorkerHeartbeatMaxAge   time.Duration
	OpsQueueLagWarnThreshold   int64
	OpsMetricsEnabled          bool
```

Add `time` to the import block:

```go
	"time"
```

- [ ] **Step 5: Add parsing inside `LoadFromEnv`**

Add this block after `adminCookieSecure` is parsed:

```go
	opsCheckTimeout, err := getenvDurationDefault("OPS_CHECK_TIMEOUT", 2*time.Second)
	if err != nil {
		return Config{}, err
	}
	opsWorkerHeartbeatMaxAge, err := getenvDurationDefault("OPS_WORKER_HEARTBEAT_MAX_AGE", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	opsQueueLagWarnThreshold, err := getenvInt64Default("OPS_QUEUE_LAG_WARN_THRESHOLD", 1000)
	if err != nil {
		return Config{}, err
	}
	opsMetricsEnabledRaw, err := getenvDefault("OPS_METRICS_ENABLED", "true")
	if err != nil {
		return Config{}, err
	}
	opsMetricsEnabled, err := strconv.ParseBool(opsMetricsEnabledRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid OPS_METRICS_ENABLED: must be true or false")
	}
```

Set the fields in the `cfg := Config{...}` literal:

```go
		OpsCheckTimeout:          opsCheckTimeout,
		OpsWorkerHeartbeatMaxAge: opsWorkerHeartbeatMaxAge,
		OpsQueueLagWarnThreshold: opsQueueLagWarnThreshold,
		OpsMetricsEnabled:        opsMetricsEnabled,
```

- [ ] **Step 6: Add config helper functions**

Add these helpers after `getenvDefault`:

```go
func getenvDurationDefault(key string, fallback time.Duration) (time.Duration, error) {
	raw, err := getenvDefault(key, fallback.String())
	if err != nil {
		return 0, err
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func getenvInt64Default(key string, fallback int64) (int64, error) {
	raw, err := getenvDefault(key, strconv.FormatInt(fallback, 10))
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return value, nil
}
```

- [ ] **Step 7: Run config tests and verify pass**

Run:

```bash
go test ./internal/config -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add operational config"
```

---

### Task 3: Go Health and Metrics Package

**Files:**
- Create: `internal/ops/health.go`
- Create: `internal/ops/health_test.go`
- Create: `internal/ops/metrics.go`
- Create: `internal/ops/metrics_test.go`

- [ ] **Step 1: Write failing health tests**

Create `internal/ops/health_test.go`:

```go
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServiceReadinessReportsHealthyDependencies(t *testing.T) {
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	service := Service{
		Now: nowFunc(now),
		PostgresCheck: func(context.Context) error { return nil },
		RedisCheck: func(context.Context) error { return nil },
		EvidenceCheck: func(context.Context) error { return nil },
		WorkerHeartbeatCheck: func(context.Context) (WorkerHeartbeatStatus, error) {
			return WorkerHeartbeatStatus{LastSeenAt: now.Add(-time.Minute), MaxAge: 5 * time.Minute, WorkerCount: 1}, nil
		},
		QueueLagCheck: func(context.Context) (QueueLagStatus, error) {
			return QueueLagStatus{QueueName: "analysis_jobs", Depth: 10, WarnThreshold: 1000}, nil
		},
	}

	response := service.Readiness(context.Background())

	if response.Status != "ok" {
		t.Fatalf("Status = %q, want ok: %#v", response.Status, response)
	}
	if response.Checks["postgres"].Status != "ok" {
		t.Fatalf("postgres check = %#v", response.Checks["postgres"])
	}
	if response.Checks["worker_heartbeat"].Status != "ok" {
		t.Fatalf("worker check = %#v", response.Checks["worker_heartbeat"])
	}
}

func TestServiceReadinessReportsDegradedQueueLag(t *testing.T) {
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	service := Service{
		Now: nowFunc(now),
		PostgresCheck: func(context.Context) error { return nil },
		RedisCheck: func(context.Context) error { return nil },
		EvidenceCheck: func(context.Context) error { return nil },
		WorkerHeartbeatCheck: func(context.Context) (WorkerHeartbeatStatus, error) {
			return WorkerHeartbeatStatus{LastSeenAt: now.Add(-time.Minute), MaxAge: 5 * time.Minute, WorkerCount: 1}, nil
		},
		QueueLagCheck: func(context.Context) (QueueLagStatus, error) {
			return QueueLagStatus{QueueName: "analysis_jobs", Depth: 1201, WarnThreshold: 1000}, nil
		},
	}

	response := service.Readiness(context.Background())

	if response.Status != "degraded" {
		t.Fatalf("Status = %q, want degraded", response.Status)
	}
	if response.Checks["queue_lag"].Status != "degraded" {
		t.Fatalf("queue check = %#v", response.Checks["queue_lag"])
	}
}

func TestServiceReadinessReportsStaleWorkerHeartbeat(t *testing.T) {
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	service := Service{
		Now: nowFunc(now),
		PostgresCheck: func(context.Context) error { return nil },
		RedisCheck: func(context.Context) error { return nil },
		EvidenceCheck: func(context.Context) error { return nil },
		WorkerHeartbeatCheck: func(context.Context) (WorkerHeartbeatStatus, error) {
			return WorkerHeartbeatStatus{LastSeenAt: now.Add(-10 * time.Minute), MaxAge: 5 * time.Minute, WorkerCount: 1}, nil
		},
		QueueLagCheck: func(context.Context) (QueueLagStatus, error) {
			return QueueLagStatus{QueueName: "analysis_jobs", Depth: 0, WarnThreshold: 1000}, nil
		},
	}

	response := service.Readiness(context.Background())

	if response.Status != "degraded" {
		t.Fatalf("Status = %q, want degraded", response.Status)
	}
	if !strings.Contains(response.Checks["worker_heartbeat"].Message, "stale") {
		t.Fatalf("worker message = %q", response.Checks["worker_heartbeat"].Message)
	}
}

func TestHandlerReturnsStatusCodes(t *testing.T) {
	service := Service{
		Now: nowFunc(time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)),
		PostgresCheck: func(context.Context) error { return errors.New("postgres down") },
		RedisCheck: func(context.Context) error { return nil },
		EvidenceCheck: func(context.Context) error { return nil },
		WorkerHeartbeatCheck: func(context.Context) (WorkerHeartbeatStatus, error) {
			return WorkerHeartbeatStatus{}, nil
		},
		QueueLagCheck: func(context.Context) (QueueLagStatus, error) {
			return QueueLagStatus{QueueName: "analysis_jobs", Depth: 0, WarnThreshold: 1000}, nil
		},
	}
	handler := Handler(service, true)

	liveReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	liveRec := httptest.NewRecorder()
	handler.ServeHTTP(liveRec, liveReq)
	if liveRec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d", liveRec.Code)
	}

	readyReq := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readyRec := httptest.NewRecorder()
	handler.ServeHTTP(readyRec, readyReq)
	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz status = %d", readyRec.Code)
	}
	var response HealthResponse
	if err := json.Unmarshal(readyRec.Body.Bytes(), &response); err != nil {
		t.Fatalf("readiness JSON: %v", err)
	}
	if response.Checks["postgres"].Status != "down" {
		t.Fatalf("postgres check = %#v", response.Checks["postgres"])
	}
}

func nowFunc(now time.Time) func() time.Time {
	return func() time.Time { return now }
}
```

- [ ] **Step 2: Write failing metrics tests**

Create `internal/ops/metrics_test.go`:

```go
package ops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRenderMetricsIncludesDependencyAndQueueValues(t *testing.T) {
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	service := Service{
		StartedAt: now.Add(-2 * time.Minute),
		Now:       nowFunc(now),
		PostgresCheck: func(context.Context) error { return nil },
		RedisCheck: func(context.Context) error { return nil },
		EvidenceCheck: func(context.Context) error { return nil },
		WorkerHeartbeatCheck: func(context.Context) (WorkerHeartbeatStatus, error) {
			return WorkerHeartbeatStatus{LastSeenAt: now.Add(-time.Minute), MaxAge: 5 * time.Minute, WorkerCount: 2}, nil
		},
		QueueLagCheck: func(context.Context) (QueueLagStatus, error) {
			return QueueLagStatus{QueueName: "analysis_jobs", Depth: 42, WarnThreshold: 1000}, nil
		},
	}

	body := RenderMetrics(service.Readiness(context.Background()), service.StartedAt, now)

	for _, want := range []string{
		`audit_gateway_up 1`,
		`audit_gateway_uptime_seconds 120`,
		`audit_gateway_dependency_up{dependency="postgres"} 1`,
		`audit_gateway_worker_heartbeat_age_seconds 60`,
		`audit_gateway_worker_count 2`,
		`audit_gateway_analysis_queue_depth{queue="analysis_jobs"} 42`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q in:\n%s", want, body)
		}
	}
}

func TestMetricsEndpointCanBeDisabled(t *testing.T) {
	handler := Handler(Service{Now: time.Now}, false)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("/metrics status = %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 3: Run ops tests and verify failure**

Run:

```bash
go test ./internal/ops -count=1
```

Expected: FAIL because `internal/ops` implementation does not exist.

- [ ] **Step 4: Implement health service**

Create `internal/ops/health.go`:

```go
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type CheckStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type HealthResponse struct {
	Status    string                 `json:"status"`
	CheckedAt string                 `json:"checked_at"`
	Checks    map[string]CheckStatus `json:"checks"`
}

type WorkerHeartbeatStatus struct {
	LastSeenAt  time.Time
	MaxAge      time.Duration
	WorkerCount int64
}

type QueueLagStatus struct {
	QueueName     string
	Depth         int64
	WarnThreshold int64
}

type Service struct {
	StartedAt            time.Time
	Now                  func() time.Time
	PostgresCheck        func(context.Context) error
	RedisCheck           func(context.Context) error
	EvidenceCheck        func(context.Context) error
	WorkerHeartbeatCheck func(context.Context) (WorkerHeartbeatStatus, error)
	QueueLagCheck        func(context.Context) (QueueLagStatus, error)
}

func (s Service) Liveness() HealthResponse {
	now := s.now()
	return HealthResponse{
		Status:    "ok",
		CheckedAt: now.Format(time.RFC3339),
		Checks: map[string]CheckStatus{
			"process": {Status: "ok"},
		},
	}
}

func (s Service) Readiness(ctx context.Context) HealthResponse {
	now := s.now()
	checks := map[string]CheckStatus{
		"postgres": s.simpleCheck(ctx, s.PostgresCheck),
		"redis":    s.simpleCheck(ctx, s.RedisCheck),
		"evidence": s.simpleCheck(ctx, s.EvidenceCheck),
	}
	checks["worker_heartbeat"] = s.workerHeartbeatCheck(ctx, now)
	checks["queue_lag"] = s.queueLagCheck(ctx)

	status := "ok"
	for _, check := range checks {
		switch check.Status {
		case "down":
			status = "down"
		case "degraded":
			if status == "ok" {
				status = "degraded"
			}
		}
	}
	return HealthResponse{Status: status, CheckedAt: now.Format(time.RFC3339), Checks: checks}
}

func (s Service) simpleCheck(ctx context.Context, check func(context.Context) error) CheckStatus {
	if check == nil {
		return CheckStatus{Status: "degraded", Message: "check is not configured"}
	}
	if err := check(ctx); err != nil {
		return CheckStatus{Status: "down", Message: err.Error()}
	}
	return CheckStatus{Status: "ok"}
}

func (s Service) workerHeartbeatCheck(ctx context.Context, now time.Time) CheckStatus {
	if s.WorkerHeartbeatCheck == nil {
		return CheckStatus{Status: "degraded", Message: "worker heartbeat check is not configured"}
	}
	heartbeat, err := s.WorkerHeartbeatCheck(ctx)
	if err != nil {
		return CheckStatus{Status: "down", Message: err.Error()}
	}
	if heartbeat.WorkerCount == 0 {
		return CheckStatus{Status: "degraded", Message: "no analysis worker heartbeat rows found"}
	}
	age := now.Sub(heartbeat.LastSeenAt)
	if age > heartbeat.MaxAge {
		return CheckStatus{Status: "degraded", Message: fmt.Sprintf("analysis worker heartbeat is stale: age=%s max_age=%s", age.Round(time.Second), heartbeat.MaxAge)}
	}
	return CheckStatus{Status: "ok", Message: fmt.Sprintf("workers=%d age=%s", heartbeat.WorkerCount, age.Round(time.Second))}
}

func (s Service) queueLagCheck(ctx context.Context) CheckStatus {
	if s.QueueLagCheck == nil {
		return CheckStatus{Status: "degraded", Message: "queue lag check is not configured"}
	}
	lag, err := s.QueueLagCheck(ctx)
	if err != nil {
		return CheckStatus{Status: "down", Message: err.Error()}
	}
	if lag.Depth > lag.WarnThreshold {
		return CheckStatus{Status: "degraded", Message: fmt.Sprintf("queue=%s depth=%d threshold=%d", lag.QueueName, lag.Depth, lag.WarnThreshold)}
	}
	return CheckStatus{Status: "ok", Message: fmt.Sprintf("queue=%s depth=%d", lag.QueueName, lag.Depth)}
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func Handler(service Service, metricsEnabled bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeHealth(w, http.StatusOK, service.Liveness())
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		response := service.Readiness(r.Context())
		statusCode := http.StatusOK
		if response.Status == "down" {
			statusCode = http.StatusServiceUnavailable
		}
		writeHealth(w, statusCode, response)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if !metricsEnabled {
			http.NotFound(w, r)
			return
		}
		now := service.now()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(RenderMetrics(service.Readiness(r.Context()), service.StartedAt, now)))
	})
	return mux
}

func writeHealth(w http.ResponseWriter, statusCode int, response HealthResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(response)
}
```

- [ ] **Step 5: Implement metrics rendering**

Create `internal/ops/metrics.go`:

```go
package ops

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func RenderMetrics(response HealthResponse, startedAt time.Time, now time.Time) string {
	var builder strings.Builder
	up := 1
	if response.Status == "down" {
		up = 0
	}
	uptime := int64(0)
	if !startedAt.IsZero() {
		uptime = int64(now.Sub(startedAt).Seconds())
	}
	builder.WriteString("# HELP audit_gateway_up Gateway readiness status, 1 unless readiness is down.\n")
	builder.WriteString("# TYPE audit_gateway_up gauge\n")
	builder.WriteString(fmt.Sprintf("audit_gateway_up %d\n", up))
	builder.WriteString("# HELP audit_gateway_uptime_seconds Gateway process uptime in seconds.\n")
	builder.WriteString("# TYPE audit_gateway_uptime_seconds gauge\n")
	builder.WriteString(fmt.Sprintf("audit_gateway_uptime_seconds %d\n", uptime))

	keys := make([]string, 0, len(response.Checks))
	for key := range response.Checks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	builder.WriteString("# HELP audit_gateway_dependency_up Dependency health, 1 for ok or degraded, 0 for down.\n")
	builder.WriteString("# TYPE audit_gateway_dependency_up gauge\n")
	for _, key := range keys {
		checkUp := 1
		if response.Checks[key].Status == "down" {
			checkUp = 0
		}
		builder.WriteString(fmt.Sprintf("audit_gateway_dependency_up{dependency=%q} %d\n", key, checkUp))
	}

	workerAge := metricDurationFromMessage(response.Checks["worker_heartbeat"].Message, "age=")
	workerCount := metricIntFromMessage(response.Checks["worker_heartbeat"].Message, "workers=")
	queueDepth := metricIntFromMessage(response.Checks["queue_lag"].Message, "depth=")
	queueName := metricStringFromMessage(response.Checks["queue_lag"].Message, "queue=")
	if queueName == "" {
		queueName = "analysis_jobs"
	}
	builder.WriteString("# HELP audit_gateway_worker_heartbeat_age_seconds Age of freshest analysis worker heartbeat.\n")
	builder.WriteString("# TYPE audit_gateway_worker_heartbeat_age_seconds gauge\n")
	builder.WriteString(fmt.Sprintf("audit_gateway_worker_heartbeat_age_seconds %d\n", workerAge))
	builder.WriteString("# HELP audit_gateway_worker_count Count of known analysis workers.\n")
	builder.WriteString("# TYPE audit_gateway_worker_count gauge\n")
	builder.WriteString(fmt.Sprintf("audit_gateway_worker_count %d\n", workerCount))
	builder.WriteString("# HELP audit_gateway_analysis_queue_depth Redis analysis queue depth.\n")
	builder.WriteString("# TYPE audit_gateway_analysis_queue_depth gauge\n")
	builder.WriteString(fmt.Sprintf("audit_gateway_analysis_queue_depth{queue=%q} %d\n", queueName, queueDepth))
	return builder.String()
}

func metricDurationFromMessage(message string, key string) int64 {
	value := metricStringFromMessage(message, key)
	if value == "" {
		return 0
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return int64(duration.Seconds())
}

func metricIntFromMessage(message string, key string) int64 {
	value := metricStringFromMessage(message, key)
	if value == "" {
		return 0
	}
	var parsed int64
	_, _ = fmt.Sscanf(value, "%d", &parsed)
	return parsed
}

func metricStringFromMessage(message string, key string) string {
	start := strings.Index(message, key)
	if start < 0 {
		return ""
	}
	value := message[start+len(key):]
	if stop := strings.IndexByte(value, ' '); stop >= 0 {
		value = value[:stop]
	}
	return value
}
```

- [ ] **Step 6: Run ops tests and verify pass**

Run:

```bash
go test ./internal/ops -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/ops/health.go internal/ops/health_test.go internal/ops/metrics.go internal/ops/metrics_test.go
git commit -m "feat: add operational health metrics"
```

---

### Task 4: Wire Operational Endpoints Into the Gateway

**Files:**
- Modify: `cmd/audit-gateway/main.go`
- Modify: `cmd/audit-gateway/main_test.go`

- [ ] **Step 1: Add failing route tests**

Append this test to `cmd/audit-gateway/main_test.go`:

```go
func TestBuildHTTPHandlerServesOperationalRoutesBeforeAdminAndProxy(t *testing.T) {
	cfg := config.Config{
		NewAPIBaseURL:                 "https://new-api.example.test/base",
		AuditHMACSecret:               "0123456789abcdef0123456789abcdef",
		EvidenceStorageDir:            t.TempDir(),
		EmployeeNoPattern:             regexp.MustCompile(`^E[0-9]+$`),
		OpsCheckTimeout:               50 * time.Millisecond,
		OpsWorkerHeartbeatMaxAge:      5 * time.Minute,
		OpsQueueLagWarnThreshold:      1000,
		OpsMetricsEnabled:             true,
	}
	handler := buildHTTPHandler(cfg, nil, nil, log.New(io.Discard, "", 0))

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusBadGateway {
				t.Fatalf("%s fell through to proxy", path)
			}
			if rec.Code == http.StatusNotFound {
				t.Fatalf("%s was not mounted", path)
			}
		})
	}
}
```

- [ ] **Step 2: Run main tests and verify failure**

Run:

```bash
go test ./cmd/audit-gateway -run TestBuildHTTPHandlerServesOperationalRoutesBeforeAdminAndProxy -count=1
```

Expected: FAIL because operational routes are not mounted.

- [ ] **Step 3: Import ops**

Add this import to `cmd/audit-gateway/main.go`:

```go
	"github.com/your-company/new-api-gateway/internal/ops"
```

- [ ] **Step 4: Create operational service wiring**

Add these helpers near `buildHTTPHandler` in `cmd/audit-gateway/main.go`:

```go
func buildOpsHandler(cfg config.Config, pool *pgxpool.Pool, redisClient *redis.Client) http.Handler {
	service := ops.Service{
		StartedAt: time.Now().UTC(),
		Now:       time.Now,
		PostgresCheck: func(ctx context.Context) error {
			if pool == nil {
				return errors.New("postgres pool is nil")
			}
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			return pool.Ping(ctx)
		},
		RedisCheck: func(ctx context.Context) error {
			if redisClient == nil {
				return errors.New("redis client is nil")
			}
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			return redisClient.Ping(ctx).Err()
		},
		EvidenceCheck: func(ctx context.Context) error {
			store := evidence.NewFilesystemStore(cfg.EvidenceStorageDir)
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			object, err := store.Put(ctx, evidence.PutRequest{
				TraceID:     "ops_healthcheck",
				ObjectType:  "readiness",
				ContentType: "text/plain",
				Reader:      strings.NewReader("ok"),
			})
			if err != nil {
				return err
			}
			reader, err := store.Get(ctx, object.ObjectRef)
			if err != nil {
				return err
			}
			return reader.Close()
		},
		WorkerHeartbeatCheck: func(ctx context.Context) (ops.WorkerHeartbeatStatus, error) {
			if pool == nil {
				return ops.WorkerHeartbeatStatus{}, errors.New("postgres pool is nil")
			}
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			var status ops.WorkerHeartbeatStatus
			status.MaxAge = cfg.OpsWorkerHeartbeatMaxAge
			err := pool.QueryRow(ctx, `
SELECT COALESCE(MAX(last_seen_at), to_timestamp(0)), COUNT(*)
FROM worker_heartbeats
WHERE worker_kind = 'analysis'`).Scan(&status.LastSeenAt, &status.WorkerCount)
			return status, err
		},
		QueueLagCheck: func(ctx context.Context) (ops.QueueLagStatus, error) {
			status := ops.QueueLagStatus{QueueName: jobs.DefaultRedisListName, WarnThreshold: cfg.OpsQueueLagWarnThreshold}
			if redisClient == nil {
				return status, errors.New("redis client is nil")
			}
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			depth, err := redisClient.LLen(ctx, jobs.DefaultRedisListName).Result()
			status.Depth = depth
			return status, err
		},
	}
	return ops.Handler(service, cfg.OpsMetricsEnabled)
}

func isOpsPath(path string) bool {
	return path == "/healthz" || path == "/readyz" || path == "/metrics"
}
```

- [ ] **Step 5: Route operational paths before admin and proxy**

Modify `buildHTTPHandler` so the returned handler starts with:

```go
	opsHandler := buildOpsHandler(cfg, pool, redisClient)
```

In both returned `http.HandlerFunc` closures, add this branch before admin checks:

```go
			if isOpsPath(r.URL.Path) {
				opsHandler.ServeHTTP(w, r)
				return
			}
```

- [ ] **Step 6: Run main tests and verify pass**

Run:

```bash
go test ./cmd/audit-gateway -count=1
```

Expected: PASS.

- [ ] **Step 7: Run all Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/audit-gateway/main.go cmd/audit-gateway/main_test.go
git commit -m "feat: mount operational endpoints"
```

---

### Task 5: Python Worker Heartbeats

**Files:**
- Create: `workers/analysis_worker/heartbeat.py`
- Create: `workers/analysis_worker/tests/test_heartbeat.py`
- Modify: `workers/analysis_worker/main.py`
- Modify: `workers/analysis_worker/tests/test_pipeline.py`

- [ ] **Step 1: Write heartbeat repository tests**

Create `workers/analysis_worker/tests/test_heartbeat.py`:

```python
import json

from heartbeat import HeartbeatRepository


class RecordingCursor:
    def __init__(self):
        self.sql = ""
        self.args = None

    def execute(self, sql, args):
        self.sql = sql
        self.args = args


class RecordingConnection:
    def __init__(self):
        self.cursor_obj = RecordingCursor()
        self.committed = False

    def cursor(self):
        return self.cursor_obj

    def commit(self):
        self.committed = True


def test_heartbeat_repository_upserts_worker_row():
    connection = RecordingConnection()
    repo = HeartbeatRepository(connection)

    repo.record(
        worker_id="worker-1",
        worker_kind="analysis",
        status="processed",
        queue_name="analysis_jobs",
        processed_count=3,
        error_count=1,
        metadata={"trace_id": "trace_1"},
    )

    assert "INSERT INTO worker_heartbeats" in connection.cursor_obj.sql
    assert "ON CONFLICT (worker_id) DO UPDATE" in connection.cursor_obj.sql
    assert connection.cursor_obj.args[:6] == (
        "worker-1",
        "analysis",
        "processed",
        "analysis_jobs",
        3,
        1,
    )
    assert json.loads(connection.cursor_obj.args[6]) == {"trace_id": "trace_1"}
    assert connection.committed
```

- [ ] **Step 2: Run heartbeat tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_heartbeat.py -q
```

Expected: FAIL because `heartbeat.py` does not exist.

- [ ] **Step 3: Implement heartbeat repository**

Create `workers/analysis_worker/heartbeat.py`:

```python
import json


class HeartbeatRepository:
    def __init__(self, connection):
        self.connection = connection

    def record(
        self,
        worker_id: str,
        worker_kind: str,
        status: str,
        queue_name: str,
        processed_count: int,
        error_count: int,
        metadata: dict,
    ) -> None:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            INSERT INTO worker_heartbeats (
                worker_id, worker_kind, status, queue_name,
                processed_count, error_count, metadata_json,
                last_seen_at, updated_at
            ) VALUES (%s,%s,%s,%s,%s,%s,%s::jsonb,now(),now())
            ON CONFLICT (worker_id) DO UPDATE SET
                worker_kind = EXCLUDED.worker_kind,
                status = EXCLUDED.status,
                queue_name = EXCLUDED.queue_name,
                processed_count = EXCLUDED.processed_count,
                error_count = EXCLUDED.error_count,
                metadata_json = EXCLUDED.metadata_json,
                last_seen_at = now(),
                updated_at = now()
            """,
            (
                worker_id,
                worker_kind,
                status,
                queue_name,
                processed_count,
                error_count,
                json.dumps(metadata, sort_keys=True),
            ),
        )
        self.connection.commit()
```

- [ ] **Step 4: Run heartbeat tests and verify pass**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_heartbeat.py -q
```

Expected: PASS.

- [ ] **Step 5: Add worker heartbeat integration test**

Append this test to `workers/analysis_worker/tests/test_pipeline.py`:

```python
def test_process_redis_once_records_idle_heartbeat(monkeypatch):
    from main import process_redis_once

    class FakeRedisClient:
        def blpop(self, list_name, timeout):
            assert list_name == "analysis_jobs"
            assert timeout == 1
            return None

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            assert url == "redis://localhost:6379/0"
            assert decode_responses is True
            return FakeRedisClient()

    class FakeHeartbeatRepository:
        calls = []

        def __init__(self, connection):
            self.connection = connection

        def record(self, **kwargs):
            self.calls.append(kwargs)

    class FakeConnection:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    exit_code = process_redis_once(
        "redis://localhost:6379/0",
        "analysis_jobs",
        "/tmp/evidence-unused",
        "postgres://unused",
        1,
        connection_factory=lambda dsn: FakeConnection(),
    )

    assert exit_code == 0
    assert FakeHeartbeatRepository.calls[0]["worker_id"] == "worker-test"
    assert FakeHeartbeatRepository.calls[0]["status"] == "idle"
    assert FakeHeartbeatRepository.calls[0]["queue_name"] == "analysis_jobs"
```

- [ ] **Step 6: Run the new pipeline test and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_pipeline.py::test_process_redis_once_records_idle_heartbeat -q
```

Expected: FAIL because `process_redis_once` does not accept `connection_factory` and does not record heartbeats.

- [ ] **Step 7: Modify worker main imports**

In `workers/analysis_worker/main.py`, add:

```python
import socket
```

Add this import with the local modules:

```python
from heartbeat import HeartbeatRepository
```

- [ ] **Step 8: Add worker ID helper**

Add this function above `process_redis_once`:

```python
def worker_id() -> str:
    configured = os.environ.get("ANALYSIS_WORKER_ID", "").strip()
    if configured:
        return configured
    return f"{socket.gethostname()}:{os.getpid()}"
```

- [ ] **Step 9: Update `process_redis_once` signature and heartbeat logic**

Replace the `process_redis_once` function in `workers/analysis_worker/main.py` with:

```python
def process_redis_once(
    redis_url: str,
    list_name: str,
    evidence_root: str,
    postgres_dsn: str,
    timeout_seconds: int,
    connection_factory=psycopg.connect,
) -> int:
    client = redis.Redis.from_url(redis_url, decode_responses=True)
    item = client.blpop(list_name, timeout=timeout_seconds)
    with connection_factory(postgres_dsn) as connection:
        heartbeat = HeartbeatRepository(connection)
        if item is None:
            heartbeat.record(
                worker_id=worker_id(),
                worker_kind="analysis",
                status="idle",
                queue_name=list_name,
                processed_count=0,
                error_count=0,
                metadata={"redis_url": redis_url},
            )
            print(json.dumps({"worker_status": "idle", "list": list_name}, sort_keys=True))
            return 0
        _, payload = item
        try:
            result = process_job_line(
                payload,
                FileEvidenceStore(evidence_root),
                PostgresAnalysisRepository(connection),
                PostgresContextRepository(connection),
            )
        except Exception as exc:
            heartbeat.record(
                worker_id=worker_id(),
                worker_kind="analysis",
                status="error",
                queue_name=list_name,
                processed_count=0,
                error_count=1,
                metadata={"error": str(exc)},
            )
            raise
        heartbeat.record(
            worker_id=worker_id(),
            worker_kind="analysis",
            status="processed",
            queue_name=list_name,
            processed_count=1,
            error_count=0,
            metadata={"trace_id": result.get("accepted_trace_id", "")},
        )
    print(json.dumps(result, sort_keys=True))
    return 0
```

- [ ] **Step 10: Run worker tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q
```

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add workers/analysis_worker/heartbeat.py workers/analysis_worker/tests/test_heartbeat.py workers/analysis_worker/main.py workers/analysis_worker/tests/test_pipeline.py
git commit -m "feat: record analysis worker heartbeats"
```

---

### Task 6: Smoke Script and Development Docs

**Files:**
- Create: `scripts/smoke_ops_health.sh`
- Modify: `docs/development.md`

- [ ] **Step 1: Create the smoke script**

Create `scripts/smoke_ops_health.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"

health_body="$(curl -fsS "$BASE_URL/healthz")"
ready_body="$(curl -fsS "$BASE_URL/readyz" || true)"
metrics_body="$(curl -fsS "$BASE_URL/metrics")"

printf '%s\n' "$health_body" | rg '"status":"ok"'
printf '%s\n' "$ready_body" | rg '"checks"'
printf '%s\n' "$metrics_body" | rg 'audit_gateway_up'
printf '%s\n' "$metrics_body" | rg 'audit_gateway_analysis_queue_depth'

echo "operational health smoke passed"
```

- [ ] **Step 2: Make the smoke script executable**

Run:

```bash
chmod +x scripts/smoke_ops_health.sh
```

Expected: command exits with status 0.

- [ ] **Step 3: Document operational endpoints**

Append this section to `docs/development.md`:

```markdown
## Operational Health and Metrics

The gateway exposes operational endpoints before admin and proxy routing:

- `GET /healthz`: process liveness. Returns HTTP 200 when the gateway process can answer.
- `GET /readyz`: readiness. Returns HTTP 200 for `ok` or `degraded`, and HTTP 503 for `down`.
- `GET /metrics`: Prometheus text metrics when `OPS_METRICS_ENABLED=true`.

Operational environment variables:

- `OPS_CHECK_TIMEOUT`: dependency check timeout, default `2s`.
- `OPS_WORKER_HEARTBEAT_MAX_AGE`: maximum fresh analysis worker heartbeat age, default `5m`.
- `OPS_QUEUE_LAG_WARN_THRESHOLD`: Redis `analysis_jobs` depth that marks readiness degraded, default `1000`.
- `OPS_METRICS_ENABLED`: enables `/metrics`, default `true`.
- `ANALYSIS_WORKER_ID`: optional stable worker ID for Python analysis worker heartbeat rows.

Local smoke check:

```bash
BASE_URL=http://127.0.0.1:18080 ./scripts/smoke_ops_health.sh
```

The smoke script expects the gateway to be running. `/readyz` can report `degraded` during local development when no analysis worker has written `worker_heartbeats`; that is acceptable as long as the response includes dependency checks and `/healthz` plus `/metrics` succeed.
```

- [ ] **Step 4: Run docs and script text checks**

Run:

```bash
rg -n "Operational Health and Metrics|OPS_WORKER_HEARTBEAT_MAX_AGE|smoke_ops_health" docs/development.md scripts/smoke_ops_health.sh
```

Expected: at least four matches across the docs and script.

- [ ] **Step 5: Commit**

```bash
git add scripts/smoke_ops_health.sh docs/development.md
git commit -m "docs: document operational health checks"
```

---

### Task 7: Final Verification

**Files:**
- No edits.

- [ ] **Step 1: Run all Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run all worker tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q
```

Expected: PASS.

- [ ] **Step 3: Run migration e2e**

Run:

```bash
E2E_DB=audit_gateway_plan_0007 ./scripts/e2e_worker_anomaly_coverage.sh
```

Expected: PASS.

- [ ] **Step 4: Run operational smoke against a local gateway**

Run:

```bash
docker compose -f deploy/docker-compose.yml up -d postgres redis
docker compose -f deploy/docker-compose.yml run --rm migrate
mkdir -p var/ops-smoke-evidence
AUDIT_GATEWAY_LISTEN_ADDR=:18080 \
NEW_API_BASE_URL=http://127.0.0.1:3000 \
AUDIT_HMAC_SECRET=0123456789abcdef0123456789abcdef \
ADMIN_SESSION_SECRET=admin-session-secret-0123456789abcdef \
EVIDENCE_STORAGE_DIR=var/ops-smoke-evidence \
POSTGRES_DSN=postgres://audit:audit@localhost:5432/audit_gateway?sslmode=disable \
REDIS_ADDR=localhost:6379 \
make run >/tmp/audit-gateway-ops-smoke.log 2>&1 &
gateway_pid=$!
trap 'kill "$gateway_pid"' EXIT
for i in {1..40}; do
  curl -fsS http://127.0.0.1:18080/healthz && break
  sleep 0.25
done
BASE_URL=http://127.0.0.1:18080 ./scripts/smoke_ops_health.sh
```

Expected: PASS with `operational health smoke passed`.

- [ ] **Step 5: Review no secret leakage**

Run:

```bash
rg -n "AUDIT_HMAC_SECRET|ADMIN_SESSION_SECRET|Authorization|x-api-key|mj-api-secret" internal/ops cmd/audit-gateway workers/analysis_worker/heartbeat.py docs/development.md
```

Expected: no matches in `internal/ops`, `cmd/audit-gateway`, or `workers/analysis_worker/heartbeat.py`; docs may mention environment variable names but must not contain secret values.

- [ ] **Step 6: Commit final verification notes if docs changed**

If Step 5 required doc wording edits, run:

```bash
git add docs/development.md
git commit -m "docs: clarify operational secret handling"
```

Expected: commit only when docs were edited in Step 5.
