#!/usr/bin/env python3
"""E2E test: Gateway + Worker pipeline with OSS evidence storage backend.

Sends a request through the gateway (running with EVIDENCE_STORAGE_BACKEND=oss),
pushes a trace_captured job to Redis, runs the analysis worker with OSS config,
and verifies that evidence objects are stored in OSS and readable.

Prerequisites:
  - OSS_ENDPOINT, OSS_BUCKET, OSS_ACCESS_KEY_ID, OSS_ACCESS_KEY_SECRET set
  - Gateway running with EVIDENCE_STORAGE_BACKEND=oss (use scripts/e2e_oss_pipeline.sh)
  - postgres, redis, new-api running
  - migrations applied

Usage:
  ./scripts/e2e_oss_pipeline.sh
  # or manually:
  uv run e2e/test_gateway_worker_pipeline_oss.py
"""

from __future__ import annotations

import json
import os
import subprocess
import sys

import oss2
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
# Config & OSS setup
# ---------------------------------------------------------------------------

OSS_ENDPOINT = os.environ.get("OSS_ENDPOINT", "")
OSS_BUCKET = os.environ.get("OSS_BUCKET", "")
OSS_ACCESS_KEY_ID = os.environ.get("OSS_ACCESS_KEY_ID", "")
OSS_ACCESS_KEY_SECRET = os.environ.get("OSS_ACCESS_KEY_SECRET", "")

WORKER_DIR = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "workers",
    "analysis_worker",
)


def _require_oss_env() -> None:
    missing = [k for k, v in {
        "OSS_ENDPOINT": OSS_ENDPOINT,
        "OSS_BUCKET": OSS_BUCKET,
        "OSS_ACCESS_KEY_ID": OSS_ACCESS_KEY_ID,
        "OSS_ACCESS_KEY_SECRET": OSS_ACCESS_KEY_SECRET,
    }.items() if not v]
    if missing:
        bail(f"Set {', '.join(missing)} to run OSS e2e")


def _oss_bucket() -> oss2.Bucket:
    auth = oss2.Auth(OSS_ACCESS_KEY_ID, OSS_ACCESS_KEY_SECRET)
    return oss2.Bucket(auth, OSS_ENDPOINT, OSS_BUCKET)


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
        "oss-pipeline:request",
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
    """Run the analysis worker in --redis-once mode with OSS config."""
    print("\n=== Phase 4: Run analysis worker (OSS backend) ===")
    env = {
        **os.environ,
        "EVIDENCE_STORAGE_BACKEND": "oss",
        "OSS_ENDPOINT": OSS_ENDPOINT,
        "OSS_BUCKET": OSS_BUCKET,
        "OSS_ACCESS_KEY_ID": OSS_ACCESS_KEY_ID,
        "OSS_ACCESS_KEY_SECRET": OSS_ACCESS_KEY_SECRET,
        "POSTGRES_DSN": PG_DSN,
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


def assert_oss_evidence_refs(conn: psycopg.Connection, trace_id: str) -> None:
    """Verify raw_evidence_objects have oss:// prefixed refs and storage_backend='oss'."""
    print("\n=== Phase 7: OSS evidence ref assertions ===")
    rows = conn.execute(
        "SELECT object_type, object_ref, storage_backend, size_bytes "
        "FROM raw_evidence_objects WHERE trace_id = %s",
        (trace_id,),
    ).fetchall()
    check("oss_evidence.exists", len(rows) > 0, f"no evidence rows for trace_id={trace_id}")
    if not rows:
        return

    expected_prefix = f"oss://{OSS_BUCKET}/"
    for obj_type, obj_ref, storage_backend, size_bytes in rows:
        check(
            f"oss_evidence.{obj_type}.ref_prefix",
            obj_ref.startswith(expected_prefix),
            f"object_ref={obj_ref!r} expected prefix={expected_prefix!r}",
        )
        check(
            f"oss_evidence.{obj_type}.storage_backend",
            storage_backend == "oss",
            f"storage_backend={storage_backend!r} expected='oss'",
        )
        print(f"  {obj_type}: ref={obj_ref} backend={storage_backend} size={size_bytes}")


def assert_oss_object_readable(conn: psycopg.Connection, trace_id: str) -> None:
    """Read back evidence objects from OSS and verify they are non-empty."""
    print("\n=== Phase 8: OSS object read-back assertions ===")
    bucket = _oss_bucket()
    rows = conn.execute(
        "SELECT object_type, object_ref, size_bytes "
        "FROM raw_evidence_objects WHERE trace_id = %s",
        (trace_id,),
    ).fetchall()
    check("oss_readback.exists", len(rows) > 0, f"no evidence rows for trace_id={trace_id}")
    if not rows:
        return

    for obj_type, obj_ref, size_bytes in rows:
        if not obj_ref.startswith(f"oss://{OSS_BUCKET}/"):
            continue
        key = obj_ref[len(f"oss://{OSS_BUCKET}/"):]
        try:
            data = bucket.get_object(key).read()
            check(
                f"oss_readback.{obj_type}.non_empty",
                len(data) > 0,
                f"object {key} is empty",
            )
            check(
                f"oss_readback.{obj_type}.size_match",
                len(data) == size_bytes,
                f"object {key} size={len(data)} expected={size_bytes}",
            )
            print(f"  {obj_type}: read {len(data)} bytes from OSS (expected {size_bytes})")
        except oss2.exceptions.NoSuchKey:
            check(f"oss_readback.{obj_type}.exists", False, f"object {key} not found in OSS")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    _require_oss_env()

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
        assert_trace_fields(conn, trace_id, "oss-capture", "openai_chat")

    push_job_to_redis(trace)

    # Run worker with OSS config
    worker_json = run_worker()

    # Assert worker result and analysis_results
    assert_worker_result(worker_json, trace_id)
    with psycopg.connect(PG_DSN) as conn:
        assert_analysis_results(conn, trace_id)
        assert_oss_evidence_refs(conn, trace_id)
        assert_oss_object_readable(conn, trace_id)

    report_results(1)


if __name__ == "__main__":
    main()
