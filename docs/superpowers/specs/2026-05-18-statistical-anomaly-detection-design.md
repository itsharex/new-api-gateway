# Statistical Anomaly Detection Upgrade Design

Date: 2026-05-18

## Context

The Python analysis worker (`workers/analysis_worker/rules.py`) currently uses 13 fixed-threshold rules and 1 keyword-based classifier for anomaly detection. This MVP approach has two problems:

1. **High false positive rate**: Fixed thresholds (e.g., 20K tokens per trace) are too aggressive for heavy users and too lenient for light users.
2. **No multivariate detection**: Rules only check single dimensions independently; they cannot detect correlated anomalies (e.g., late-night usage of an expensive model with unusually high tokens).

## Goals

- Replace fixed thresholds with per-token-fingerprint statistical baselines derived from historical usage.
- Add multivariate anomaly detection using Isolation Forest for complex cross-dimensional patterns.
- Upgrade work relevance classification from keyword matching to semantic embedding similarity.
- Maintain the existing `AnomalyAlert` contract and PG schema so the Go gateway and admin UI require minimal changes.

## Constraints

- **Deployment**: All services managed via a single `docker-compose.yml`.
- **Scale**: Medium — 1K-50K traces/day, 100-1000 active API keys.
- **Latency**: Offline-first — batch analysis runs hourly, online trace processing uses cached baselines.
- **Alerting**: Write anomalies to PG only (current behavior), no new notification channels.
- **Backward compatibility**: New token fingerprints with no history must fall back to current fixed thresholds.

## Architecture

```
analysis_worker (existing process)
├── Online mode (current):
│     BLPOP → parse → normalize → rules → write PG
│
├── Offline mode (new):
│     --offline-batch → read PG history → compute baselines + train model → write baseline_cache + model_artifacts → exit
│
└── Online mode (upgraded):
      BLPOP → parse → normalize → rules_v2(baseline_cache) → write PG

docker-compose services:
  analysis-worker    # existing BLPOP consumer
  analysis-batch     # cron sidecar, hourly: uv run python main.py --offline-batch
  embedding          # bge-m3 inference service (text-embeddings-inference)
  postgres           # shared (with pgvector extension added)
  redis              # shared
```

The batch container uses the same image as the worker with a different entrypoint. No additional build or image maintenance cost.

## Data Model

### New table: `baseline_cache`

```sql
CREATE TABLE baseline_cache (
    id              serial PRIMARY KEY,
    fingerprint_key varchar(64) NOT NULL,
    metric_type     varchar(64) NOT NULL,
    metric_value    double precision NOT NULL,
    metadata_json   jsonb DEFAULT '{}',
    computed_at     timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    UNIQUE (fingerprint_key, metric_type)
);
```

- `fingerprint_key`: `token_fingerprint` or `'global'` (cold-start fallback).
- `metric_type`: identifies the metric (see table below).
- `metadata_json`: stores MAD, sample count, percentiles.
- `expires_at`: online mode ignores stale baselines and falls back to fixed defaults.

### Baseline metrics

| metric_type | Meaning | Source query | Replaces |
|---|---|---|---|
| `hourly_tokens_median` | Per-user hourly token median | `usage_aggregates` (hour bucket, last 7 days) | `daily_token_limit` |
| `hourly_tokens_mad` | Median absolute deviation | same | — |
| `short_window_median` | 5-min window token median | `traces` (last 7 days, 5-min slices) | `short_window_token_threshold` |
| `short_window_mad` | MAD | same | — |
| `trace_tokens_p95` | Per-trace token P95 | `traces` (last 7 days) | `HIGH_TRACE_TOKEN_THRESHOLD` (20K) |
| `completion_tokens_p95` | Per-trace completion token P95 | same | `long_output_token_threshold` (8K) |
| `off_hours_median` | Off-hours hourly median | `usage_aggregates` (off-hours, last 14 days) | `off_hours_token_threshold` |
| `off_hours_mad` | MAD | same | — |
| `model_hourly_median` | Per-model hourly median | `usage_aggregates` (grouped by model) | `expensive_model_token_threshold` |

### New table: `model_artifacts`

```sql
CREATE TABLE model_artifacts (
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
```

Stores serialized Isolation Forest models (joblib). The two most recent versions are retained for rollback.

### pgvector on `context_catalog`

```sql
CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE context_catalog ADD COLUMN embedding vector(1024);
CREATE INDEX ON context_catalog
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);
```

## Offline Batch Module

New file: `workers/analysis_worker/offline.py`.

### Entry point

```python
def run_offline_batch(pg_dsn: str, lookback_days: int = 7) -> dict:
```

### Pipeline

1. **Query active fingerprints** from `usage_aggregates` (last 7 days).
2. **Compute baseline metrics** per fingerprint using 4-5 aggregate SQL queries (PG `PERCENTILE_CONT` for median/P95). Writes to `baseline_cache`.
3. **Compute global baselines** (all fingerprints aggregated) for cold-start fallback.
4. **Train Isolation Forest** on 14-day traces (feature vector: total_tokens, completion_ratio, hour_of_day, is_weekend, model_price_tier, prompt_repetition, distinct_models_24h). Serialize to `model_artifacts`.
5. **Run offline inference** on traces from the last hour using the trained model; insert `multivariate_anomaly` alerts.
6. **Precompute catalog embeddings** by calling the local embedding service; update `context_catalog.embedding`.

### Performance

~5 SQL queries covering all metrics, plus model training. Estimated total: 5-10 seconds for medium scale. Runs hourly via cron sidecar.

### CLI entry

`main.py --offline-batch` — connects to PG only (no Redis), runs the pipeline, exits with code 0.

## Online Rules Upgrade

### AnalysisContext extension

New optional fields (all default to `None` meaning "no personalized baseline available"):

```python
hourly_tokens_baseline: float | None = None
hourly_tokens_mad: float | None = None
short_window_baseline: float | None = None
short_window_mad: float | None = None
trace_tokens_p95: float | None = None
completion_tokens_p95: float | None = None
off_hours_baseline: float | None = None
model_baselines: dict[str, float] | None = None
baseline_computed_at: str | None = None
```

### Threshold selection pattern

```python
def _personalized_threshold(baseline, mad, default, k=3):
    if baseline is None:
        return default
    return baseline + k * (mad or baseline * 0.2)
```

### Rules mapping

**Upgraded (6 rules):**

| Rule | Current | Upgraded to |
|---|---|---|
| `high_trace_tokens` | 20,000 | `trace_tokens_p95` |
| `daily_token_limit_exceeded` | 100,000 | `hourly_tokens_baseline * 24 * k` |
| `short_window_token_spike` | 10,000 | `short_window_baseline + 3*MAD` |
| `expensive_model_overuse` | 500 | `model_baselines[model] + 3*MAD` |
| `long_output_anomaly` | 8,000 | `completion_tokens_p95` |
| `off_hours_high_usage` | 2,000 | `off_hours_baseline + 3*MAD` |

**Unchanged (7 rules):** `missing_username`, `identity_unresolved_success`, `invalid_username`, `repeated_prompt`, `possible_token_leak`, `raw_only_large_response`, `retry_storm_trace`. These encode business, security, or infrastructure semantics where fixed thresholds are appropriate.

### AnomalyAlert change

`baseline_value` field (currently always `None`) is populated with the personalized baseline when available. This enables the admin UI to display "normal level: X, observed: Y".

### repository.py change

`analysis_context_for()` gains an additional query to `baseline_cache`:

```sql
SELECT metric_type, metric_value, metadata_json
FROM baseline_cache
WHERE fingerprint_key IN (%s, 'global')
  AND expires_at > now()
ORDER BY fingerprint_key, metric_type
```

Prefers fingerprint-specific baselines; falls back to `'global'` entries.

## Multivariate Anomaly Detection

### Model

Isolation Forest (scikit-learn), `contamination=0.02`, `n_estimators=100`.

### Feature vector

| Feature | Type | Source |
|---|---|---|
| `usage_total_tokens` | float | `traces` |
| `completion_ratio` | float (0-1) | `usage_completion_tokens / usage_total_tokens` |
| `hour_of_day` | int (0-23) | `request_started_at` |
| `is_weekend` | int (0/1) | `request_started_at` |
| `model_price_tier` | int (1-5) | lookup from known model pricing |
| `prompt_repetition` | float (0-1) | repeated prompt count / total prompts |
| `distinct_models_24h` | int | count distinct models per fingerprint in last 24h |

### Output

New anomaly type `multivariate_anomaly` with `severity="medium"`, only produced by offline batch processing. Does not block online trace handling.

### Model lifecycle

- Trained every 24 hours during offline batch (or when `model_artifacts` has no active model).
- Previous version kept for rollback; older versions pruned.

## Semantic Classification Upgrade

### Embedding service

Local `bge-m3` model served via HuggingFace `text-embeddings-inference`:

```yaml
embedding:
  image: ghcr.io/huggingface/text-embeddings-inference:latest
  command: --model-id BAAI/bge-m3 --port 8080
```

- Multilingual (Chinese + English), 1024 dimensions.
- Runs on CPU, ~50ms inference latency, ~4GB memory.

### Classification logic

```python
def classify_work_relevance_v2(job, messages, contexts, embedding_client):
    text = _combined_text(messages)
    trace_embedding = embedding_client.embed(text)
    matched = pgvector_cosine_search(trace_embedding, top_k=3, threshold=0.75)

    if matched:
        # Semantic match from catalog — use catalog metadata for scoring
        return build_assessment(job, matched)

    # Fallback to existing keyword-based classifier
    return classify_work_relevance(job, messages, contexts)
```

Two-tier approach: high-confidence embedding match first, keyword fallback second.

### Offline preprocessing

`--offline-batch` re-embeds all `active=true` catalog entries and updates `context_catalog.embedding`.

## Migration

New migration file in `migrations/` (next sequential number):

1. Create `baseline_cache` table.
2. Create `model_artifacts` table.
3. Add `pgvector` extension.
4. Add `embedding vector(1024)` column to `context_catalog`.
5. Create IVFFlat index on `context_catalog.embedding`.

## New Dependencies

Python packages added to `workers/analysis_worker/pyproject.toml`:
- `scikit-learn` — Isolation Forest training and inference.
- `numpy` — numerical operations.
- `pgvector` — Python client for pgvector queries.

Docker images (no new builds, public images):
- `ghcr.io/huggingface/text-embeddings-inference:latest` — embedding service.
- Cron sidecar uses the same worker image.

## Implementation Phases

**Phase 1 — Baselines (estimated 1-2 weeks):**
- Add `baseline_cache` table and migration.
- Implement `offline.py` with baseline computation.
- Upgrade 6 rules in `rules.py` to use personalized thresholds.
- Extend `repository.py` to load baselines.
- Add cron sidecar to docker-compose.

**Phase 2 — Multivariate anomaly (estimated 1 week):**
- Add `model_artifacts` table and migration.
- Implement `isolation_forest.py` with training and offline inference.
- Integrate into `offline.py` pipeline.
- Add `multivariate_anomaly` alerts.

**Phase 3 — Semantic classification (estimated 1-2 weeks):**
- Add embedding service to docker-compose.
- Add pgvector extension and `context_catalog.embedding` column.
- Implement embedding client and pgvector search.
- Upgrade `work_relevance.py` with two-tier classification.
- Add catalog embedding precomputation to offline pipeline.
