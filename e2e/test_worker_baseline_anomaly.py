#!/usr/bin/env python3
"""E2E test: baseline-informed anomaly detection.

Seeds a trace_effective_tokens_p95 baseline in baseline_cache, then pushes a
trace whose effective token count meets the personalized threshold. Verifies
the worker fires a high_trace_tokens anomaly with baseline_value populated.

Prerequisites:
  - postgres and redis running (docker compose up -d postgres redis)
  - Python worker available at workers/analysis_worker/

Usage:
  uv run e2e/test_worker_baseline_anomaly.py
"""

from __future__ import annotations

import json
import os
from datetime import datetime, timedelta, timezone

import psycopg
import redis

from helpers import (
    REDIS_URL,
    apply_migrations,
    check,
    create_test_database,
    eq,
    errors,
    flush_redis,
    report_results,
    run_worker_once,
)

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

DB_NAME = "audit_gateway_baseline_anomaly_e2e"
TRACE_ID = "trace_baseline_anomaly"
EVIDENCE_ROOT = os.environ.get(
    "EVIDENCE_ROOT",
    os.path.join(
        os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
        "var",
        "e2e-baseline-evidence",
    ),
)

TOKEN_FINGERPRINT = "tkfp_baseline"
BASELINE_P95 = 30000.0
TRACE_PROMPT_TOKENS = 42000
TRACE_CACHED_TOKENS = 7000
TRACE_COMPLETION_TOKENS = 10000
TRACE_TOTAL_TOKENS = TRACE_PROMPT_TOKENS + TRACE_COMPLETION_TOKENS
TRACE_EFFECTIVE_TOKENS = (
    max(TRACE_PROMPT_TOKENS - TRACE_CACHED_TOKENS, 0) + TRACE_COMPLETION_TOKENS
)
EXPECTED_THRESHOLD = max(BASELINE_P95 * 1.5, 40000)

# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------


def setup(dsn: str) -> None:
    ev_dir = os.path.join(EVIDENCE_ROOT, "raw/e2e", TRACE_ID)
    os.makedirs(ev_dir, exist_ok=True)
    with open(os.path.join(ev_dir, "request_body.bin"), "w") as f:
        f.write("{}\n")
    with open(os.path.join(ev_dir, "response_body.bin"), "w") as f:
        f.write("{}\n")

    now = datetime.now(timezone.utc)
    expires = now + timedelta(hours=25)

    with psycopg.connect(dsn) as conn:
        conn.execute(
            """INSERT INTO baseline_cache
               (fingerprint_key, metric_type, metric_value, metadata_json, computed_at, expires_at)
               VALUES (%s, %s, %s, %s, %s, %s)""",
            (TOKEN_FINGERPRINT, "trace_effective_tokens_p95", BASELINE_P95, "{}", now, expires),
        )

        conn.execute(
            """INSERT INTO traces (
                trace_id, method, path, route_pattern, protocol_family, capture_mode,
                status_code, upstream_status_code, stream, request_started_at,
                request_body_size, response_body_size, request_raw_ref, response_raw_ref,
                token_fingerprint, fingerprint_display, new_api_token_id_snapshot,
                token_name_snapshot, username_snapshot, identity_resolution_status,
                model_requested, usage_total_tokens, usage_prompt_tokens,
                usage_completion_tokens, usage_cached_tokens
            ) VALUES (
                %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s,
                %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s
            )""",
            (
                TRACE_ID, "POST", "/v1/chat/completions", "/v1/chat/completions",
                "openai_chat", "raw_and_normalized", 200, 200, False,
                "2026-05-18T10:00:00Z", 2, 2,
                f"file:///raw/e2e/{TRACE_ID}/request_body.bin",
                f"file:///raw/e2e/{TRACE_ID}/response_body.bin",
                TOKEN_FINGERPRINT, "tkfp_display", 42, "", "", "unresolved",
                "gpt-4.1", TRACE_TOTAL_TOKENS, TRACE_PROMPT_TOKENS,
                TRACE_COMPLETION_TOKENS, TRACE_CACHED_TOKENS,
            ),
        )
    print("  Seeded baseline_cache and trace")

    job = {
        "type": "trace_captured",
        "trace_id": TRACE_ID,
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "",
        "request_raw_ref": f"file:///raw/e2e/{TRACE_ID}/request_body.bin",
        "response_raw_ref": f"file:///raw/e2e/{TRACE_ID}/response_body.bin",
        "request_content_type": "application/json",
        "response_content_type": "application/json",
        "model_requested": "gpt-4.1",
        "usage_prompt_tokens": TRACE_PROMPT_TOKENS,
        "usage_completion_tokens": TRACE_COMPLETION_TOKENS,
        "usage_total_tokens": TRACE_TOTAL_TOKENS,
        "usage_cached_tokens": TRACE_CACHED_TOKENS,
        "token_fingerprint": TOKEN_FINGERPRINT,
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "",
        "identity_resolution_status": "unresolved",
        "status_code": 200,
        "upstream_status_code": 200,
        "stream": False,
        "request_started_at": "2026-05-18T10:00:00Z",
        "request_body_size": 2,
        "response_body_size": 2,
    }
    r = redis.Redis.from_url(REDIS_URL, decode_responses=True)
    r.rpush("analysis_jobs", json.dumps(job))
    print("  Pushed job to Redis")


# ---------------------------------------------------------------------------
# Assertions
# ---------------------------------------------------------------------------


def assert_worker_output(worker_json: dict) -> None:
    print("\n=== Worker output assertions ===")
    eq("worker", "worker_status", worker_json.get("worker_status"), "processed")
    eq("worker", "anomaly_count", worker_json.get("anomaly_count"), 1)


def assert_db_records(dsn: str) -> None:
    print("\n=== DB record assertions ===")
    with psycopg.connect(dsn) as conn:
        rows = conn.execute(
            """SELECT anomaly_type, observed_value, threshold_value, baseline_value
               FROM usage_anomalies
               WHERE %s = ANY(sample_trace_ids)
               ORDER BY anomaly_type""",
            (TRACE_ID,),
        ).fetchall()

        eq("db", "anomaly_row_count", len(rows), 1)

        high_tokens = [r for r in rows if r[0] == "high_trace_tokens"]
        check("db", len(high_tokens) == 1,
              f"expected high_trace_tokens anomaly, found types: {[r[0] for r in rows]}")

        if high_tokens:
            anom = high_tokens[0]
            eq("db.high_trace_tokens", "observed_value", anom[1], TRACE_EFFECTIVE_TOKENS)
            eq("db.high_trace_tokens", "threshold_value", anom[2], EXPECTED_THRESHOLD)
            eq("db.high_trace_tokens", "baseline_value", anom[3], BASELINE_P95)
            print(f"    anomaly: type={anom[0]} observed={anom[1]} "
                  f"threshold={anom[2]} baseline={anom[3]}")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    print("=== Phase 1: Setup test database ===")
    rm_rf(EVIDENCE_ROOT)
    dsn = create_test_database(DB_NAME)
    apply_migrations(dsn)
    flush_redis()

    print("\n=== Phase 2: Seed data ===")
    setup(dsn)

    print("\n=== Phase 3: Run worker ===")
    worker_json = run_worker_once(postgres_dsn=dsn, evidence_dir=EVIDENCE_ROOT)

    assert_worker_output(worker_json)
    assert_db_records(dsn)
    report_results(1)


def rm_rf(path: str) -> None:
    import shutil
    if os.path.isdir(path):
        shutil.rmtree(path)


if __name__ == "__main__":
    main()
