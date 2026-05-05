#!/usr/bin/env python3
"""E2E test: media base64 extraction through gateway → worker pipeline.

Sends an OpenAI chat completion with a small base64 PNG image through the
gateway, runs the analysis worker, and verifies that:
  - The worker extracts the binary asset
  - The evidence JSON is rewritten with audit-media: references
  - The raw_evidence_objects table contains the media asset record
  - The binary file matches the original image data
  - traces.request_body_sha256 is updated

Prerequisites:
  - postgres, redis, new-api, audit-gateway all running
  - migrations applied
  - EVIDENCE_STORAGE_DIR set to the gateway's evidence directory

Usage:
  uv run e2e/test_media_extraction.py
"""

from __future__ import annotations

import base64
import json
import os
import signal
import subprocess
import sys
import time

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
MODEL = os.environ.get("TEST_MODEL", "gpt-5.2")

# Minimal valid 1x1 RGBA PNG (70 bytes)
SMALL_PNG = (
    b"\x89PNG\r\n\x1a\n"
    b"\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01"
    b"\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"
    b"\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f"
    b"\x00\x05\x00\x01\xff\x89\x99=\x1d"
    b"\x00\x00\x00\x00IEND\xaeB`\x82"
)
SMALL_PNG_B64 = base64.b64encode(SMALL_PNG).decode("ascii")
DATA_URL = f"data:image/png;base64,{SMALL_PNG_B64}"


# ---------------------------------------------------------------------------
# Phase 2: Send request with base64 image
# ---------------------------------------------------------------------------


def send_image_request() -> TraceResult:
    """Send an OpenAI chat completion with a base64 PNG image_url."""
    print("\n=== Phase 2: Send request with base64 image ===")
    resp, err = gateway_post(
        "/v1/chat/completions",
        {
            "model": MODEL,
            "messages": [
                {
                    "role": "user",
                    "content": [
                        {"type": "text", "text": "describe this image"},
                        {
                            "type": "image_url",
                            "image_url": {"url": DATA_URL},
                        },
                    ],
                }
            ],
            "max_tokens": 10,
        },
        "media-extraction:request",
    )
    if err or resp is None:
        return {"trace_id": "", "endpoint": "/v1/chat/completions", "turn": 1, "status_code": 0}
    trace_id = resp.headers.get("x-audit-trace-id", "")
    print(f"  Request: status={resp.status_code} trace_id={trace_id}")
    return {"trace_id": trace_id, "endpoint": "/v1/chat/completions", "turn": 1, "status_code": resp.status_code}


# ---------------------------------------------------------------------------
# Phase 3: Stop background workers
# ---------------------------------------------------------------------------


def stop_background_workers() -> None:
    """Stop any running analysis worker processes."""
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


# ---------------------------------------------------------------------------
# Phase 4: Push job to Redis and run worker
# ---------------------------------------------------------------------------


def push_job_to_redis(trace: dict) -> None:
    """Push a trace_captured job to Redis analysis_jobs list."""
    print("\n=== Phase 4: Push job to Redis ===")
    payload = build_job_payload(trace)
    r = redis.Redis.from_url(REDIS_URL, decode_responses=True)
    r.delete("analysis_jobs")
    r.rpush("analysis_jobs", json.dumps(payload))
    print(f"  Pushed job for trace_id={trace['trace_id']}")


def run_worker() -> dict:
    """Run the analysis worker in --redis-once mode."""
    print("\n=== Phase 5: Run analysis worker ===")
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

    lines = [ln.strip() for ln in result.stdout.strip().splitlines() if ln.strip()]
    if not lines:
        bail(f"Worker produced no stdout output. stderr: {result.stderr[:500]}")

    worker_json = json.loads(lines[-1])
    print(f"  Worker result: {json.dumps(worker_json, indent=2)}")
    return worker_json


# ---------------------------------------------------------------------------
# Phase 6: Assertions
# ---------------------------------------------------------------------------


def assert_worker_result(worker_json: dict, trace_id: str) -> None:
    print("\n=== Phase 6: Worker output assertions ===")
    eq("worker", "worker_status", worker_json.get("worker_status"), "processed")
    eq("worker", "accepted_trace_id", worker_json.get("accepted_trace_id"), trace_id)
    gt("worker", "media_assets_extracted", worker_json.get("media_assets_extracted", 0), 0)


def assert_media_assets(conn: psycopg.Connection, trace_id: str) -> None:
    print("\n=== Phase 7: Media asset DB assertions ===")
    rows = conn.execute(
        "SELECT object_type, object_ref, content_type, size_bytes FROM raw_evidence_objects "
        "WHERE trace_id = %s AND object_type LIKE 'media_asset_%'",
        (trace_id,),
    ).fetchall()
    check("media_assets.exists", len(rows) > 0, f"no media_asset rows for trace_id={trace_id}")
    if not rows:
        return
    asset_type, asset_ref, content_type, size_bytes = rows[0]
    eq("media_assets", "object_type", asset_type, "media_asset_000001")
    not_empty("media_assets", "object_ref", asset_ref)
    eq("media_assets", "content_type", content_type, "image/png")
    gt("media_assets", "size_bytes", size_bytes, 0)
    print(f"  media_asset: type={asset_type} ref={asset_ref} content_type={content_type} size={size_bytes}")


def assert_evidence_rewritten(trace: dict) -> None:
    print("\n=== Phase 8: Evidence file assertions ===")
    request_ref = trace.get("request_raw_ref", "")
    if not request_ref:
        check("evidence", "request_raw_ref", False, "empty request_raw_ref")
        return

    evidence_root = EVIDENCE_DIR
    if not os.path.isabs(evidence_root):
        project_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        evidence_root = os.path.join(project_root, evidence_root)

    evidence_path = os.path.join(evidence_root, request_ref)
    if not os.path.exists(evidence_path):
        check("evidence", "file_exists", False, f"file not found: {evidence_path}")
        return

    with open(evidence_path, "r", encoding="utf-8") as f:
        body = f.read()

    check("evidence", "contains_audit_media_ref", "audit-media:media_asset_000001" in body,
          "expected 'audit-media:media_asset_000001' in evidence JSON")
    check("evidence", "base64_removed", SMALL_PNG_B64 not in body,
          "base64 data should be replaced by reference")

    # Verify the binary asset file exists and matches
    asset_dir = os.path.dirname(evidence_path)
    asset_path = os.path.join(asset_dir, "media_asset_000001.bin")
    check("evidence", "asset_file_exists", os.path.exists(asset_path),
          f"binary asset not found: {asset_path}")
    if os.path.exists(asset_path):
        with open(asset_path, "rb") as f:
            binary = f.read()
        check("evidence", "asset_content_matches", binary == SMALL_PNG,
              f"asset size={len(binary)} expected={len(SMALL_PNG)}")


def assert_sha256_updated(conn: psycopg.Connection, trace_id: str) -> None:
    print("\n=== Phase 9: SHA256 update assertion ===")
    row = conn.execute(
        "SELECT request_body_sha256 FROM traces WHERE trace_id = %s",
        (trace_id,),
    ).fetchone()
    if row is None:
        check("sha256", "trace_exists", False, "no trace row")
        return
    sha256 = row[0]
    not_empty("sha256", "request_body_sha256", sha256)
    print(f"  request_body_sha256={sha256[:16]}...")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    preflight(
        "/v1/chat/completions",
        {
            "model": MODEL,
            "messages": [{"role": "user", "content": "ping"}],
            "max_tokens": 1,
        },
        model_label=MODEL,
    )

    # Send request with base64 image
    result = send_image_request()
    trace_id = result.get("trace_id", "")
    if not trace_id:
        bail("No trace_id returned from gateway request")

    # Wait for trace in DB
    wait_for_traces([trace_id])

    # Stop background workers
    print("\n=== Phase 3: Stop background workers ===")
    stop_background_workers()

    # Read trace and push job
    with psycopg.connect(PG_DSN) as conn:
        trace = read_trace_for_job(conn, trace_id)
    if trace is None:
        bail(f"Trace {trace_id} not found in DB")

    # Verify trace was captured correctly
    with psycopg.connect(PG_DSN) as conn:
        assert_trace_fields(conn, trace_id, "media-capture", "openai_chat")

    push_job_to_redis(trace)

    # Run worker
    worker_json = run_worker()

    # Assert
    assert_worker_result(worker_json, trace_id)
    with psycopg.connect(PG_DSN) as conn:
        assert_media_assets(conn, trace_id)
        assert_sha256_updated(conn, trace_id)

    assert_evidence_rewritten(trace)

    report_results(1)


if __name__ == "__main__":
    main()
