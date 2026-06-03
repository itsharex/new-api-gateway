#!/usr/bin/env python3
"""E2E test: offline batch baseline computation and Isolation Forest training.

Seeds 150 traces in PG, runs run_offline_batch(), and verifies:
  - baseline_cache has rows with correct metric_types
  - model_artifacts has an active isolation_forest model

Prerequisites:
  - postgres running (docker compose up -d postgres)
  - Python worker available at workers/analysis_worker/

Usage:
  uv run e2e/test_offline_batch.py
"""

from __future__ import annotations

import os
import sys
from datetime import datetime, timedelta, timezone

import psycopg

from helpers import (
    apply_migrations,
    check,
    create_test_database,
    eq,
    errors,
    gt,
    not_empty,
    report_results,
)

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

DB_NAME = "audit_gateway_offline_batch_e2e"
TOKEN_FINGERPRINT = "tkfp_batch"
TRACE_COUNT = 150


def make_test_database() -> str:
    dsn = create_test_database(DB_NAME)
    apply_migrations(dsn)
    return dsn


# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------


def seed_traces(dsn: str) -> None:
    now = datetime.now(timezone.utc).replace(minute=0, second=0, microsecond=0)
    with psycopg.connect(dsn) as conn:
        for i in range(TRACE_COUNT):
            tokens = 1000 + (i * 50)
            trace_id = f"batch_trace_{i:04d}"
            trace_time = now - timedelta(hours=i % 72, minutes=i % 60)
            conn.execute(
                """INSERT INTO traces (
                    trace_id, method, path, route_pattern, protocol_family, capture_mode,
                    status_code, upstream_status_code, stream, request_started_at,
                    request_body_size, response_body_size, request_raw_ref, response_raw_ref,
                    token_fingerprint, fingerprint_display, new_api_token_id_snapshot,
                    token_name_snapshot, username_snapshot, identity_resolution_status,
                    model_requested, usage_total_tokens,
                    usage_prompt_tokens, usage_completion_tokens
                ) VALUES (
                    %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s,
                    %s, %s, %s, %s, %s, %s, %s, %s, %s, %s
                )""",
                (
                    trace_id, "POST", "/v1/chat/completions", "/v1/chat/completions",
                    "openai_chat", "raw_and_normalized", 200, 200, False,
                    trace_time.isoformat(),
                    100, 200, "", "",
                    TOKEN_FINGERPRINT, "tkfp_display", 42, "", "alice", "resolved",
                    "gpt-4.1", tokens, tokens // 2, tokens // 2,
                ),
            )

        # Seed usage_aggregates so hourly and model baselines have data
        for i in range(20):
            tokens = 1000 + (i * 100)
            bucket_hour = now - timedelta(hours=19 - i)
            conn.execute(
                """INSERT INTO usage_aggregates (
                    bucket_start, bucket_size, token_fingerprint, new_api_token_id,
                    username, token_name_snapshot, model, route_pattern, protocol_family,
                    request_count, success_count, error_count, stream_count,
                    prompt_tokens, completion_tokens, total_tokens,
                    reasoning_tokens, cached_tokens,
                    request_body_bytes, response_body_bytes
                ) VALUES (
                    %s, 'hour', %s, 42, %s, '', %s, %s, %s,
                    5, 5, 0, 0,
                    %s, %s, %s, 0, 0, 0, 0
                )""",
                (
                    bucket_hour.isoformat(), TOKEN_FINGERPRINT, "alice",
                    "gpt-4.1", "/v1/chat/completions", "openai_chat",
                    tokens // 2, tokens // 2, tokens,
                ),
            )
    print(f"  Seeded {TRACE_COUNT} traces and 20 usage_aggregates")


# ---------------------------------------------------------------------------
# Run offline batch
# ---------------------------------------------------------------------------


def run_offline_batch_via_worker(dsn: str) -> dict:
    """Run offline batch via worker subprocess, return parsed result dict."""
    import ast
    import json
    import subprocess

    WORKER_DIR = os.path.join(
        os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
        "workers", "analysis_worker",
    )
    env = {
        **os.environ,
        "POSTGRES_DSN": dsn,
    }
    result = subprocess.run(
        ["uv", "run", "python", "main.py", "--offline-batch"],
        cwd=WORKER_DIR,
        capture_output=True,
        text=True,
        timeout=120,
        env=env,
    )
    print(f"  Worker exit code: {result.returncode}")
    if result.stdout.strip():
        print(f"  Worker stdout: {result.stdout.strip()}")
    if result.returncode != 0:
        print(f"  Worker stderr: {result.stderr.strip()}", file=sys.stderr)
        raise RuntimeError(f"Offline batch failed: {result.stderr[:500]}")
    # stdout format: "offline batch complete: {...}"
    stdout = result.stdout.strip()
    prefix = "offline batch complete: "
    if stdout.startswith(prefix):
        batch_result = ast.literal_eval(stdout[len(prefix):])
    else:
        batch_result = {}
    print(f"  Offline batch result: {batch_result}")
    return batch_result


# ---------------------------------------------------------------------------
# Assertions
# ---------------------------------------------------------------------------


def assert_baselines(dsn: str) -> None:
    print("\n=== Baseline cache assertions ===")
    with psycopg.connect(dsn) as conn:
        metric_types = [row[0] for row in conn.execute(
            "SELECT DISTINCT metric_type FROM baseline_cache WHERE fingerprint_key = %s",
            (TOKEN_FINGERPRINT,),
        ).fetchall()]

        check("baselines", "hourly_tokens_median" in metric_types,
              f"missing hourly_tokens_median, got {metric_types}")
        check("baselines", "trace_effective_tokens_p95" in metric_types,
              f"missing trace_effective_tokens_p95, got {metric_types}")
        check("baselines", "completion_tokens_p95" in metric_types,
              f"missing completion_tokens_p95, got {metric_types}")
        check("baselines", "model_hourly_median_gpt-4.1" in metric_types,
              f"missing model_hourly_median_gpt-4.1, got {metric_types}")

        count = conn.execute(
            "SELECT count(*) FROM baseline_cache WHERE fingerprint_key = %s",
            (TOKEN_FINGERPRINT,),
        ).fetchone()[0]
        gt("baselines", "row_count", count, 0)

        print(f"    metric_types={metric_types} count={count}")


def assert_model_artifacts(dsn: str) -> None:
    print("\n=== Model artifacts assertions ===")
    with psycopg.connect(dsn) as conn:
        row = conn.execute(
            """SELECT model_name, version, is_active, array_length(feature_columns, 1)
               FROM model_artifacts
               WHERE model_name = 'isolation_forest' AND is_active = true
               ORDER BY trained_at DESC LIMIT 1""",
        ).fetchone()

        check("model", row is not None, "no active isolation_forest model found")
        if row:
            eq("model", "model_name", row[0], "isolation_forest")
            not_empty("model", "version", row[1])
            eq("model", "is_active", row[2], True)
            gt("model", "feature_columns_count", row[3], 0)
            print(f"    model: name={row[0]} version={row[1]} active={row[2]} features={row[3]}")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    print("=== Phase 1: Setup test database ===")
    dsn = make_test_database()

    print("\n=== Phase 2: Seed traces ===")
    seed_traces(dsn)

    print("\n=== Phase 3: Run offline batch ===")
    result = run_offline_batch_via_worker(dsn)
    gt("batch", "fingerprints_processed", result.get("fingerprints_processed", 0), 0)
    gt("batch", "baselines_written", result.get("baselines_written", 0), 0)

    assert_baselines(dsn)
    assert_model_artifacts(dsn)
    report_results(1)


if __name__ == "__main__":
    main()
