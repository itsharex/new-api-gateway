#!/usr/bin/env python3
"""E2E test: analysis worker anomaly & coverage alert detection.

Seeds a trace with high token usage, runs the analysis worker, and verifies
that usage_anomalies and coverage_alerts are created correctly.

Prerequisites:
  - postgres and redis running (docker compose up -d postgres redis)
  - Python worker available at workers/analysis_worker/

Usage:
  uv run e2e/test_worker_anomaly_coverage.py
"""

from __future__ import annotations

import json
import os

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

DB_NAME = "audit_gateway_e2e"
TRACE_ID = "trace_gap"
EVIDENCE_ROOT = os.environ.get(
    "EVIDENCE_ROOT",
    os.path.join(
        os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
        "var",
        "e2e-evidence",
    ),
)

# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------


def setup(dsn: str) -> None:
    """Seed evidence files and database records."""
    # Evidence files
    ev_dir = os.path.join(EVIDENCE_ROOT, "raw/e2e/trace_gap")
    os.makedirs(ev_dir, exist_ok=True)
    with open(os.path.join(ev_dir, "request_body.bin"), "w") as f:
        f.write("{}\n")
    with open(os.path.join(ev_dir, "response_body.bin"), "w") as f:
        f.write("{}\n")

    # Insert trace
    with psycopg.connect(dsn) as conn:
        conn.execute(
            """INSERT INTO traces (
                trace_id, method, path, route_pattern, protocol_family, capture_mode,
                status_code, upstream_status_code, stream, request_started_at,
                request_body_size, response_body_size, request_raw_ref, response_raw_ref,
                token_fingerprint, fingerprint_display, new_api_token_id_snapshot,
                token_name_snapshot, username_snapshot, identity_resolution_status,
                model_requested, usage_total_tokens
            ) VALUES (
                %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s,
                %s, %s, %s, %s, %s, %s, %s, %s
            )""",
            (
                TRACE_ID, "POST", "/v1/chat/completions", "/v1/chat/completions",
                "openai_chat", "raw_and_normalized", 200, 200, False,
                "2026-04-28T13:45:22Z", 2, 2,
                "file:///raw/e2e/trace_gap/request_body.bin", "file:///raw/e2e/trace_gap/response_body.bin",
                "tkfp_raw", "tkfp_display", 42, "", "", "unresolved",
                "gpt-4.1", 25001,
            ),
        )
    print("  Seeded trace and evidence files")

    # Push job to Redis
    job = {
        "type": "trace_captured",
        "trace_id": TRACE_ID,
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "",
        "request_raw_ref": "file:///raw/e2e/trace_gap/request_body.bin",
        "response_raw_ref": "file:///raw/e2e/trace_gap/response_body.bin",
        "request_content_type": "application/json",
        "response_content_type": "application/json",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 25001,
        "token_fingerprint": "tkfp_raw",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "",
        "identity_resolution_status": "unresolved",
        "status_code": 200,
        "upstream_status_code": 200,
        "stream": False,
        "request_started_at": "2026-04-28T13:45:22Z",
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
    eq("worker", "anomaly_count", worker_json.get("anomaly_count"), 4)
    eq("worker", "coverage_alert_count", worker_json.get("coverage_alert_count"), 1)


def assert_db_records(dsn: str) -> None:
    print("\n=== DB record assertions ===")
    with psycopg.connect(dsn) as conn:
        anomaly_count = conn.execute(
            "SELECT count(*) FROM usage_anomalies WHERE %s = ANY(sample_trace_ids)",
            (TRACE_ID,),
        ).fetchone()[0]
        eq("db", "usage_anomalies_count", anomaly_count, 4)

        coverage_count = conn.execute(
            "SELECT count(*) FROM coverage_alerts WHERE %s = ANY(sample_trace_ids)",
            (TRACE_ID,),
        ).fetchone()[0]
        eq("db", "coverage_alerts_count", coverage_count, 1)

        conn.execute("TABLE usage_anomalies;")
        conn.execute("TABLE coverage_alerts;")


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
    """Remove a directory tree if it exists."""
    import shutil

    if os.path.isdir(path):
        shutil.rmtree(path)


if __name__ == "__main__":
    main()
