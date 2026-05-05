#!/usr/bin/env python3
"""E2E test: media base64 extraction with OSS evidence storage.

Sends an OpenAI chat completion with a small base64 PNG image through the
gateway (OSS backend), runs the analysis worker with OSS config, and verifies:
  - Media assets are stored in OSS with oss:// prefixed refs
  - Binary content in OSS matches the original image data
  - The request body evidence is rewritten with audit-media: references

Prerequisites:
  - OSS_ENDPOINT, OSS_BUCKET, OSS_ACCESS_KEY_ID, OSS_ACCESS_KEY_SECRET set
  - Gateway running with EVIDENCE_STORAGE_BACKEND=oss (use scripts/e2e_oss_pipeline.sh)
  - postgres, redis, new-api running
  - migrations applied

Usage:
  ./scripts/e2e_oss_pipeline.sh
  # or manually:
  uv run e2e/test_media_extraction_oss.py
"""

from __future__ import annotations

import base64
import json
import os
import signal
import subprocess
import sys
import time

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

EVIDENCE_DIR = os.environ.get("EVIDENCE_STORAGE_DIR", "var/evidence")
WORKER_DIR = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "workers",
    "analysis_worker",
)
MODEL = os.environ.get("TEST_MODEL", "gpt-5.2")

# Minimal valid 1x1 PNG (8 bytes)
SMALL_PNG = b"\x89PNG\r\n\x1a\n"
SMALL_PNG_B64 = base64.b64encode(SMALL_PNG).decode("ascii")
DATA_URL = f"data:image/png;base64,{SMALL_PNG_B64}"


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


def _read_oss_object(object_ref: str) -> bytes:
    """Read an object from OSS given its oss://bucket/key ref."""
    prefix = f"oss://{OSS_BUCKET}/"
    if not object_ref.startswith(prefix):
        bail(f"Expected oss:// ref, got {object_ref!r}")
    key = object_ref[len(prefix):]
    return _oss_bucket().get_object(key).read()


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
        "oss-media-extraction:request",
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
    """Run the analysis worker in --redis-once mode with OSS config."""
    print("\n=== Phase 5: Run analysis worker (OSS backend) ===")
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
# Phase 6: Assertions
# ---------------------------------------------------------------------------


def assert_worker_result(worker_json: dict, trace_id: str) -> None:
    print("\n=== Phase 6: Worker output assertions ===")
    eq("worker", "worker_status", worker_json.get("worker_status"), "processed")
    eq("worker", "accepted_trace_id", worker_json.get("accepted_trace_id"), trace_id)
    gt("worker", "media_assets_extracted", worker_json.get("media_assets_extracted", 0), 0)


def assert_oss_media_assets(conn: psycopg.Connection, trace_id: str) -> None:
    """Verify media assets in DB have oss:// prefixed refs."""
    print("\n=== Phase 7: OSS media asset DB assertions ===")
    rows = conn.execute(
        "SELECT object_type, object_ref, content_type, size_bytes, storage_backend "
        "FROM raw_evidence_objects "
        "WHERE trace_id = %s AND object_type LIKE 'media_asset_%'",
        (trace_id,),
    ).fetchall()
    check("oss_media_assets.exists", len(rows) > 0, f"no media_asset rows for trace_id={trace_id}")
    if not rows:
        return
    asset_type, asset_ref, content_type, size_bytes, storage_backend = rows[0]
    eq("oss_media_assets", "object_type", asset_type, "media_asset_000001")
    not_empty("oss_media_assets", "object_ref", asset_ref)
    check(
        "oss_media_assets.ref_prefix",
        asset_ref.startswith(f"oss://{OSS_BUCKET}/"),
        f"object_ref={asset_ref!r} expected oss://{OSS_BUCKET}/ prefix",
    )
    eq("oss_media_assets", "content_type", content_type, "image/png")
    eq("oss_media_assets", "storage_backend", storage_backend, "oss")
    gt("oss_media_assets", "size_bytes", size_bytes, 0)
    print(f"  media_asset: type={asset_type} ref={asset_ref} content_type={content_type} size={size_bytes}")


def assert_oss_media_content(conn: psycopg.Connection, trace_id: str) -> None:
    """Read back media asset from OSS and verify binary content."""
    print("\n=== Phase 8: OSS media content read-back ===")
    row = conn.execute(
        "SELECT object_ref FROM raw_evidence_objects "
        "WHERE trace_id = %s AND object_type = 'media_asset_000001'",
        (trace_id,),
    ).fetchone()
    if row is None:
        check("oss_media_content", "asset_found", False, "no media_asset_000001 row")
        return
    asset_ref = row[0]
    try:
        data = _read_oss_object(asset_ref)
        check(
            "oss_media_content", "content_matches",
            data == SMALL_PNG,
            f"size={len(data)} expected={len(SMALL_PNG)}",
        )
        print(f"  media content: {len(data)} bytes, matches original PNG={data == SMALL_PNG}")
    except oss2.exceptions.NoSuchKey:
        check("oss_media_content", "object_exists", False, f"object {asset_ref} not found in OSS")


def assert_oss_evidence_rewritten(conn: psycopg.Connection, trace_id: str) -> None:
    """Read request_body evidence from OSS and verify audit-media: replacement."""
    print("\n=== Phase 9: OSS evidence rewrite assertions ===")
    row = conn.execute(
        "SELECT object_ref FROM raw_evidence_objects "
        "WHERE trace_id = %s AND object_type = 'request_body'",
        (trace_id,),
    ).fetchone()
    if row is None:
        check("oss_evidence_rewrite", "request_body_found", False, "no request_body row")
        return
    request_ref = row[0]
    try:
        body = _read_oss_object(request_ref).decode("utf-8")
        check(
            "oss_evidence_rewrite", "contains_audit_media_ref",
            "audit-media:media_asset_000001" in body,
            "expected 'audit-media:media_asset_000001' in evidence JSON",
        )
        check(
            "oss_evidence_rewrite", "base64_removed",
            SMALL_PNG_B64 not in body,
            "base64 data should be replaced by reference",
        )
        print(f"  request_body evidence: {len(body)} chars, audit-media ref present")
    except oss2.exceptions.NoSuchKey:
        check("oss_evidence_rewrite", "object_readable", False, f"object {request_ref} not found in OSS")


def assert_sha256_updated(conn: psycopg.Connection, trace_id: str) -> None:
    print("\n=== Phase 10: SHA256 update assertion ===")
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
    _require_oss_env()

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
        assert_trace_fields(conn, trace_id, "oss-media-capture", "openai_chat")

    push_job_to_redis(trace)

    # Run worker with OSS config
    worker_json = run_worker()

    # Assert
    assert_worker_result(worker_json, trace_id)
    with psycopg.connect(PG_DSN) as conn:
        assert_oss_media_assets(conn, trace_id)
        assert_sha256_updated(conn, trace_id)
        assert_oss_media_content(conn, trace_id)
        assert_oss_evidence_rewritten(conn, trace_id)

    report_results(1)


if __name__ == "__main__":
    main()
