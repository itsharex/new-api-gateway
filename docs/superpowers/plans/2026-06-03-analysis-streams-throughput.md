# Analysis Streams Throughput Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current serial Redis list analysis worker with a Redis Streams core/enrichment pipeline that improves peak throughput, keeps core results fast, and exposes queue/runtime metrics in the admin UI.

**Architecture:** Gateway requests will enqueue lightweight `analysis.core` stream messages keyed by `trace_id`. Core workers will claim tasks from Redis Streams, write immutable per-trace facts and core analysis results, then optionally enqueue enrichment work. Enrichment workers will append slow-path outputs without mutating core facts, and a rollup process will rebuild aggregate tables off the hot path. Admin APIs will expose live snapshot and historical runtime metrics for queue depth, latency, throughput, and consumer health.

**Tech Stack:** Go gateway/admin backend (`go test`), Python analysis worker (`uv`, `pytest`), Redis Streams, PostgreSQL migrations, vanilla admin UI JavaScript.

---

## File Structure

### Gateway And Queue Contract

- Create: `internal/jobs/streams.go`
  - Redis Streams publisher and lightweight message contract.
- Create: `internal/jobs/streams_test.go`
  - Unit tests for `XADD` field shape and default stream names.
- Modify: `internal/gateway/proxy.go`
  - Publish `analysis.core` messages instead of Redis list payloads.
- Modify: `cmd/audit-gateway/main.go`
  - Wire the new stream publisher and runtime readers.
- Modify: `cmd/audit-gateway/main_test.go`
  - Verify new publisher wiring and ops/admin handler dependencies.

### Schema And Shared Models

- Create: `migrations/0016_analysis_streams_redesign.sql`
  - New task/runtime tables and trace/result/evidence schema changes.
- Modify: `workers/analysis_worker/models.py`
  - Add stream/task/result-stage dataclasses and status constants.
- Modify: `internal/admin/models.go`
  - Add runtime snapshot/history/consumer response structs.
- Modify: `README.md`
  - Document Streams queues, worker modes, and runtime admin APIs.
- Modify: `ARCHITECTURE.md`
  - Replace Redis list description with the new core/enrichment/rollup pipeline.

### Worker Execution

- Create: `workers/analysis_worker/task_store.py`
  - Owns `analysis_tasks`, `trace_usage_facts`, and trace-stage transitions.
- Create: `workers/analysis_worker/streams.py`
  - Redis Streams consumer/publisher helpers, claim and ack flow.
- Create: `workers/analysis_worker/core_stage.py`
  - Core-stage orchestration from trace load to core commit.
- Create: `workers/analysis_worker/enrichment_stage.py`
  - Enrichment-stage orchestration for slow appends only.
- Create: `workers/analysis_worker/runtime_metrics.py`
  - Runtime metric sampling and history persistence.
- Modify: `workers/analysis_worker/main.py`
  - Replace `BLPOP` loop with stage-aware Streams runners.
- Modify: `workers/analysis_worker/repository.py`
  - Split core result writes from aggregate rollups and add stage/producer fields.
- Modify: `workers/analysis_worker/media_extraction.py`
  - Produce derived objects without rewriting original request bodies.
- Modify: `workers/analysis_worker/offline.py`
  - Rebuild `usage_aggregates` and `baseline_cache` from `trace_usage_facts`.

### Worker Tests

- Create: `workers/analysis_worker/tests/test_task_store.py`
- Create: `workers/analysis_worker/tests/test_streams.py`
- Modify: `workers/analysis_worker/tests/test_models.py`
- Modify: `workers/analysis_worker/tests/test_pipeline.py`
- Modify: `workers/analysis_worker/tests/test_repository.py`
- Modify: `workers/analysis_worker/tests/test_media_extraction.py`
- Modify: `workers/analysis_worker/tests/test_offline.py`

### Admin Runtime View

- Create: `internal/admin/runtime.go`
  - Runtime provider interface and summary helpers used by handlers.
- Modify: `internal/admin/handlers.go`
  - Add `/admin/api/analysis-runtime*` routes.
- Modify: `internal/admin/repository.go`
  - Load runtime history samples and consumer tables.
- Modify: `internal/admin/handlers_test.go`
- Modify: `internal/admin/repository_test.go`
- Modify: `internal/adminui/app.js`
  - Add runtime tab, KPI cards, charts, and consumer table.

## Task 1: Add The Streams-Oriented Schema And Shared Status Models

**Files:**
- Create: `migrations/0016_analysis_streams_redesign.sql`
- Modify: `workers/analysis_worker/models.py`
- Modify: `internal/admin/models.go`
- Modify: `workers/analysis_worker/tests/test_models.py`

- [ ] **Step 1: Write the failing shared-model tests**

Add these tests first.

```python
# workers/analysis_worker/tests/test_models.py
from models import (
    AnalysisTask,
    AnalysisStage,
    TaskStatus,
    TraceStageStatus,
    StreamEnvelope,
)


def test_stream_envelope_defaults_to_core_stage_attempt_one():
    envelope = StreamEnvelope(trace_id="trace_1")

    assert envelope.trace_id == "trace_1"
    assert envelope.stage == AnalysisStage.CORE
    assert envelope.attempt == 1
    assert envelope.hints == {}


def test_analysis_task_tracks_lease_and_error_fields():
    task = AnalysisTask(
        trace_id="trace_1",
        stage=AnalysisStage.ENRICHMENT,
        status=TaskStatus.QUEUED,
        attempt_count=0,
        max_attempts=5,
    )

    assert task.lease_owner == ""
    assert task.last_error_code == ""
    assert task.last_error_message == ""


def test_trace_stage_status_values_match_dual_stage_summary_model():
    assert TraceStageStatus.PENDING.value == "pending"
    assert TraceStageStatus.PROCESSING.value == "processing"
    assert TraceStageStatus.COMPLETED.value == "completed"
    assert TraceStageStatus.FAILED.value == "failed"
```

```go
// internal/admin/models.go test to add in internal/admin/handlers_test.go or repository_test.go later
func TestAnalysisRuntimeSnapshotJSONFields(t *testing.T) {
    snapshot := AnalysisRuntimeSnapshot{
        Stage:                   "core",
        QueueDepth:              12,
        OldestPendingAgeSeconds: 45,
        ThroughputPerMinute:     18,
    }

    if snapshot.Stage != "core" || snapshot.QueueDepth != 12 {
        t.Fatalf("snapshot = %#v", snapshot)
    }
}
```

- [ ] **Step 2: Run the targeted model tests and confirm they fail**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_models.py -k "stream_envelope or analysis_task or trace_stage_status"
```

Expected:

- FAIL because `StreamEnvelope`, `AnalysisTask`, and stage/status enums do not exist yet

- [ ] **Step 3: Add the migration and shared model types**

Create the schema and dataclasses.

```sql
-- migrations/0016_analysis_streams_redesign.sql
ALTER TABLE traces
    ADD COLUMN core_status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN enrichment_required BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN enrichment_status TEXT NOT NULL DEFAULT 'not_required',
    ADD COLUMN core_queued_at TIMESTAMPTZ,
    ADD COLUMN core_started_at TIMESTAMPTZ,
    ADD COLUMN core_completed_at TIMESTAMPTZ,
    ADD COLUMN enrichment_queued_at TIMESTAMPTZ,
    ADD COLUMN enrichment_started_at TIMESTAMPTZ,
    ADD COLUMN enrichment_completed_at TIMESTAMPTZ,
    ADD COLUMN last_analysis_error_code TEXT NOT NULL DEFAULT '';

ALTER TABLE analysis_results
    ADD COLUMN stage TEXT NOT NULL DEFAULT 'core',
    ADD COLUMN producer TEXT NOT NULL DEFAULT '',
    ADD COLUMN result_key TEXT NOT NULL DEFAULT '';

ALTER TABLE raw_evidence_objects
    ADD COLUMN variant TEXT NOT NULL DEFAULT 'original',
    ADD COLUMN derived_from_object_ref TEXT NOT NULL DEFAULT '';

CREATE TABLE analysis_tasks (
    trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    stage TEXT NOT NULL,
    status TEXT NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    stream_name TEXT NOT NULL DEFAULT '',
    stream_message_id TEXT NOT NULL DEFAULT '',
    queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '',
    last_error_message TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (trace_id, stage),
    CHECK (stage IN ('core', 'enrichment')),
    CHECK (status IN ('queued', 'leased', 'succeeded', 'failed_retryable', 'failed_terminal'))
);

CREATE TABLE trace_usage_facts (
    trace_id TEXT PRIMARY KEY REFERENCES traces(trace_id) ON DELETE CASCADE,
    token_fingerprint TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    route_pattern TEXT NOT NULL DEFAULT '',
    protocol_family TEXT NOT NULL DEFAULT '',
    request_started_at TIMESTAMPTZ NOT NULL,
    request_count BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    error_count BIGINT NOT NULL DEFAULT 0,
    stream_count BIGINT NOT NULL DEFAULT 0,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    cached_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    request_body_bytes BIGINT NOT NULL DEFAULT 0,
    response_body_bytes BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE analysis_runtime_samples (
    id BIGSERIAL PRIMARY KEY,
    sampled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    stage TEXT NOT NULL,
    queue_depth BIGINT NOT NULL DEFAULT 0,
    pending_count BIGINT NOT NULL DEFAULT 0,
    leased_count BIGINT NOT NULL DEFAULT 0,
    oldest_pending_age_seconds BIGINT NOT NULL DEFAULT 0,
    throughput_per_minute BIGINT NOT NULL DEFAULT 0,
    queue_wait_p50_ms BIGINT NOT NULL DEFAULT 0,
    queue_wait_p95_ms BIGINT NOT NULL DEFAULT 0,
    processing_p50_ms BIGINT NOT NULL DEFAULT 0,
    processing_p95_ms BIGINT NOT NULL DEFAULT 0,
    retryable_fail_count BIGINT NOT NULL DEFAULT 0,
    terminal_fail_count BIGINT NOT NULL DEFAULT 0,
    active_consumers BIGINT NOT NULL DEFAULT 0,
    CHECK (stage IN ('core', 'enrichment'))
);
```

```python
# workers/analysis_worker/models.py
from dataclasses import dataclass, field
from enum import StrEnum


class AnalysisStage(StrEnum):
    CORE = "core"
    ENRICHMENT = "enrichment"


class TaskStatus(StrEnum):
    QUEUED = "queued"
    LEASED = "leased"
    SUCCEEDED = "succeeded"
    FAILED_RETRYABLE = "failed_retryable"
    FAILED_TERMINAL = "failed_terminal"


class TraceStageStatus(StrEnum):
    PENDING = "pending"
    PROCESSING = "processing"
    COMPLETED = "completed"
    FAILED = "failed"
    NOT_REQUIRED = "not_required"


@dataclass(frozen=True)
class StreamEnvelope:
    trace_id: str
    stage: AnalysisStage = AnalysisStage.CORE
    enqueued_at: str = ""
    attempt: int = 1
    hints: dict[str, str] = field(default_factory=dict)


@dataclass(frozen=True)
class AnalysisTask:
    trace_id: str
    stage: AnalysisStage
    status: TaskStatus
    attempt_count: int
    max_attempts: int
    lease_owner: str = ""
    lease_expires_at: str = ""
    stream_name: str = ""
    stream_message_id: str = ""
    queued_at: str = ""
    started_at: str = ""
    completed_at: str = ""
    last_error_code: str = ""
    last_error_message: str = ""
```

```go
// internal/admin/models.go
type AnalysisRuntimeSnapshot struct {
    Stage                   string `json:"stage"`
    QueueDepth              int64  `json:"queue_depth"`
    PendingCount            int64  `json:"pending_count"`
    LeasedCount             int64  `json:"leased_count"`
    OldestPendingAgeSeconds int64  `json:"oldest_pending_age_seconds"`
    ThroughputPerMinute     int64  `json:"throughput_per_minute"`
    QueueWaitP95MS          int64  `json:"queue_wait_p95_ms"`
    ProcessingP95MS         int64  `json:"processing_p95_ms"`
}

type AnalysisRuntimeHistoryPoint struct {
    SampledAt               string `json:"sampled_at"`
    QueueDepth              int64  `json:"queue_depth"`
    OldestPendingAgeSeconds int64  `json:"oldest_pending_age_seconds"`
    QueueWaitP95MS          int64  `json:"queue_wait_p95_ms"`
    ProcessingP95MS         int64  `json:"processing_p95_ms"`
}

type AnalysisRuntimeConsumer struct {
    WorkerID      string `json:"worker_id"`
    Stage         string `json:"stage"`
    LeasedCount   int64  `json:"leased_count"`
    LastSeenAt    string `json:"last_seen_at"`
    IdleSeconds   int64  `json:"idle_seconds"`
    LastErrorCode string `json:"last_error_code"`
}
```

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_models.py -k "stream_envelope or analysis_task or trace_stage_status"
```

Expected:

- PASS for the new enum/dataclass tests

- [ ] **Step 5: Commit the schema/model foundation**

```bash
cd /Users/roy/codes/new-api-gateway
git add migrations/0016_analysis_streams_redesign.sql workers/analysis_worker/models.py workers/analysis_worker/tests/test_models.py internal/admin/models.go
git commit -m "feat: add analysis streams schema and status models"
```

## Task 2: Replace Redis List Publishing With Streams Core Messages

**Files:**
- Create: `internal/jobs/streams.go`
- Create: `internal/jobs/streams_test.go`
- Modify: `internal/gateway/proxy.go`
- Modify: `cmd/audit-gateway/main.go`
- Modify: `cmd/audit-gateway/main_test.go`

- [ ] **Step 1: Write the failing Go tests for stream publishing**

```go
// internal/jobs/streams_test.go
type fakeStreamClient struct {
    stream string
    values map[string]any
}

func (f *fakeStreamClient) XAdd(_ context.Context, a *redis.XAddArgs) *redis.StringCmd {
    f.stream = a.Stream
    f.values = a.Values.(map[string]any)
    return redis.NewStringResult("1-0", nil)
}

func TestRedisStreamPublisherPublishesCoreEnvelope(t *testing.T) {
    client := &fakeStreamClient{}
    publisher := NewRedisStreamPublisher(client, "analysis.core")

    err := publisher.PublishTraceCaptured(context.Background(), TraceCapturedInput{
        TraceID: "trace_1",
        ProtocolFamily: "openai_chat",
        RequestBodySize: 512,
        ResponseBodySize: 2048,
        CaptureMode: "raw_and_normalized",
    })
    if err != nil {
        t.Fatalf("PublishTraceCaptured() error = %v", err)
    }

    if client.stream != "analysis.core" {
        t.Fatalf("stream = %q, want analysis.core", client.stream)
    }
    if client.values["trace_id"] != "trace_1" {
        t.Fatalf("values = %#v", client.values)
    }
    if client.values["stage"] != "core" {
        t.Fatalf("values = %#v", client.values)
    }
}
```

```go
// cmd/audit-gateway/main_test.go
func TestBuildHandlerUsesRedisStreamPublisher(t *testing.T) {
    handler := buildHandler(config.Config{
        NewAPIBaseURL: "http://localhost:3000",
        AuditHMACSecret: strings.Repeat("a", 32),
    }, nil, nil, redis.NewClient(&redis.Options{Addr: "localhost:6379"}), log.New(io.Discard, "", 0))
    if handler.JobPublisher == nil {
        t.Fatal("JobPublisher is nil")
    }
}
```

- [ ] **Step 2: Run the targeted Go tests and confirm they fail**

Run:

```bash
cd /Users/roy/codes/new-api-gateway
go test ./internal/jobs ./cmd/audit-gateway -run 'TestRedisStreamPublisherPublishesCoreEnvelope|TestBuildHandlerUsesRedisStreamPublisher'
```

Expected:

- FAIL because `NewRedisStreamPublisher` does not exist yet

- [ ] **Step 3: Add the stream publisher and switch gateway wiring**

```go
// internal/jobs/streams.go
package jobs

import (
    "context"
    "time"

    "github.com/redis/go-redis/v9"
)

const DefaultRedisCoreStream = "analysis.core"

type redisStreamClient interface {
    XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
}

type RedisStreamPublisher struct {
    client redisStreamClient
    stream string
    now    func() time.Time
}

func NewRedisStreamPublisher(client redisStreamClient, stream string) RedisStreamPublisher {
    if stream == "" {
        stream = DefaultRedisCoreStream
    }
    return RedisStreamPublisher{client: client, stream: stream, now: time.Now}
}

func (p RedisStreamPublisher) PublishTraceCaptured(ctx context.Context, input TraceCapturedInput) error {
    values := map[string]any{
        "trace_id":     input.TraceID,
        "stage":        "core",
        "enqueued_at":  p.now().UTC().Format(time.RFC3339),
        "attempt":      "1",
        "protocol":     input.ProtocolFamily,
        "capture_mode": input.CaptureMode,
        "request_size": input.RequestBodySize,
        "response_size": input.ResponseBodySize,
    }
    return p.client.XAdd(ctx, &redis.XAddArgs{Stream: p.stream, Values: values}).Err()
}
```

```go
// internal/gateway/proxy.go
if h.JobPublisher != nil {
    if err := h.JobPublisher.PublishTraceCaptured(ctx, jobs.TraceCapturedInput{
        TraceID:          record.traceID,
        ProtocolFamily:   record.entry.ProtocolFamily,
        CaptureMode:      string(record.entry.CaptureMode),
        RequestBodySize:  record.requestSize,
        ResponseBodySize: record.responseSize,
    }); err != nil {
        h.reportAuditError(ctx, err)
        errs = append(errs, err)
    }
}
```

```go
// cmd/audit-gateway/main.go
JobPublisher: jobs.NewRedisStreamPublisher(redisClient, jobs.DefaultRedisCoreStream),
```

- [ ] **Step 4: Re-run the Go tests and confirm they pass**

Run:

```bash
cd /Users/roy/codes/new-api-gateway
go test ./internal/jobs ./cmd/audit-gateway -run 'TestRedisStreamPublisherPublishesCoreEnvelope|TestBuildHandlerUsesRedisStreamPublisher'
```

Expected:

- PASS for the new publisher contract and handler wiring tests

- [ ] **Step 5: Commit the stream publisher switch**

```bash
cd /Users/roy/codes/new-api-gateway
git add internal/jobs/streams.go internal/jobs/streams_test.go internal/gateway/proxy.go cmd/audit-gateway/main.go cmd/audit-gateway/main_test.go
git commit -m "feat: publish analysis work to redis streams"
```

## Task 3: Build Task Leasing And Core Streams Execution

**Files:**
- Create: `workers/analysis_worker/task_store.py`
- Create: `workers/analysis_worker/streams.py`
- Create: `workers/analysis_worker/core_stage.py`
- Modify: `workers/analysis_worker/main.py`
- Create: `workers/analysis_worker/tests/test_task_store.py`
- Create: `workers/analysis_worker/tests/test_streams.py`
- Modify: `workers/analysis_worker/tests/test_pipeline.py`

- [ ] **Step 1: Write failing task-store and stream-runner tests**

```python
# workers/analysis_worker/tests/test_task_store.py
def test_claim_task_transitions_queued_to_leased():
    conn = FakeConnection()
    store = AnalysisTaskStore(conn, worker_id="core-1")

    store.insert_task(trace_id="trace_1", stage="core", stream_name="analysis.core", stream_message_id="1-0")
    claimed = store.claim_task(trace_id="trace_1", stage="core", lease_seconds=30)

    assert claimed is True
    assert conn.task_rows[("trace_1", "core")]["status"] == "leased"
    assert conn.task_rows[("trace_1", "core")]["lease_owner"] == "core-1"
```

```python
# workers/analysis_worker/tests/test_streams.py
def test_stream_consumer_reads_claims_and_acks_message():
    redis_client = FakeRedisStreamClient(messages=[("1-0", {"trace_id": "trace_1", "stage": "core", "attempt": "1"})])
    consumer = StreamConsumer(redis_client, stream="analysis.core", group="analysis-core-workers", consumer_name="worker-1")

    message = consumer.read_one(count=1, block_ms=1000)

    assert message.message_id == "1-0"
    consumer.ack(message.message_id)
    assert redis_client.acked == [("analysis.core", "analysis-core-workers", "1-0")]
```

```python
# workers/analysis_worker/tests/test_pipeline.py
def test_process_core_stream_message_marks_trace_completed_and_acks():
    redis_client = FakeRedisStreamClient(messages=[("1-0", {"trace_id": "trace_1", "stage": "core", "attempt": "1"})])
    connection = FakeConnection()
    stage = CoreStageProcessor(connection, FilesystemEvidenceStore("/tmp/evidence"))

    result = run_core_once(redis_client, connection, stage, worker_id="core-1")

    assert result["worker_status"] == "processed"
    assert connection.traces["trace_1"]["core_status"] == "completed"
    assert redis_client.acked == [("analysis.core", "analysis-core-workers", "1-0")]
```

- [ ] **Step 2: Run the worker tests and confirm they fail**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_task_store.py tests/test_streams.py tests/test_pipeline.py -k "claim_task or stream_consumer or core_stream_message"
```

Expected:

- FAIL because `AnalysisTaskStore`, `StreamConsumer`, and `run_core_once` do not exist yet

- [ ] **Step 3: Implement leasing, ack flow, and core execution**

```python
# workers/analysis_worker/task_store.py
class AnalysisTaskStore:
    def __init__(self, connection, worker_id: str):
        self.connection = connection
        self.worker_id = worker_id

    def insert_task(self, trace_id: str, stage: str, stream_name: str, stream_message_id: str) -> None:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            INSERT INTO analysis_tasks (
                trace_id, stage, status, stream_name, stream_message_id
            ) VALUES (%s, %s, 'queued', %s, %s)
            ON CONFLICT (trace_id, stage) DO NOTHING
            """,
            (trace_id, stage, stream_name, stream_message_id),
        )
        self.connection.commit()

    def claim_task(self, trace_id: str, stage: str, lease_seconds: int) -> bool:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            UPDATE analysis_tasks
            SET status = 'leased',
                lease_owner = %s,
                lease_expires_at = now() + (%s || ' seconds')::interval,
                attempt_count = attempt_count + 1,
                started_at = COALESCE(started_at, now()),
                updated_at = now()
            WHERE trace_id = %s
              AND stage = %s
              AND status IN ('queued', 'failed_retryable')
            """,
            (self.worker_id, lease_seconds, trace_id, stage),
        )
        self.connection.commit()
        return cursor.rowcount == 1

    def mark_succeeded(self, trace_id: str, stage: str) -> None:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            UPDATE analysis_tasks
            SET status = 'succeeded', completed_at = now(), updated_at = now()
            WHERE trace_id = %s AND stage = %s
            """,
            (trace_id, stage),
        )
        self.connection.commit()
```

```python
# workers/analysis_worker/streams.py
@dataclass(frozen=True)
class StreamMessage:
    message_id: str
    values: dict[str, str]


class StreamConsumer:
    def __init__(self, client, stream: str, group: str, consumer_name: str):
        self.client = client
        self.stream = stream
        self.group = group
        self.consumer_name = consumer_name

    def read_one(self, count: int, block_ms: int) -> StreamMessage | None:
        response = self.client.xreadgroup(
            groupname=self.group,
            consumername=self.consumer_name,
            streams={self.stream: ">"},
            count=count,
            block=block_ms,
        )
        if not response:
            return None
        _, entries = response[0]
        message_id, values = entries[0]
        return StreamMessage(message_id=message_id, values=values)

    def ack(self, message_id: str) -> None:
        self.client.xack(self.stream, self.group, message_id)


def publish_stream_message(client, stream: str, trace_id: str, stage: str, attempt: int, hints: dict[str, str]) -> str:
    values = {
        "trace_id": trace_id,
        "stage": stage,
        "attempt": str(attempt),
    }
    for key, value in hints.items():
        values[f"hint_{key}"] = value
    return client.xadd(stream, values)
```

```python
# workers/analysis_worker/core_stage.py
class CoreStageProcessor:
    def __init__(self, connection, evidence_store, redis_client, llm_judge=None):
        self.connection = connection
        self.evidence_store = evidence_store
        self.redis_client = redis_client
        self.llm_judge = llm_judge

    def process(self, trace_id: str) -> dict:
        task_repo = PostgresAnalysisRepository(self.connection)
        context_repo = PostgresContextRepository(self.connection)
        payload = task_repo.load_trace_job_json(trace_id)
        result = process_job_line(
            payload,
            self.evidence_store,
            task_repo,
            context_repo,
            llm_judge=self.llm_judge,
        )
        cursor = self.connection.cursor()
        enrichment_required = result.get("media_assets_extracted", 0) > 0 or result.get("llm_judge_status") == "degraded"
        cursor.execute(
            """
            UPDATE traces
            SET core_status = 'completed',
                core_completed_at = now(),
                enrichment_required = %s,
                enrichment_status = CASE WHEN %s THEN 'pending' ELSE 'not_required' END,
                enrichment_queued_at = CASE WHEN %s THEN now() ELSE enrichment_queued_at END,
                updated_at = now()
            WHERE trace_id = %s
            """,
            (enrichment_required, enrichment_required, enrichment_required, trace_id),
        )
        self.connection.commit()
        if enrichment_required:
            publish_stream_message(
                self.redis_client,
                stream="analysis.enrichment",
                trace_id=trace_id,
                stage="enrichment",
                attempt=1,
                hints={},
            )
        return result
```

```python
# workers/analysis_worker/main.py
def run_core_once(redis_client, connection, stage_processor, worker_id: str) -> dict:
    consumer = StreamConsumer(redis_client, "analysis.core", "analysis-core-workers", worker_id)
    message = consumer.read_one(count=1, block_ms=1000)
    if message is None:
        return {"worker_status": "idle"}

    store = AnalysisTaskStore(connection, worker_id)
    trace_id = message.values["trace_id"]
    store.insert_task(trace_id, "core", "analysis.core", message.message_id)
    if not store.claim_task(trace_id, "core", lease_seconds=30):
        consumer.ack(message.message_id)
        return {"worker_status": "skipped", "trace_id": trace_id}

    result = stage_processor.process(trace_id)
    store.mark_succeeded(trace_id, "core")
    consumer.ack(message.message_id)
    return {"worker_status": "processed", "trace_id": trace_id, **result}
```

- [ ] **Step 4: Re-run the worker tests and confirm they pass**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_task_store.py tests/test_streams.py tests/test_pipeline.py -k "claim_task or stream_consumer or core_stream_message"
```

Expected:

- PASS for claim, ack, and core stage flow

- [ ] **Step 5: Commit the core Streams runner**

```bash
cd /Users/roy/codes/new-api-gateway
git add workers/analysis_worker/task_store.py workers/analysis_worker/streams.py workers/analysis_worker/core_stage.py workers/analysis_worker/main.py workers/analysis_worker/tests/test_task_store.py workers/analysis_worker/tests/test_streams.py workers/analysis_worker/tests/test_pipeline.py
git commit -m "feat: add core stream task leasing"
```

## Task 4: Split Enrichment And Make Derived Evidence Immutable

**Files:**
- Create: `workers/analysis_worker/enrichment_stage.py`
- Modify: `workers/analysis_worker/media_extraction.py`
- Modify: `workers/analysis_worker/repository.py`
- Modify: `workers/analysis_worker/main.py`
- Modify: `workers/analysis_worker/tests/test_media_extraction.py`
- Modify: `workers/analysis_worker/tests/test_repository.py`
- Modify: `workers/analysis_worker/tests/test_pipeline.py`

- [ ] **Step 1: Write failing enrichment and derived-evidence tests**

```python
# workers/analysis_worker/tests/test_media_extraction.py
def test_apply_replacements_writes_sanitized_copy_without_mutating_original():
    store = FakeEvidenceStore({"file:///raw/trace_1/request_body.json": '{"image":"data:image/png;base64,AAAA"}'})
    ctx = MediaExtractionContext(store, "file:///raw/trace_1", "trace_1")

    asset = ctx.extract_data_url("data:image/png;base64,AAAA", "image")
    derived_ref = ctx.write_sanitized_copy("file:///raw/trace_1/request_body.json")

    assert asset is not None
    assert derived_ref.endswith("request_body.sanitized.json")
    assert store.read_text("file:///raw/trace_1/request_body.json") == '{"image":"data:image/png;base64,AAAA"}'
    assert "audit-media:" in store.read_text(derived_ref)
```

```python
# workers/analysis_worker/tests/test_pipeline.py
def test_core_stage_enqueues_enrichment_when_llm_or_media_is_required():
    redis_client = FakeRedisStreamClient()
    connection = FakeConnection(trace_needs_enrichment=True)
    processor = CoreStageProcessor(connection, FilesystemEvidenceStore("/tmp/evidence"))

    result = processor.process("trace_1")

    assert result["enrichment_required"] is True
    assert redis_client.added[0][0] == "analysis.enrichment"
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_media_extraction.py tests/test_pipeline.py -k "sanitized_copy or enqueues_enrichment"
```

Expected:

- FAIL because original evidence is still rewritten in place and enrichment queueing does not exist

- [ ] **Step 3: Implement immutable derived evidence and enrichment queueing**

```python
# workers/analysis_worker/media_extraction.py
class MediaExtractionContext:
    def write_sanitized_copy(self, object_ref: str) -> str:
        source = self.evidence_store.read_text(object_ref)
        sanitized = source
        for original, replacement in self.replacements:
            sanitized = sanitized.replace(original, replacement)
        derived_ref = object_ref.replace(".json", ".sanitized.json")
        self.evidence_store.write_text(derived_ref, sanitized)
        return derived_ref
```

```python
# workers/analysis_worker/repository.py
def save_derived_evidence_object(self, trace_id: str, object_ref: str, content_type: str, variant: str, derived_from: str) -> None:
    cursor = self.connection.cursor()
    cursor.execute(
        """
        INSERT INTO raw_evidence_objects (
            trace_id, object_type, object_ref, storage_backend, content_type,
            size_bytes, variant, derived_from_object_ref
        ) VALUES (%s, 'request_body', %s, 'filesystem', %s, 0, %s, %s)
        """,
        (trace_id, object_ref, content_type, variant, derived_from),
    )
    self.connection.commit()


def load_trace_job_json(self, trace_id: str) -> str:
    cursor = self.connection.cursor()
    cursor.execute(
        """
        SELECT json_build_object(
            'type', 'trace_captured',
            'trace_id', trace_id,
            'route_pattern', route_pattern,
            'protocol_family', protocol_family,
            'capture_mode', capture_mode,
            'username', username_snapshot,
            'request_raw_ref', request_raw_ref,
            'response_raw_ref', response_raw_ref,
            'request_content_type', request_content_type,
            'response_content_type', response_content_type,
            'model_requested', model_requested,
            'usage_prompt_tokens', usage_prompt_tokens,
            'usage_completion_tokens', usage_completion_tokens,
            'usage_total_tokens', usage_total_tokens,
            'usage_reasoning_tokens', usage_reasoning_tokens,
            'usage_cached_tokens', usage_cached_tokens,
            'token_fingerprint', token_fingerprint,
            'fingerprint_display', fingerprint_display,
            'new_api_token_id', new_api_token_id_snapshot,
            'token_name_snapshot', token_name_snapshot,
            'identity_resolution_status', identity_resolution_status,
            'client_ip_hash', client_ip_hash,
            'user_agent_hash', user_agent_hash,
            'status_code', status_code,
            'upstream_status_code', upstream_status_code,
            'stream', stream,
            'request_started_at', request_started_at::text,
            'request_body_size', request_body_size,
            'response_body_size', response_body_size
        )::text
        FROM traces
        WHERE trace_id = %s
        """,
        (trace_id,),
    )
    row = cursor.fetchone()
    if row is None:
        raise ValueError(f"trace not found: {trace_id}")
    return row[0]


def insert_analysis_result(self, result: AnalysisResult, stage: str, producer: str, result_key: str) -> None:
    cursor = self.connection.cursor()
    cursor.execute(
        """
        INSERT INTO analysis_results (
            trace_id, analyzer_name, analyzer_version, policy_version,
            category, label, score, confidence, severity, result_json,
            stage, producer, result_key
        ) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s::jsonb,%s,%s,%s)
        ON CONFLICT (trace_id, stage, producer, result_key)
        DO UPDATE SET
            analyzer_version = EXCLUDED.analyzer_version,
            label = EXCLUDED.label,
            score = EXCLUDED.score,
            confidence = EXCLUDED.confidence,
            severity = EXCLUDED.severity,
            result_json = EXCLUDED.result_json
        """,
        (
            result.trace_id,
            result.analyzer_name,
            result.analyzer_version,
            result.policy_version,
            result.category,
            result.label,
            result.score,
            result.confidence,
            result.severity,
            json.dumps(result.result, sort_keys=True),
            stage,
            producer,
            result_key,
        ),
    )
    self.connection.commit()
```

```python
# workers/analysis_worker/enrichment_stage.py
class EnrichmentStageProcessor:
    def __init__(self, connection, evidence_store, llm_judge=None):
        self.connection = connection
        self.evidence_store = evidence_store
        self.llm_judge = llm_judge

    def process(self, trace_id: str) -> dict:
        repo = PostgresAnalysisRepository(self.connection)
        payload = repo.load_trace_job_json(trace_id)
        job = parse_job(payload)
        request_body = self.evidence_store.read_text(job.request_raw_ref) if job.request_raw_ref else ""
        response_body = self.evidence_store.read_text(job.response_raw_ref) if job.response_raw_ref else ""
        messages, _ = normalize_json_trace(job, request_body, response_body, None)
        assessment = classify_work_relevance(job, messages, [], llm_judge=self.llm_judge)
        repo.insert_analysis_result(
            assessment.to_analysis_result(),
            stage="enrichment",
            producer="llm_judge",
            result_key="work_relevance_secondary",
        )
        cursor = self.connection.cursor()
        cursor.execute(
            """
            UPDATE traces
            SET enrichment_status = 'completed',
                enrichment_completed_at = now(),
                updated_at = now()
            WHERE trace_id = %s
            """,
            (trace_id,),
        )
        self.connection.commit()
        result = {"trace_id": trace_id, "worker_status": "processed"}
        return result
```

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_media_extraction.py tests/test_pipeline.py -k "sanitized_copy or enqueues_enrichment"
```

Expected:

- PASS for immutable evidence handling and enrichment enqueueing

- [ ] **Step 5: Commit the enrichment split**

```bash
cd /Users/roy/codes/new-api-gateway
git add workers/analysis_worker/enrichment_stage.py workers/analysis_worker/media_extraction.py workers/analysis_worker/repository.py workers/analysis_worker/main.py workers/analysis_worker/tests/test_media_extraction.py workers/analysis_worker/tests/test_repository.py workers/analysis_worker/tests/test_pipeline.py
git commit -m "feat: split enrichment from core analysis"
```

## Task 5: Move Aggregates Off The Hot Path And Add Rollups

**Files:**
- Modify: `workers/analysis_worker/repository.py`
- Modify: `workers/analysis_worker/offline.py`
- Modify: `workers/analysis_worker/tests/test_repository.py`
- Modify: `workers/analysis_worker/tests/test_offline.py`

- [ ] **Step 1: Write failing tests for per-trace facts and rollup rebuild**

```python
# workers/analysis_worker/tests/test_repository.py
def test_save_trace_analysis_writes_trace_usage_fact_instead_of_usage_aggregate_upsert():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)

    repo.save_trace_usage_fact(
        trace_id="trace_1",
        token_fingerprint="fp_1",
        username="alice",
        model="gpt-4.1",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        request_started_at="2026-06-03T00:00:00+00:00",
        request_count=1,
        success_count=1,
        error_count=0,
        stream_count=0,
        prompt_tokens=10,
        completion_tokens=12,
        cached_tokens=2,
        total_tokens=22,
        reasoning_tokens=0,
        request_body_bytes=128,
        response_body_bytes=512,
    )

    assert any("INSERT INTO trace_usage_facts" in sql for sql in conn.executed_sql)
    assert not any("INSERT INTO usage_aggregates" in sql for sql in conn.executed_sql)
```

```python
# workers/analysis_worker/tests/test_offline.py
def test_run_offline_batch_rebuilds_usage_aggregates_from_trace_usage_facts():
    conn = FakeConnection()
    conn.trace_usage_fact_rows = [
        {
            "bucket_start": "2026-06-03T00:00:00+00:00",
            "bucket_size": "day",
            "token_fingerprint": "fp_1",
            "username": "alice",
            "model": "gpt-4.1",
            "route_pattern": "/v1/chat/completions",
            "protocol_family": "openai_chat",
            "request_count": 2,
            "success_count": 2,
            "error_count": 0,
            "prompt_tokens": 20,
            "completion_tokens": 24,
            "cached_tokens": 4,
            "total_tokens": 44,
        }
    ]

    result = run_offline_batch(conn)

    assert result["usage_aggregate_rows"] == 1
    assert any("INSERT INTO usage_aggregates" in sql for sql in conn.executed_sql)
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_repository.py tests/test_offline.py -k "trace_usage_fact or rebuilds_usage_aggregates"
```

Expected:

- FAIL because repository still writes aggregates inline and offline batch does not rebuild from facts

- [ ] **Step 3: Implement `trace_usage_facts` writes and rollup rebuild**

```python
# workers/analysis_worker/repository.py
def save_trace_usage_fact(self, **fact) -> None:
    cursor = self.connection.cursor()
    cursor.execute(
        """
        INSERT INTO trace_usage_facts (
            trace_id, token_fingerprint, username, model, route_pattern, protocol_family,
            request_started_at, request_count, success_count, error_count, stream_count,
            prompt_tokens, completion_tokens, cached_tokens, total_tokens, reasoning_tokens,
            request_body_bytes, response_body_bytes, updated_at
        ) VALUES (
            %(trace_id)s, %(token_fingerprint)s, %(username)s, %(model)s, %(route_pattern)s, %(protocol_family)s,
            %(request_started_at)s, %(request_count)s, %(success_count)s, %(error_count)s, %(stream_count)s,
            %(prompt_tokens)s, %(completion_tokens)s, %(cached_tokens)s, %(total_tokens)s, %(reasoning_tokens)s,
            %(request_body_bytes)s, %(response_body_bytes)s, now()
        )
        ON CONFLICT (trace_id) DO UPDATE SET
            prompt_tokens = EXCLUDED.prompt_tokens,
            completion_tokens = EXCLUDED.completion_tokens,
            cached_tokens = EXCLUDED.cached_tokens,
            total_tokens = EXCLUDED.total_tokens,
            updated_at = now()
        """,
        fact,
    )
    self.connection.commit()
```

```python
# workers/analysis_worker/offline.py
from baseline import compute_trace_level_baselines, upsert_baselines

ROLLUP_USAGE_FACTS = """
SELECT
    date_trunc('hour', request_started_at) AS bucket_start,
    'hour' AS bucket_size,
    token_fingerprint,
    username,
    model,
    route_pattern,
    protocol_family,
    SUM(request_count) AS request_count,
    SUM(success_count) AS success_count,
    SUM(error_count) AS error_count,
    SUM(prompt_tokens) AS prompt_tokens,
    SUM(completion_tokens) AS completion_tokens,
    SUM(cached_tokens) AS cached_tokens,
    SUM(total_tokens) AS total_tokens
FROM trace_usage_facts
GROUP BY 1, 2, 3, 4, 5, 6, 7
"""

TRACE_LEVEL_BASELINES = """
SELECT
    token_fingerprint AS fingerprint_key,
    PERCENTILE_CONT(0.95) WITHIN GROUP (
        ORDER BY GREATEST(prompt_tokens - cached_tokens, 0) + completion_tokens
    ) AS p95_effective,
    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY completion_tokens) AS p95_completion
FROM trace_usage_facts
WHERE request_started_at >= (now() - (%s || ' days')::interval)
GROUP BY token_fingerprint
HAVING COUNT(*) >= 5
"""


def load_trace_level_rows(connection, lookback_days: int) -> list[dict]:
    cursor = connection.cursor()
    cursor.execute(TRACE_LEVEL_BASELINES, (str(lookback_days),))
    columns = ["fingerprint_key", "p95_effective", "p95_completion"]
    return [dict(zip(columns, row)) for row in cursor.fetchall()]

def run_offline_batch(connection, lookback_days: int = 7) -> dict:
    cursor = connection.cursor()
    cursor.execute(ROLLUP_USAGE_FACTS)
    rows = cursor.fetchall()
    for row in rows:
        cursor.execute(
            """
            INSERT INTO usage_aggregates (
                bucket_start, bucket_size, token_fingerprint, username, model, route_pattern,
                protocol_family, request_count, success_count, error_count,
                prompt_tokens, completion_tokens, cached_tokens, total_tokens
            ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
            ON CONFLICT (
                bucket_start, bucket_size, token_fingerprint, username, model, route_pattern, protocol_family
            ) DO UPDATE SET
                request_count = EXCLUDED.request_count,
                success_count = EXCLUDED.success_count,
                error_count = EXCLUDED.error_count,
                prompt_tokens = EXCLUDED.prompt_tokens,
                completion_tokens = EXCLUDED.completion_tokens,
                cached_tokens = EXCLUDED.cached_tokens,
                total_tokens = EXCLUDED.total_tokens,
                updated_at = now()
            """,
            row,
        )
    connection.commit()
    trace_level_rows = load_trace_level_rows(connection, lookback_days=lookback_days)
    upsert_baselines(connection, compute_trace_level_baselines(trace_level_rows), ttl_hours=25)
    return {"usage_aggregate_rows": len(rows)}
```

- [ ] **Step 4: Re-run the targeted tests and confirm they pass**

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q tests/test_repository.py tests/test_offline.py -k "trace_usage_fact or rebuilds_usage_aggregates"
```

Expected:

- PASS for per-trace fact persistence and rollup rebuild

- [ ] **Step 5: Commit the hot-path aggregate removal**

```bash
cd /Users/roy/codes/new-api-gateway
git add workers/analysis_worker/repository.py workers/analysis_worker/offline.py workers/analysis_worker/tests/test_repository.py workers/analysis_worker/tests/test_offline.py
git commit -m "feat: move analysis aggregates to async rollup"
```

## Task 6: Add Admin Runtime APIs And Runtime Dashboard UI

**Files:**
- Create: `internal/admin/runtime.go`
- Modify: `internal/admin/handlers.go`
- Modify: `internal/admin/repository.go`
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/handlers_test.go`
- Modify: `internal/admin/repository_test.go`
- Modify: `internal/adminui/app.js`
- Modify: `cmd/audit-gateway/main.go`

- [ ] **Step 1: Write failing Go tests for runtime APIs and UI view loading**

```go
// internal/admin/handlers_test.go
type stubRuntimeProvider struct {
    snapshot  AnalysisRuntimeSnapshot
    consumers []AnalysisRuntimeConsumer
}

func (s stubRuntimeProvider) Snapshot(context.Context, string) (AnalysisRuntimeSnapshot, error) {
    return s.snapshot, nil
}

func (s stubRuntimeProvider) Consumers(context.Context, string) ([]AnalysisRuntimeConsumer, error) {
    return s.consumers, nil
}

func TestAnalysisRuntimeSnapshotHandlerReturnsCoreMetrics(t *testing.T) {
    db := &recordingAdminDB{}
    runtime := stubRuntimeProvider{
        snapshot: AnalysisRuntimeSnapshot{
            Stage:                   "core",
            QueueDepth:              8,
            PendingCount:            3,
            LeasedCount:             2,
            OldestPendingAgeSeconds: 30,
            ThroughputPerMinute:     25,
            QueueWaitP95MS:          1200,
            ProcessingP95MS:         900,
        },
    }
    handler := NewHandler(HandlerConfig{Repo: NewRepository(db), RuntimeProvider: runtime})

    req := httptest.NewRequest(http.MethodGet, "/admin/api/analysis-runtime?stage=core", nil)
    recorder := httptest.NewRecorder()
    handler.ServeHTTP(recorder, req)

    if recorder.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", recorder.Code)
    }
    if !strings.Contains(recorder.Body.String(), `"queue_depth":8`) {
        t.Fatalf("body = %s", recorder.Body.String())
    }
}
```

```javascript
// internal/adminui/app.js test intent to mirror in manual verification
state.view = "analysis-runtime";
const body = await api("/analysis-runtime?stage=core");
renderAnalysisRuntime(body);
```

- [ ] **Step 2: Run the targeted Go tests and confirm they fail**

Run:

```bash
cd /Users/roy/codes/new-api-gateway
go test ./internal/admin ./cmd/audit-gateway -run 'TestAnalysisRuntimeSnapshotHandlerReturnsCoreMetrics'
```

Expected:

- FAIL because `RuntimeProvider` and `/admin/api/analysis-runtime` do not exist yet

- [ ] **Step 3: Implement runtime provider, handlers, and UI view**

```go
// internal/admin/runtime.go
package admin

import "context"

type RuntimeProvider interface {
    Snapshot(ctx context.Context, stage string) (AnalysisRuntimeSnapshot, error)
    Consumers(ctx context.Context, stage string) ([]AnalysisRuntimeConsumer, error)
}
```

```go
// internal/admin/handlers.go
type HandlerConfig struct {
    Repo            Repository
    Auth            Auth
    AuditSecret     string
    EvidenceStore   evidence.Store
    RuntimeProvider RuntimeProvider
}

type Handler struct {
    repo            Repository
    auth            Auth
    auditSecret     string
    evidenceStore   evidence.Store
    runtimeProvider RuntimeProvider
    lookupLimiter   RateLimiter
    rawLimiter      RateLimiter
}

func (h Handler) routes() *http.ServeMux {
    mux := http.NewServeMux()
    // existing routes omitted
    mux.Handle("GET /admin/api/analysis-runtime", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.analysisRuntimeSnapshot))))
    mux.Handle("GET /admin/api/analysis-runtime/history", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.analysisRuntimeHistory))))
    mux.Handle("GET /admin/api/analysis-runtime/consumers", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.analysisRuntimeConsumers))))
    return mux
}

func (h Handler) analysisRuntimeSnapshot(w http.ResponseWriter, r *http.Request) {
    stage := strings.TrimSpace(r.URL.Query().Get("stage"))
    if stage == "" {
        stage = "core"
    }
    snapshot, err := h.runtimeProvider.Snapshot(r.Context(), stage)
    if err != nil {
        http.Error(w, "failed to load analysis runtime snapshot", http.StatusInternalServerError)
        return
    }
    writeJSON(w, http.StatusOK, map[string]any{"snapshot": snapshot})
}
```

```go
// internal/admin/repository.go
func (r Repository) ListAnalysisRuntimeHistory(ctx context.Context, stage string, limit int) ([]AnalysisRuntimeHistoryPoint, error) {
    rows, err := r.db.Query(ctx, `
SELECT sampled_at::text, queue_depth, oldest_pending_age_seconds, queue_wait_p95_ms, processing_p95_ms
FROM analysis_runtime_samples
WHERE stage = $1
ORDER BY sampled_at DESC
LIMIT $2`, stage, limit)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    items := []AnalysisRuntimeHistoryPoint{}
    for rows.Next() {
        var item AnalysisRuntimeHistoryPoint
        if err := rows.Scan(&item.SampledAt, &item.QueueDepth, &item.OldestPendingAgeSeconds, &item.QueueWaitP95MS, &item.ProcessingP95MS); err != nil {
            return nil, err
        }
        items = append(items, item)
    }
    return items, rows.Err()
}
```

```javascript
// internal/adminui/app.js
const views = [
  { id: "overview", label: "概览" },
  { id: "analysis-runtime", label: "分析运行" },
  // existing views
];

async function loadView() {
  if (state.view === "analysis-runtime") {
    const [snapshotBody, historyBody, consumersBody] = await Promise.all([
      api("/analysis-runtime?stage=core"),
      api("/analysis-runtime/history?stage=core&range=1h"),
      api("/analysis-runtime/consumers?stage=core"),
    ]);
    renderAnalysisRuntime(snapshotBody, historyBody, consumersBody);
    return;
  }
  // existing branches
}

function renderAnalysisRuntime(snapshotBody, historyBody, consumersBody) {
  const snapshot = snapshotBody.snapshot || {};
  const cards = [
    ["Core Queue Depth", formatNumber(snapshot.queue_depth)],
    ["Oldest Pending (s)", formatNumber(snapshot.oldest_pending_age_seconds)],
    ["Leased", formatNumber(snapshot.leased_count)],
    ["Throughput/min", formatNumber(snapshot.throughput_per_minute)],
    ["Queue Wait P95", `${formatNumber(snapshot.queue_wait_p95_ms)} ms`],
    ["Processing P95", `${formatNumber(snapshot.processing_p95_ms)} ms`],
  ];
  const cardsHTML = cards
    .map(([label, value]) => `<div class="metric-card"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value)}</strong></div>`)
    .join("");
  const rows = arrayValue((consumersBody || {}).consumers).map((item) => [
    item.worker_id,
    item.stage,
    formatNumber(item.leased_count),
    formatTime(item.last_seen_at),
    formatNumber(item.idle_seconds),
    item.last_error_code || "无",
  ]);
  renderShell(page("分析运行", `<section class="panel"><div class="metric-grid">${cardsHTML}</div></section><section class="panel">${table(["Worker", "Stage", "Leased", "Last Seen", "Idle (s)", "Last Error"], rows)}</section>`));
}
```

- [ ] **Step 4: Re-run the targeted Go tests and manually verify the UI view**

Run:

```bash
cd /Users/roy/codes/new-api-gateway
go test ./internal/admin ./cmd/audit-gateway -run 'TestAnalysisRuntimeSnapshotHandlerReturnsCoreMetrics'
```

Expected:

- PASS for the new runtime handler test

Manual verification:

```bash
make run
```

Then open the admin UI and confirm:

- A new `分析运行` tab appears
- Core queue cards render
- Consumer table renders with runtime data

- [ ] **Step 5: Commit the admin runtime dashboard**

```bash
cd /Users/roy/codes/new-api-gateway
git add internal/admin/runtime.go internal/admin/handlers.go internal/admin/repository.go internal/admin/models.go internal/admin/handlers_test.go internal/admin/repository_test.go internal/adminui/app.js cmd/audit-gateway/main.go
git commit -m "feat: add analysis runtime admin dashboard"
```

## Task 7: Update Docs And Run Full Verification

**Files:**
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`
- Modify: `docs/superpowers/specs/2026-06-03-analysis-streams-throughput-design.md` only if implementation discovered a mismatch

- [ ] **Step 1: Write the doc updates**

```md
# README.md additions
- Gateway now publishes lightweight `trace_id` envelopes to Redis Streams `analysis.core`.
- Core analysis and enrichment run as separate worker stages.
- Admin UI includes an `分析运行` page for queue depth, throughput, and consumer health.
```

```md
# ARCHITECTURE.md additions
- Redis communication uses Streams and consumer groups instead of `analysis_jobs` list.
- `analysis_tasks` owns leasing and retry state.
- `trace_usage_facts` is the hot-path persistence table; `usage_aggregates` is derived asynchronously.
```

- [ ] **Step 2: Run the full worker and Go test suites**

Run:

```bash
cd /Users/roy/codes/new-api-gateway
go test ./...
```

Expected:

- PASS for gateway, admin, ops, and jobs packages

Run:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run pytest -q
```

Expected:

- PASS for worker models, repository, pipeline, offline, and media tests

- [ ] **Step 3: Run targeted end-to-end verification**

Run:

```bash
cd /Users/roy/codes/new-api-gateway
make dev -d
make run
```

In another shell:

```bash
cd /Users/roy/codes/new-api-gateway/workers/analysis_worker
uv run python main.py --redis-url redis://localhost:6379/0 --postgres-dsn "$POSTGRES_DSN"
```

Expected:

- Gateway requests enqueue to `analysis.core`
- Core worker consumes and marks traces `core_status=completed`
- Traces requiring enrichment enqueue to `analysis.enrichment`
- Admin `分析运行` page shows queue depth and throughput data

- [ ] **Step 4: Commit the docs and verification updates**

```bash
cd /Users/roy/codes/new-api-gateway
git add README.md ARCHITECTURE.md
git commit -m "docs: document analysis streams runtime architecture"
```

## Spec Coverage Check

- Streams core/enrichment/dlq topology is implemented by Task 2, Task 3, and Task 4.
- `analysis_tasks`, `trace_usage_facts`, stage fields, and immutable evidence rules are implemented by Task 1, Task 3, Task 4, and Task 5.
- Async rollup and baseline rebuild are implemented by Task 5.
- Admin runtime APIs and UI metrics are implemented by Task 6.
- README and ARCHITECTURE sync is implemented by Task 7.

## Placeholder Check

- No task says “add tests later”; every task names exact test files and snippets.
- No task uses `TBD`, `TODO`, or “appropriate handling” wording without concrete code or commands.
- Every command includes an exact path and expected result.

## Type Consistency Check

- Queue stages use only `core` and `enrichment`.
- Task states use only `queued`, `leased`, `succeeded`, `failed_retryable`, and `failed_terminal`.
- Trace summary states use only `pending`, `processing`, `completed`, `failed`, and `not_required`.
- Runtime API structs use `AnalysisRuntimeSnapshot`, `AnalysisRuntimeHistoryPoint`, and `AnalysisRuntimeConsumer` consistently across handler, repository, and UI tasks.
