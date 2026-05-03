#!/usr/bin/env python3
"""E2E test: gateway capture → Worker analysis pipeline.

Sends a request through the gateway, pushes a trace_captured job to Redis,
runs the analysis worker, and verifies the analysis_results table.

Prerequisites:
  - postgres, redis, new-api, audit-gateway all running
  - Python worker available at workers/analysis_worker/
  - migrations applied
  - EVIDENCE_STORAGE_DIR set to the gateway's evidence directory

Usage:
  uv run e2e/test_gateway_worker_pipeline.py
"""

from __future__ import annotations

import json
import os
import subprocess
import sys

import psycopg
import redis

from helpers import (
    PG_DSN,
    REDIS_URL,
    TraceResult,
    assert_trace_fields,
    bail,
    build_job_payload,
    check,
    eq,
    errors,
    gateway_post,
    gt,
    not_empty,
    preflight,
    read_trace_for_job,
    report_results,
    wait_for_traces,
)

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

EVIDENCE_DIR = os.environ.get("EVIDENCE_STORAGE_DIR", "var/evidence")
WORKER_DIR = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "workers",
    "analysis_worker",
)


# ---------------------------------------------------------------------------
# Phase 2: Send request & trigger worker
# ---------------------------------------------------------------------------


def send_openai_request() -> TraceResult:
    """Send a single OpenAI Chat non-streaming request through the gateway."""
    print("\n=== Phase 2: Send request through gateway ===")
    resp, err = gateway_post(
        "/v1/chat/completions",
        {
            "model": os.environ.get("TEST_MODEL", "gpt-5.2"),
            "messages": [{"role": "user", "content": "hello"}],
            "max_tokens": 10,
        },
        "worker-pipeline:request",
    )
    if err or resp is None:
        return {"trace_id": "", "endpoint": "/v1/chat/completions", "turn": 1, "status_code": 0}
    trace_id = resp.headers.get("x-audit-trace-id", "")
    print(f"  Request: status={resp.status_code} trace_id={trace_id}")
    return {"trace_id": trace_id, "endpoint": "/v1/chat/completions", "turn": 1, "status_code": resp.status_code}


def stop_background_workers() -> None:
    """Stop any running analysis worker processes to avoid race conditions."""
    import signal
    result = subprocess.run(
        ["pgrep", "-f", "analysis_worker.*main.py"],
        capture_output=True, text=True,
    )
    pids = [p for p in result.stdout.strip().splitlines() if p.strip()]
    if pids:
        for pid in pids:
            pid = pid.strip()
            print(f"  Stopping background worker PID {pid}")
            try:
                os.kill(int(pid), signal.SIGTERM)
            except ProcessLookupError:
                pass
        import time
        time.sleep(1)
        # Verify they stopped
        for pid in pids:
            try:
                os.kill(int(pid.strip()), 0)
                os.kill(int(pid.strip()), signal.SIGKILL)
            except ProcessLookupError:
                pass
        print(f"  Stopped {len(pids)} background worker(s)")
    else:
        print("  No background workers found")


def push_job_to_redis(trace: dict) -> None:
    """Push a trace_captured job to Redis analysis_jobs list."""
    print("\n=== Phase 3: Push job to Redis ===")
    payload = build_job_payload(trace)
    r = redis.Redis.from_url(REDIS_URL, decode_responses=True)
    r.delete("analysis_jobs")
    r.rpush("analysis_jobs", json.dumps(payload))
    print(f"  Pushed job for trace_id={trace['trace_id']}")


def run_worker() -> dict:
    """Run the analysis worker in --redis-once mode, return parsed stdout JSON."""
    print("\n=== Phase 4: Run analysis worker ===")
    env = {
        **os.environ,
        "POSTGRES_DSN": PG_DSN,
        "EVIDENCE_STORAGE_DIR": EVIDENCE_DIR,
        "REDIS_URL": REDIS_URL,
    }
    result = subprocess.run(
        ["uv", "run", "python", "main.py", "--redis-once"],
        cwd=WORKER_DIR,
        capture_output=True,
        text=True,
        timeout=60,
        env=env,
    )
    print(f"  Worker exit code: {result.returncode}")
    print(f"  Worker stdout: {result.stdout.strip()}")
    if result.returncode != 0:
        print(f"  Worker stderr: {result.stderr.strip()}", file=sys.stderr)

    # Parse the last non-empty stdout line as JSON
    lines = [ln.strip() for ln in result.stdout.strip().splitlines() if ln.strip()]
    if not lines:
        bail(f"Worker produced no stdout output. stderr: {result.stderr[:500]}")

    worker_json = json.loads(lines[-1])
    print(f"  Worker result: {json.dumps(worker_json, indent=2)}")
    return worker_json


# ---------------------------------------------------------------------------
# Phase 5: Assertions
# ---------------------------------------------------------------------------


def assert_worker_result(worker_json: dict, trace_id: str) -> None:
    print("\n=== Phase 5: Worker output assertions ===")
    eq("worker", "worker_status", worker_json.get("worker_status"), "processed")
    eq("worker", "accepted_trace_id", worker_json.get("accepted_trace_id"), trace_id)
    gt("worker", "analysis_result_count", worker_json.get("analysis_result_count", 0), 0)


def assert_analysis_results(conn: psycopg.Connection, trace_id: str) -> None:
    print("\n=== Phase 6: analysis_results DB assertions ===")
    rows = conn.execute(
        "SELECT analyzer_name, category, label, result_json FROM analysis_results WHERE trace_id = %s",
        (trace_id,),
    ).fetchall()
    check("analysis_results.exists", len(rows) > 0, f"no rows for trace_id={trace_id}")
    if not rows:
        return
    print(f"  Found {len(rows)} analysis result(s)")
    for row in rows:
        analyzer_name, category, label, result_json = row
        not_empty("analysis_results", "analyzer_name", analyzer_name)
        not_empty("analysis_results", "category", category)
        not_empty("analysis_results", "label", label)
        print(f"    analyzer={analyzer_name} category={category} label={label}")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    preflight(
        "/v1/chat/completions",
        {
            "model": os.environ.get("TEST_MODEL", "gpt-5.2"),
            "messages": [{"role": "user", "content": "ping"}],
            "max_tokens": 1,
        },
        model_label=os.environ.get("TEST_MODEL", "gpt-5.2"),
    )

    # Send request
    result = send_openai_request()
    trace_id = result.get("trace_id", "")
    if not trace_id:
        bail("No trace_id returned from gateway request")

    # Wait for trace in DB
    wait_for_traces([trace_id])

    # Stop background workers before pushing to Redis
    print("\n=== Phase 2.5: Stop background workers ===")
    stop_background_workers()

    # Read trace and push job
    with psycopg.connect(PG_DSN) as conn:
        trace = read_trace_for_job(conn, trace_id)
    if trace is None:
        bail(f"Trace {trace_id} not found in DB")

    # Verify trace was captured correctly
    with psycopg.connect(PG_DSN) as conn:
        assert_trace_fields(conn, trace_id, "gateway-capture", "openai_chat")

    push_job_to_redis(trace)

    # Run worker
    worker_json = run_worker()

    # Assert worker result and analysis_results
    assert_worker_result(worker_json, trace_id)
    with psycopg.connect(PG_DSN) as conn:
        assert_analysis_results(conn, trace_id)

    report_results(1)


if __name__ == "__main__":
    main()
