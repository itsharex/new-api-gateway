#!/usr/bin/env python3
"""E2E test: analysis worker work-relevance classification.

Seeds a trace with coding-related content, runs the analysis worker, and
verifies that the work_relevance analysis result is produced with the
correct label.

Prerequisites:
  - postgres and redis running (docker compose up -d postgres redis)
  - Python worker available at workers/analysis_worker/

Usage:
  uv run e2e/test_worker_work_relevance.py
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

DB_NAME = "audit_gateway_work_relevance_e2e"
TRACE_ID = "trace_work"
EVIDENCE_ROOT = os.environ.get(
    "EVIDENCE_ROOT",
    os.path.join(
        os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
        "var",
        "e2e-work-relevance-evidence",
    ),
)

REQUEST_BODY = '{"model":"gpt-4.1","messages":[{"role":"user","content":"Debug the new-api gateway route tests"}]}\n'
RESPONSE_BODY = '{"choices":[{"message":{"role":"assistant","content":"Check the route registry tests."}}],"usage":{"total_tokens":1200}}\n'

# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------


def setup(dsn: str) -> None:
    """Seed evidence files, context_catalog, and trace records."""
    # Evidence files
    ev_dir = os.path.join(EVIDENCE_ROOT, "raw/e2e/trace_work")
    os.makedirs(ev_dir, exist_ok=True)
    with open(os.path.join(ev_dir, "request_body.bin"), "w") as f:
        f.write(REQUEST_BODY)
    with open(os.path.join(ev_dir, "response_body.bin"), "w") as f:
        f.write(RESPONSE_BODY)

    with psycopg.connect(dsn) as conn:
        # Insert context catalog entry for work-relevance matching
        conn.execute(
            """INSERT INTO context_catalog (
                context_type, name, description, keywords, aliases, owner,
                expected_task_categories, expected_models, expected_usage_level,
                created_by, updated_by
            ) VALUES (
                %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s
            )""",
            (
                "repo", "new-api-gateway", "Audit gateway repository",
                ["new-api", "gateway"], ["route registry"],
                "platform", ["coding", "debugging"], ["gpt-4.1"], "normal",
                "e2e", "e2e",
            ),
        )

        # Insert trace
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
                "2026-04-28T13:45:22Z", len(REQUEST_BODY), len(RESPONSE_BODY),
                "raw/e2e/trace_work/request_body.bin", "raw/e2e/trace_work/response_body.bin",
                "tkfp_raw", "tkfp_display", 42, "E10001", "E10001", "resolved",
                "gpt-4.1", 1200,
            ),
        )
    print("  Seeded context_catalog, trace, and evidence files")

    # Push job to Redis
    job = {
        "type": "trace_captured",
        "trace_id": TRACE_ID,
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "employee_no": "E10001",
        "username": "E10001",
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
        "stream": False,
        "request_started_at": "2026-04-28T13:45:22Z",
        "request_body_size": len(REQUEST_BODY),
        "response_body_size": len(RESPONSE_BODY),
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
    eq("worker", "work_relevance_count", worker_json.get("work_relevance_count"), 1)


def assert_db_records(dsn: str) -> None:
    print("\n=== DB record assertions ===")
    with psycopg.connect(dsn) as conn:
        label = conn.execute(
            "SELECT label FROM analysis_results WHERE trace_id = %s AND category = 'work_relevance'",
            (TRACE_ID,),
        ).fetchone()
        check("db.work_relevance_label", label is not None, "no work_relevance result found")
        if label:
            eq("db", "work_relevance_label", label[0], "debugging")

        rows = conn.execute(
            "SELECT trace_id, category, label, score, confidence, result_json "
            "FROM analysis_results WHERE trace_id = %s ORDER BY category",
            (TRACE_ID,),
        ).fetchall()
        for row in rows:
            print(f"    trace_id={row[0]} category={row[1]} label={row[2]} "
                  f"score={row[3]} confidence={row[4]}")


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
