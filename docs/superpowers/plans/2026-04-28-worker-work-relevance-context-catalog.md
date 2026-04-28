# Worker Work Relevance and Context Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an admin-maintained context catalog data model and have the Python worker persist deterministic work relevance results for each analyzed trace.

**Architecture:** Keep the Go gateway unchanged and extend the Python analysis worker after protocol normalization. The worker loads active `context_catalog` rows from PostgreSQL, classifies normalized messages with deterministic keyword/category matching, writes a `work_relevance` `analysis_results` row, and emits a review-ready `low_work_relevance_high_cost` anomaly for high-token personal/entertainment traces.

**Tech Stack:** PostgreSQL migrations, Python 3.11 dataclasses, `psycopg`, `pytest`, existing worker repository pattern, existing Docker Compose Postgres/Redis services.

---

## Completed Context Check

The approved design in `docs/superpowers/specs/2026-04-25-new-api-gateway-audit-design.md` calls for:

- `context_catalog` as the admin-maintained source of projects, repos, services, keywords, aliases, expected categories, models, and usage levels.
- Work relevance classification from normalized request/response text, route/protocol/model, employee identity, token identity, context catalog, and later review feedback.
- Initial task categories including coding, debugging, documentation, research, operations, personal_chat, entertainment, and unknown.
- A later anomaly rule for low work relevance plus high cost.

Implemented slices already provide:

- Gateway capture, identity, route coverage, raw/header evidence, trace rows, and Redis `trace_captured` jobs.
- Python worker evidence loading, JSON normalization, usage extraction result persistence, hourly/daily usage aggregates.
- Rule anomalies and worker-generated coverage alerts.
- Docker Compose Postgres/Redis services plus an e2e worker anomaly/coverage script.

This plan intentionally does not implement Admin API, Web UI, RBAC, review decision screens, embeddings/RAG, statistical baselines, or semantic duplicate detection. Those remain follow-on plans.

## File Structure

- Create `migrations/0005_context_catalog_work_relevance.sql`: context catalog table, indexes, and the new anomaly rule seed.
- Modify `workers/analysis_worker/models.py`: add `ContextCatalogEntry` and `WorkRelevanceAssessment` dataclasses.
- Create `workers/analysis_worker/tests/test_context_repository.py`: fake-connection tests for loading active context rows.
- Create `workers/analysis_worker/context_repository.py`: PostgreSQL reader for active context catalog entries.
- Create `workers/analysis_worker/tests/test_work_relevance.py`: deterministic classifier tests.
- Create `workers/analysis_worker/work_relevance.py`: deterministic context/category classifier and result-to-analysis conversion.
- Modify `workers/analysis_worker/rules.py`: add low-work-relevance/high-token anomaly detection.
- Modify `workers/analysis_worker/tests/test_rules.py`: assert the new anomaly rule.
- Modify `workers/analysis_worker/main.py`: load contexts, run work relevance after normalization, include counts in worker status output, and pass the work relevance result to anomaly detection.
- Modify `workers/analysis_worker/tests/test_pipeline.py`: assert work relevance results are persisted and can trigger the new anomaly.
- Create `scripts/e2e_worker_work_relevance.sh`: Docker Compose e2e check for migration, seeded context, Redis job, worker result, and anomaly row.
- Modify `docs/development.md`: document context catalog and work relevance worker outputs.

---

### Task 1: Context Catalog Schema

**Files:**
- Create: `migrations/0005_context_catalog_work_relevance.sql`

- [ ] **Step 1: Write the migration**

Create `migrations/0005_context_catalog_work_relevance.sql`:

```sql
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
```

- [ ] **Step 2: Verify migration text has expected objects**

Run:

```bash
rg -n "CREATE TABLE IF NOT EXISTS context_catalog|low_work_relevance_high_cost|USING GIN" migrations/0005_context_catalog_work_relevance.sql
```

Expected: four matches: one table, two GIN indexes, and one anomaly rule seed.

- [ ] **Step 3: Verify migration executes in Docker Compose**

Run:

```bash
E2E_DB=audit_gateway_plan_0005 ./scripts/e2e_worker_anomaly_coverage.sh
```

Expected: PASS. This existing e2e script recreates a dedicated database and applies all migrations, so it must include `0005_context_catalog_work_relevance.sql`.

- [ ] **Step 4: Commit**

```bash
git add migrations/0005_context_catalog_work_relevance.sql
git commit -m "feat: add context catalog schema"
```

---

### Task 2: Worker Context Models and Repository

**Files:**
- Modify: `workers/analysis_worker/models.py`
- Create: `workers/analysis_worker/context_repository.py`
- Create: `workers/analysis_worker/tests/test_context_repository.py`
- Test: `workers/analysis_worker/tests/test_models.py`

- [ ] **Step 1: Add model tests first**

Append to `workers/analysis_worker/tests/test_models.py`:

```python
from models import ContextCatalogEntry, WorkRelevanceAssessment


def test_context_catalog_entry_normalizes_keyword_lists():
    entry = ContextCatalogEntry(
        id=1,
        context_type="repo",
        name="new-api-gateway",
        description="Audit gateway for new-api",
        keywords=["Gateway", " new-api ", ""],
        aliases=["audit gateway", "Gateway"],
        owner="platform",
        expected_task_categories=["coding", "debugging"],
        expected_models=["gpt-4.1"],
        expected_usage_level="normal",
        active=True,
    )

    assert entry.search_terms() == ["gateway", "new-api", "audit gateway", "new-api-gateway"]


def test_work_relevance_assessment_converts_to_analysis_result():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_1",
        task_category="coding",
        work_related_score=0.82,
        personal_use_score=0.05,
        confidence=0.74,
        matched_context=[{"type": "repo", "name": "new-api-gateway", "matched_terms": ["gateway"]}],
        evidence=["Request matched repo context."],
        needs_review=False,
        analyzer_version="work_relevance_mvp_2026_04_28",
    )

    result = assessment.to_analysis_result()

    assert result.category == "work_relevance"
    assert result.label == "coding"
    assert result.score == 0.82
    assert result.confidence == 0.74
    assert result.result["matched_context"][0]["name"] == "new-api-gateway"
```

- [ ] **Step 2: Run model tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_models.py -q
```

Expected: FAIL because `ContextCatalogEntry` and `WorkRelevanceAssessment` do not exist.

- [ ] **Step 3: Add dataclasses**

Modify `workers/analysis_worker/models.py` after `CoverageAlert`:

```python
@dataclass(frozen=True)
class ContextCatalogEntry:
    id: int
    context_type: str
    name: str
    description: str
    keywords: list[str]
    aliases: list[str]
    owner: str
    expected_task_categories: list[str]
    expected_models: list[str]
    expected_usage_level: str
    active: bool

    def search_terms(self) -> list[str]:
        seen: set[str] = set()
        terms: list[str] = []
        for value in [*self.keywords, *self.aliases, self.name]:
            normalized = value.strip().lower()
            if normalized and normalized not in seen:
                seen.add(normalized)
                terms.append(normalized)
        return terms


@dataclass(frozen=True)
class WorkRelevanceAssessment:
    trace_id: str
    task_category: str
    work_related_score: float
    personal_use_score: float
    confidence: float
    matched_context: list[dict[str, Any]]
    evidence: list[str]
    needs_review: bool
    analyzer_version: str

    def to_analysis_result(self) -> AnalysisResult:
        return AnalysisResult(
            trace_id=self.trace_id,
            analyzer_name="work_relevance",
            analyzer_version=self.analyzer_version,
            policy_version="",
            category="work_relevance",
            label=self.task_category,
            score=self.work_related_score,
            confidence=self.confidence,
            severity="review" if self.needs_review else "",
            result={
                "task_category": self.task_category,
                "work_related_score": self.work_related_score,
                "personal_use_score": self.personal_use_score,
                "confidence": self.confidence,
                "matched_context": self.matched_context,
                "evidence": self.evidence,
                "needs_review": self.needs_review,
            },
        )
```

- [ ] **Step 4: Add context repository tests first**

Create `workers/analysis_worker/tests/test_context_repository.py`:

```python
from context_repository import PostgresContextRepository


class FakeCursor:
    def __init__(self):
        self.executed = []
        self.rows = [
            (
                1,
                "repo",
                "new-api-gateway",
                "Audit gateway",
                ["new-api", "gateway"],
                ["audit gateway"],
                "platform",
                ["coding", "debugging"],
                ["gpt-4.1"],
                "normal",
                True,
            )
        ]

    def execute(self, query, params=None):
        self.executed.append((query, params))

    def fetchall(self):
        return self.rows


class FakeConnection:
    def __init__(self):
        self.cursor_obj = FakeCursor()

    def cursor(self):
        return self.cursor_obj


def test_list_active_contexts_returns_catalog_entries():
    conn = FakeConnection()
    repo = PostgresContextRepository(conn)

    contexts = repo.list_active_contexts()

    assert contexts[0].name == "new-api-gateway"
    assert contexts[0].search_terms() == ["new-api", "gateway", "audit gateway", "new-api-gateway"]
    query = conn.cursor_obj.executed[0][0]
    assert "FROM context_catalog" in query
    assert "WHERE active = TRUE" in query
```

- [ ] **Step 5: Run context repository tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_context_repository.py -q
```

Expected: FAIL because `context_repository.py` does not exist.

- [ ] **Step 6: Create context repository**

Create `workers/analysis_worker/context_repository.py`:

```python
from models import ContextCatalogEntry


class PostgresContextRepository:
    def __init__(self, connection):
        self.connection = connection

    def list_active_contexts(self) -> list[ContextCatalogEntry]:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            SELECT
                id, context_type, name, description, keywords, aliases, owner,
                expected_task_categories, expected_models, expected_usage_level, active
            FROM context_catalog
            WHERE active = TRUE
            ORDER BY context_type, name
            """
        )
        return [
            ContextCatalogEntry(
                id=row[0],
                context_type=row[1],
                name=row[2],
                description=row[3],
                keywords=list(row[4] or []),
                aliases=list(row[5] or []),
                owner=row[6],
                expected_task_categories=list(row[7] or []),
                expected_models=list(row[8] or []),
                expected_usage_level=row[9],
                active=row[10],
            )
            for row in cursor.fetchall()
        ]
```

- [ ] **Step 7: Run model and context repository tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_models.py tests/test_context_repository.py -q
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add workers/analysis_worker/models.py workers/analysis_worker/context_repository.py workers/analysis_worker/tests/test_models.py workers/analysis_worker/tests/test_context_repository.py
git commit -m "feat: load worker context catalog"
```

---

### Task 3: Deterministic Work Relevance Classifier

**Files:**
- Create: `workers/analysis_worker/work_relevance.py`
- Create: `workers/analysis_worker/tests/test_work_relevance.py`

- [ ] **Step 1: Write classifier tests first**

Create `workers/analysis_worker/tests/test_work_relevance.py`:

```python
from models import ContextCatalogEntry, NormalizedMessage, TraceCapturedJob
from work_relevance import ANALYZER_VERSION, classify_work_relevance


def job(**overrides):
    values = {
        "type": "trace_captured",
        "trace_id": "trace_1",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "employee_no": "E10001",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 1200,
    }
    values.update(overrides)
    return TraceCapturedJob(**values)


def message(text: str) -> NormalizedMessage:
    return NormalizedMessage(
        trace_id="trace_1",
        direction="request",
        sequence_index=0,
        role="user",
        modality="text",
        content_text=text,
        content_text_hash="hash",
        media_url="",
        source_path="request.messages[0]",
        protocol_item_type="openai_chat_message",
        token_count_estimate=10,
        metadata={},
    )


def context() -> ContextCatalogEntry:
    return ContextCatalogEntry(
        id=1,
        context_type="repo",
        name="new-api-gateway",
        description="Audit gateway",
        keywords=["new-api", "gateway", "audit"],
        aliases=["relay"],
        owner="platform",
        expected_task_categories=["coding", "debugging", "documentation"],
        expected_models=["gpt-4.1"],
        expected_usage_level="normal",
        active=True,
    )


def test_classifies_context_matched_coding_as_work_related():
    assessment = classify_work_relevance(
        job(),
        [message("Debug the new-api gateway relay route and write tests.")],
        [context()],
    )

    assert assessment.task_category == "debugging"
    assert assessment.work_related_score == 0.9
    assert assessment.personal_use_score == 0.02
    assert assessment.confidence >= 0.75
    assert assessment.needs_review is False
    assert assessment.analyzer_version == ANALYZER_VERSION
    assert assessment.matched_context[0]["name"] == "new-api-gateway"


def test_classifies_personal_chat_as_review_needed():
    assessment = classify_work_relevance(
        job(),
        [message("Write a funny birthday party toast for my friend.")],
        [context()],
    )

    assert assessment.task_category == "personal_chat"
    assert assessment.work_related_score == 0.1
    assert assessment.personal_use_score == 0.8
    assert assessment.needs_review is True
    assert assessment.matched_context == []


def test_empty_messages_are_unknown_and_low_confidence():
    assessment = classify_work_relevance(job(), [], [context()])

    assert assessment.task_category == "unknown"
    assert assessment.work_related_score == 0.0
    assert assessment.personal_use_score == 0.0
    assert assessment.confidence == 0.1
    assert assessment.needs_review is True
```

- [ ] **Step 2: Run classifier tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -q
```

Expected: FAIL because `work_relevance.py` does not exist.

- [ ] **Step 3: Implement classifier**

Create `workers/analysis_worker/work_relevance.py`:

```python
from models import ContextCatalogEntry, NormalizedMessage, TraceCapturedJob, WorkRelevanceAssessment


ANALYZER_VERSION = "work_relevance_mvp_2026_04_28"

TASK_KEYWORDS = {
    "debugging": ["debug", "bug", "error", "failure", "stack trace", "regression", "fix"],
    "coding": ["code", "implement", "function", "class", "api", "test", "refactor", "sql"],
    "code_review": ["review", "pull request", "diff", "regression risk"],
    "documentation": ["document", "readme", "docs", "write-up", "guide"],
    "data_analysis": ["analyze", "csv", "spreadsheet", "metric", "dashboard"],
    "operations": ["deploy", "incident", "oncall", "alert", "runbook"],
    "research": ["research", "compare", "evaluate", "investigate"],
    "product_design": ["product", "requirements", "user story", "wireframe"],
    "translation": ["translate", "localize"],
    "meeting_summary": ["meeting", "minutes", "summary"],
    "customer_support": ["customer", "support ticket", "refund"],
    "personal_chat": ["birthday", "friend", "dating", "vacation", "party", "personal"],
    "entertainment": ["game", "joke", "lyrics", "movie", "roleplay"],
}

WORK_CATEGORIES = {
    "coding",
    "debugging",
    "code_review",
    "documentation",
    "translation",
    "data_analysis",
    "meeting_summary",
    "product_design",
    "customer_support",
    "research",
    "operations",
}

PERSONAL_CATEGORIES = {"personal_chat", "entertainment"}


def classify_work_relevance(
    job: TraceCapturedJob,
    messages: list[NormalizedMessage],
    contexts: list[ContextCatalogEntry],
) -> WorkRelevanceAssessment:
    text = _combined_text(messages)
    if not text:
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category="unknown",
            work_related_score=0.0,
            personal_use_score=0.0,
            confidence=0.1,
            matched_context=[],
            evidence=["No normalized text was available for work relevance classification."],
            needs_review=True,
            analyzer_version=ANALYZER_VERSION,
        )

    category, category_terms = _detect_category(text)
    matched_context = _match_contexts(text, contexts)

    if category in PERSONAL_CATEGORIES:
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=0.1,
            personal_use_score=0.8,
            confidence=0.8,
            matched_context=matched_context,
            evidence=[f"Detected personal category terms: {', '.join(category_terms)}."],
            needs_review=True,
            analyzer_version=ANALYZER_VERSION,
        )

    if matched_context and category in WORK_CATEGORIES:
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=0.9,
            personal_use_score=0.02,
            confidence=0.8,
            matched_context=matched_context,
            evidence=[f"Matched catalog context and work category {category}."],
            needs_review=False,
            analyzer_version=ANALYZER_VERSION,
        )

    if matched_context:
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=0.72,
            personal_use_score=0.05,
            confidence=0.62,
            matched_context=matched_context,
            evidence=["Matched catalog context but task category was weak or unknown."],
            needs_review=False,
            analyzer_version=ANALYZER_VERSION,
        )

    if category in WORK_CATEGORIES:
        return WorkRelevanceAssessment(
            trace_id=job.trace_id,
            task_category=category,
            work_related_score=0.55,
            personal_use_score=0.08,
            confidence=0.45,
            matched_context=[],
            evidence=[f"Detected work category {category} without catalog context."],
            needs_review=True,
            analyzer_version=ANALYZER_VERSION,
        )

    return WorkRelevanceAssessment(
        trace_id=job.trace_id,
        task_category="unknown",
        work_related_score=0.25,
        personal_use_score=0.1,
        confidence=0.2,
        matched_context=[],
        evidence=["No catalog context or known task category matched."],
        needs_review=True,
        analyzer_version=ANALYZER_VERSION,
    )


def _combined_text(messages: list[NormalizedMessage]) -> str:
    return "\n".join(message.content_text for message in messages if message.content_text).lower()


def _detect_category(text: str) -> tuple[str, list[str]]:
    best_category = "unknown"
    best_terms: list[str] = []
    for category, terms in TASK_KEYWORDS.items():
        matched = [term for term in terms if term in text]
        if len(matched) > len(best_terms):
            best_category = category
            best_terms = matched
    return best_category, best_terms


def _match_contexts(text: str, contexts: list[ContextCatalogEntry]) -> list[dict[str, object]]:
    matches: list[dict[str, object]] = []
    for context in contexts:
        matched_terms = [term for term in context.search_terms() if term in text]
        if matched_terms:
            matches.append({
                "type": context.context_type,
                "name": context.name,
                "matched_terms": matched_terms,
            })
    return matches
```

- [ ] **Step 4: Run classifier tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -q
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/work_relevance.py workers/analysis_worker/tests/test_work_relevance.py
git commit -m "feat: classify work relevance from context catalog"
```

---

### Task 4: Worker Pipeline Integration

**Files:**
- Modify: `workers/analysis_worker/main.py`
- Modify: `workers/analysis_worker/tests/test_pipeline.py`

- [ ] **Step 1: Extend pipeline tests first**

Modify `RecordingRepository` in `workers/analysis_worker/tests/test_pipeline.py` so it can provide contexts:

```python
from models import ContextCatalogEntry


class RecordingContextRepository:
    def __init__(self, contexts=None):
        self.contexts = list(contexts or [])

    def list_active_contexts(self):
        return self.contexts
```

Add this test:

```python
def test_process_job_line_persists_work_relevance_result(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_work"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text(json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "Debug the new-api gateway route tests"}]
    }), encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text(json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "Check the route registry test."}}],
        "usage": {"total_tokens": 100}
    }), encoding="utf-8")
    repo = RecordingRepository()
    contexts = RecordingContextRepository([ContextCatalogEntry(
        id=1,
        context_type="repo",
        name="new-api-gateway",
        description="Audit gateway",
        keywords=["new-api", "gateway"],
        aliases=[],
        owner="platform",
        expected_task_categories=["coding", "debugging"],
        expected_models=["gpt-4.1"],
        expected_usage_level="normal",
        active=True,
    )])
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_work",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "employee_no": "E10001",
        "request_raw_ref": "raw/2026/04/28/trace_work/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_work/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 100,
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(line, FileEvidenceStore(tmp_path), repo, contexts)

    assert response["work_relevance_count"] == 1
    work_results = [result for result in repo.results if result.category == "work_relevance"]
    assert len(work_results) == 1
    assert work_results[0].label == "debugging"
    assert work_results[0].result["work_related_score"] == 0.9
```

- [ ] **Step 2: Run pipeline test and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_pipeline.py::test_process_job_line_persists_work_relevance_result -q
```

Expected: FAIL because `process_job_line` does not accept a context repository and no `work_relevance_count` exists.

- [ ] **Step 3: Integrate context loading and classification**

Modify imports in `workers/analysis_worker/main.py`:

```python
from context_repository import PostgresContextRepository
from models import ContextCatalogEntry, TraceCapturedJob, UsageAggregateDelta, bucket_start_day, bucket_start_hour, parse_job
from work_relevance import classify_work_relevance
```

Modify `NoopAnalysisRepository` area:

```python
class NoopContextRepository:
    def list_active_contexts(self) -> list[ContextCatalogEntry]:
        return []
```

Modify `process_job_line`, `process_contract_validation_line`, and `process_trace`:

```python
def process_job_line(line: str, evidence_store: FileEvidenceStore, repository, context_repository=None) -> dict:
    job = parse_job(line)
    request_body = evidence_store.read_text(job.request_raw_ref) if job.request_raw_ref else ""
    response_body = evidence_store.read_text(job.response_raw_ref) if job.response_raw_ref else ""
    contexts = context_repository.list_active_contexts() if context_repository else []
    return process_trace(job, request_body, response_body, repository, contexts)


def process_contract_validation_line(line: str) -> dict:
    job = parse_job(line)
    return process_trace(job, "", "", NoopAnalysisRepository(), [])


def process_trace(
    job: TraceCapturedJob,
    request_body: str,
    response_body: str,
    repository,
    contexts: list[ContextCatalogEntry] | None = None,
) -> dict:
    messages, results = normalize_json_trace(job, request_body, response_body)
    work_relevance = classify_work_relevance(job, messages, list(contexts or []))
    results.append(work_relevance.to_analysis_result())
    aggregates = aggregate_deltas(job)
    anomalies = detect_anomalies(job)
    coverage_alerts = detect_coverage_alerts(job, messages)
    repository.save_trace_analysis(messages, results, aggregates, anomalies, coverage_alerts)
    return {
        "accepted_trace_id": job.trace_id,
        "worker_status": "processed",
        "normalized_message_count": len(messages),
        "analysis_result_count": len(results),
        "work_relevance_count": 1,
        "aggregate_count": len(aggregates),
        "anomaly_count": len(anomalies),
        "coverage_alert_count": len(coverage_alerts),
        "usage_total_tokens": job.usage_total_tokens,
    }
```

Modify DB-backed callers:

```python
with psycopg.connect(postgres_dsn) as connection:
    result = process_job_line(
        payload,
        FileEvidenceStore(evidence_root),
        PostgresAnalysisRepository(connection),
        PostgresContextRepository(connection),
    )
```

Apply that same `PostgresContextRepository(connection)` pattern in both `process_stdin` and `process_redis_once`.

- [ ] **Step 4: Update existing pipeline assertions**

In `workers/analysis_worker/tests/test_pipeline.py`, update assertions that expected one analysis result:

```python
assert response["analysis_result_count"] == 2
assert len(repo.results) == 2
assert [result.category for result in repo.results] == ["usage_extraction", "work_relevance"]
```

In `test_contract_example_processes_from_stdin_without_services`, add:

```python
assert response["work_relevance_count"] == 1
assert response["analysis_result_count"] == 2
```

- [ ] **Step 5: Run pipeline tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_pipeline.py -q
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/main.py workers/analysis_worker/tests/test_pipeline.py
git commit -m "feat: run work relevance in analysis worker"
```

---

### Task 5: Low Work Relevance High Cost Anomaly

**Files:**
- Modify: `workers/analysis_worker/rules.py`
- Modify: `workers/analysis_worker/tests/test_rules.py`
- Modify: `workers/analysis_worker/main.py`
- Modify: `workers/analysis_worker/tests/test_pipeline.py`

- [ ] **Step 1: Add rule tests first**

Append to `workers/analysis_worker/tests/test_rules.py`:

```python
from models import WorkRelevanceAssessment
from rules import detect_work_relevance_anomalies


def test_detects_low_work_relevance_high_cost_anomaly():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_personal",
        task_category="personal_chat",
        work_related_score=0.1,
        personal_use_score=0.8,
        confidence=0.8,
        matched_context=[],
        evidence=["Detected personal category terms: birthday."],
        needs_review=True,
        analyzer_version="work_relevance_mvp_2026_04_28",
    )
    alerts = detect_work_relevance_anomalies(
        job(trace_id="trace_personal", usage_total_tokens=25000, model_requested="gpt-4.1"),
        assessment,
    )

    assert len(alerts) == 1
    assert alerts[0].anomaly_type == "low_work_relevance_high_cost"
    assert alerts[0].severity == "high"
    assert alerts[0].observed_value == 25000
    assert alerts[0].threshold_value == 20000
    assert "personal use score" in alerts[0].reason


def test_does_not_detect_low_work_relevance_high_cost_for_low_tokens():
    assessment = WorkRelevanceAssessment(
        trace_id="trace_personal",
        task_category="personal_chat",
        work_related_score=0.1,
        personal_use_score=0.8,
        confidence=0.8,
        matched_context=[],
        evidence=[],
        needs_review=True,
        analyzer_version="work_relevance_mvp_2026_04_28",
    )

    assert detect_work_relevance_anomalies(job(usage_total_tokens=100), assessment) == []
```

- [ ] **Step 2: Run rule tests and verify failure**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_rules.py::test_detects_low_work_relevance_high_cost_anomaly -q
```

Expected: FAIL because `detect_work_relevance_anomalies` does not exist.

- [ ] **Step 3: Implement rule**

Modify imports in `workers/analysis_worker/rules.py`:

```python
    WorkRelevanceAssessment,
```

Add constants:

```python
LOW_WORK_RELEVANCE_TOKEN_THRESHOLD = 20_000
LOW_WORK_RELEVANCE_PERSONAL_SCORE_THRESHOLD = 0.6
```

Add function after `detect_anomalies`:

```python
def detect_work_relevance_anomalies(
    job: TraceCapturedJob,
    assessment: WorkRelevanceAssessment,
) -> list[AnomalyAlert]:
    if job.usage_total_tokens < LOW_WORK_RELEVANCE_TOKEN_THRESHOLD:
        return []
    if assessment.personal_use_score < LOW_WORK_RELEVANCE_PERSONAL_SCORE_THRESHOLD:
        return []
    return [_anomaly(
        job,
        "low_work_relevance_high_cost",
        "high",
        observed_value=job.usage_total_tokens,
        threshold_value=LOW_WORK_RELEVANCE_TOKEN_THRESHOLD,
        reason=(
            f"trace used {job.usage_total_tokens} tokens with personal use score "
            f"{assessment.personal_use_score:.2f}"
        ),
    )]
```

- [ ] **Step 4: Integrate rule in pipeline**

Modify imports in `workers/analysis_worker/main.py`:

```python
from rules import detect_anomalies, detect_coverage_alerts, detect_work_relevance_anomalies
```

Modify anomaly generation in `process_trace`:

```python
anomalies = [
    *detect_anomalies(job),
    *detect_work_relevance_anomalies(job, work_relevance),
]
```

- [ ] **Step 5: Add pipeline coverage for the new anomaly**

Append to `workers/analysis_worker/tests/test_pipeline.py`:

```python
def test_process_job_line_detects_low_work_relevance_high_cost(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_personal"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text(json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "Write a funny birthday party toast for my friend."}]
    }), encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text(json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "Here is a toast."}}],
        "usage": {"total_tokens": 25000}
    }), encoding="utf-8")
    repo = RecordingRepository()
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_personal",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "employee_no": "E10001",
        "request_raw_ref": "raw/2026/04/28/trace_personal/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_personal/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 25000,
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(line, FileEvidenceStore(tmp_path), repo, RecordingContextRepository())

    assert response["anomaly_count"] == 2
    assert [alert.anomaly_type for alert in repo.anomalies] == [
        "high_trace_tokens",
        "low_work_relevance_high_cost",
    ]
```

- [ ] **Step 6: Run rule and pipeline tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_rules.py tests/test_pipeline.py -q
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add workers/analysis_worker/rules.py workers/analysis_worker/main.py workers/analysis_worker/tests/test_rules.py workers/analysis_worker/tests/test_pipeline.py
git commit -m "feat: alert on high cost personal usage"
```

---

### Task 6: Repository and End-to-End Verification

**Files:**
- Modify: `workers/analysis_worker/tests/test_repository.py`
- Create: `scripts/e2e_worker_work_relevance.sh`
- Modify: `docs/development.md`

- [ ] **Step 1: Extend repository test assertions**

Modify `workers/analysis_worker/tests/test_repository.py` in `test_repository_inserts_messages_results_aggregates_anomalies_and_coverage` so it includes a work relevance result:

```python
work_relevance_result = AnalysisResult(
    trace_id="trace_1",
    analyzer_name="work_relevance",
    analyzer_version="work_relevance_mvp_2026_04_28",
    policy_version="",
    category="work_relevance",
    label="debugging",
    score=0.9,
    confidence=0.8,
    severity="",
    result={
        "task_category": "debugging",
        "work_related_score": 0.9,
        "personal_use_score": 0.02,
        "confidence": 0.8,
        "matched_context": [{"type": "repo", "name": "new-api-gateway", "matched_terms": ["gateway"]}],
        "evidence": ["Matched catalog context and work category debugging."],
        "needs_review": False,
    },
)
```

Call:

```python
repo.save_trace_analysis([message], [result, work_relevance_result], [aggregate], [anomaly], [coverage])
```

Add assertion:

```python
analysis_queries = [
    query for query, _ in conn.cursor_obj.executed if "INSERT INTO analysis_results" in query
]
assert len(analysis_queries) == 2
```

- [ ] **Step 2: Run repository tests**

Run:

```bash
cd workers/analysis_worker && uv run pytest tests/test_repository.py -q
```

Expected: PASS because work relevance reuses existing `analysis_results` persistence.

- [ ] **Step 3: Create Docker Compose e2e script**

Create `scripts/e2e_worker_work_relevance.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/deploy/docker-compose.yml}"
readonly EVIDENCE_ROOT="${EVIDENCE_ROOT:-$REPO_ROOT/var/e2e-work-relevance-evidence}"
readonly E2E_DB="${E2E_DB:-audit_gateway_work_relevance_e2e}"
readonly E2E_POSTGRES_DSN="postgres://audit:audit@postgres:5432/$E2E_DB?sslmode=disable"
cd "$REPO_ROOT"

if [[ ! "$E2E_DB" =~ ^[A-Za-z0-9_]+$ ]]; then
  echo "E2E_DB must contain only letters, numbers, and underscores" >&2
  exit 1
fi

docker compose -f "$COMPOSE_FILE" up -d postgres redis

until docker compose -f "$COMPOSE_FILE" exec -T postgres pg_isready -U audit -d audit_gateway >/dev/null; do
  sleep 1
done

docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d postgres -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS $E2E_DB WITH (FORCE);"
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d postgres -v ON_ERROR_STOP=1 \
  -c "CREATE DATABASE $E2E_DB;"

POSTGRES_DB="$E2E_DB" docker compose -f "$COMPOSE_FILE" run --rm migrate
docker compose -f "$COMPOSE_FILE" exec -T redis redis-cli FLUSHDB >/dev/null

rm -rf "$EVIDENCE_ROOT"
mkdir -p "$EVIDENCE_ROOT/raw/e2e/trace_work"
printf '{"model":"gpt-4.1","messages":[{"role":"user","content":"Debug the new-api gateway route tests"}]}\n' > "$EVIDENCE_ROOT/raw/e2e/trace_work/request_body.bin"
printf '{"choices":[{"message":{"role":"assistant","content":"Check the route registry tests."}}],"usage":{"total_tokens":1200}}\n' > "$EVIDENCE_ROOT/raw/e2e/trace_work/response_body.bin"

docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d "$E2E_DB" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
INSERT INTO context_catalog (
    context_type, name, description, keywords, aliases, owner,
    expected_task_categories, expected_models, expected_usage_level, created_by, updated_by
) VALUES (
    'repo', 'new-api-gateway', 'Audit gateway repository',
    ARRAY['new-api','gateway'], ARRAY['route registry'],
    'platform', ARRAY['coding','debugging'], ARRAY['gpt-4.1'], 'normal',
    'e2e', 'e2e'
);

INSERT INTO traces (
    trace_id, method, path, route_pattern, protocol_family, capture_mode,
    status_code, upstream_status_code, stream, request_started_at,
    request_body_size, response_body_size, request_raw_ref, response_raw_ref,
    token_fingerprint, fingerprint_display, new_api_token_id_snapshot,
    token_name_snapshot, employee_no_snapshot, identity_resolution_status,
    model_requested, usage_total_tokens
) VALUES (
    'trace_work', 'POST', '/v1/chat/completions', '/v1/chat/completions',
    'openai_chat', 'raw_and_normalized', 200, 200, false,
    '2026-04-28T13:45:22Z', 92, 112,
    'raw/e2e/trace_work/request_body.bin', 'raw/e2e/trace_work/response_body.bin',
    'tkfp_raw', 'tkfp_display', 42, 'E10001', 'E10001', 'resolved',
    'gpt-4.1', 1200
);
SQL

job_file="$(mktemp)"
trap 'rm -f "$job_file"' EXIT
cat > "$job_file" <<'JSON'
{
  "type": "trace_captured",
  "trace_id": "trace_work",
  "route_pattern": "/v1/chat/completions",
  "protocol_family": "openai_chat",
  "capture_mode": "raw_and_normalized",
  "employee_no": "E10001",
  "request_raw_ref": "raw/e2e/trace_work/request_body.bin",
  "response_raw_ref": "raw/e2e/trace_work/response_body.bin",
  "request_content_type": "application/json",
  "response_content_type": "application/json",
  "model_requested": "gpt-4.1",
  "usage_total_tokens": 1200,
  "token_fingerprint": "tkfp_raw",
  "fingerprint_display": "tkfp_display",
  "new_api_token_id": 42,
  "token_name_snapshot": "E10001",
  "identity_resolution_status": "resolved",
  "status_code": 200,
  "upstream_status_code": 200,
  "stream": false,
  "request_started_at": "2026-04-28T13:45:22Z",
  "request_body_size": 92,
  "response_body_size": 112
}
JSON

docker compose -f "$COMPOSE_FILE" exec -T redis redis-cli -x RPUSH analysis_jobs < "$job_file" >/dev/null

worker_output="$(
  EVIDENCE_STORAGE_DIR="$EVIDENCE_ROOT" ANALYSIS_WORKER_POSTGRES_DSN="$E2E_POSTGRES_DSN" \
    docker compose -f "$COMPOSE_FILE" run --rm analysis-worker uv run python main.py --redis-once
)"
echo "$worker_output"

python - "$worker_output" <<'PY'
import json
import sys

payload = json.loads(sys.argv[1])
if payload.get("worker_status") != "processed":
    raise SystemExit(payload)
if payload.get("work_relevance_count") != 1:
    raise SystemExit(payload)
PY

label="$(
  docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d "$E2E_DB" -At \
    -c "SELECT label FROM analysis_results WHERE trace_id = 'trace_work' AND category = 'work_relevance';"
)"

if [[ "$label" != "debugging" ]]; then
  echo "work relevance label=$label, want debugging" >&2
  exit 1
fi

docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U audit -d "$E2E_DB" \
  -c "SELECT trace_id, category, label, score, confidence, result_json FROM analysis_results WHERE trace_id = 'trace_work' ORDER BY category;"
```

- [ ] **Step 4: Make script executable and run e2e**

Run:

```bash
chmod +x scripts/e2e_worker_work_relevance.sh
./scripts/e2e_worker_work_relevance.sh
```

Expected: PASS and query output includes `category=work_relevance`, `label=debugging`, `score=0.9`.

- [ ] **Step 5: Document worker relevance outputs**

Modify `docs/development.md` under worker outputs:

````markdown
## Worker Work Relevance

The worker loads active `context_catalog` rows from PostgreSQL and writes a `work_relevance` row to `analysis_results` for every processed trace. The MVP classifier is deterministic: it matches normalized text against context keywords/aliases and a fixed task-category keyword map. Low-confidence, personal, or entertainment results set `needs_review` in `result_json`.

Run the Docker Compose work relevance check:

```bash
./scripts/e2e_worker_work_relevance.sh
```
````

- [ ] **Step 6: Run all verification**

Run:

```bash
go test ./...
cd workers/analysis_worker && uv run pytest -q
./scripts/e2e_worker_anomaly_coverage.sh
./scripts/e2e_worker_work_relevance.sh
git diff --check
```

Expected: all commands pass.

- [ ] **Step 7: Commit**

```bash
git add workers/analysis_worker/tests/test_repository.py scripts/e2e_worker_work_relevance.sh docs/development.md
git commit -m "test: add work relevance e2e coverage"
```

---

## Self-Review Checklist

- Spec coverage:
  - `context_catalog` table is covered by Task 1.
  - Work relevance classification inputs from normalized messages and context catalog are covered by Tasks 2-4.
  - Work relevance output shape from the approved design is covered by `WorkRelevanceAssessment.to_analysis_result`.
  - Low work relevance plus high token anomaly is covered by Task 5.
  - Docker Compose end-to-end validation is covered by Task 6.
- Scope exclusions:
  - Admin API/UI for managing contexts is intentionally deferred.
  - Review feedback loops, embeddings/RAG, baselines, semantic duplicate detection, and dashboards are intentionally deferred.
  - Gateway request path remains unchanged.
- Placeholder scan:
  - No placeholder markers or unspecified "add tests" steps remain.
- Type consistency:
  - `ContextCatalogEntry`, `WorkRelevanceAssessment`, `classify_work_relevance`, and `detect_work_relevance_anomalies` are consistently named across tasks.

## Execution Handoff

Plan complete when this file is saved. Recommended execution mode is subagent-driven because schema, classifier, pipeline, anomaly, and e2e tasks have clear boundaries and can be reviewed between commits.
