# OSS Evidence Storage E2E Tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 OSS e2e 测试覆盖 Gateway + Worker 全链路 OSS 后端，包含基本管线和媒体提取两个场景，通过 oss2 SDK 回读验证数据完整性。

**Architecture:** Shell 脚本控制 Gateway 生命周期（用 OSS env 重启），两个独立 Python e2e 文件分别测试管线和媒体提取。测试通过 DB 查询获取 `object_ref`，再用 oss2 SDK 回读验证内容。

**Tech Stack:** Bash, Python 3.11+, oss2, psycopg, redis, subprocess

---

## File Structure

| File | Responsibility |
|------|----------------|
| Create: `e2e/test_gateway_worker_pipeline_oss.py` | OSS 管线 e2e 测试 |
| Create: `e2e/test_media_extraction_oss.py` | OSS 媒体提取 e2e 测试 |
| Create: `scripts/e2e_oss_pipeline.sh` | Gateway 生命周期管理 + 测试运行入口 |
| Modify: `e2e/pyproject.toml` | 新增 oss2 依赖 |

---

### Task 1: Add oss2 dependency to e2e pyproject.toml

**Files:**
- Modify: `e2e/pyproject.toml`

- [ ] **Step 1: Add oss2 to dependencies**

Change `e2e/pyproject.toml` to:

```toml
[project]
name = "new-api-gateway-e2e"
version = "0.1.0"
description = "End-to-end tests for the new-api audit gateway"
requires-python = ">=3.11"
dependencies = [
    "requests>=2.31.0",
    "psycopg[binary]>=3.2.0",
    "redis>=5.0.0",
    "oss2>=2.19.0",
]

[tool.uv]
package = false
```

- [ ] **Step 2: Install dependency**

Run: `cd e2e && uv sync`
Expected: oss2 installed successfully.

- [ ] **Step 3: Commit**

```bash
git add e2e/pyproject.toml e2e/uv.lock
git commit -m "chore(e2e): add oss2 dependency for OSS e2e tests"
```

---

### Task 2: Create OSS pipeline e2e test

**Files:**
- Create: `e2e/test_gateway_worker_pipeline_oss.py`

This test verifies the full Gateway → Worker pipeline with OSS evidence storage. It mirrors `e2e/test_gateway_worker_pipeline.py` but sets `EVIDENCE_STORAGE_BACKEND=oss` and adds OSS-specific assertions.

**Key differences from the filesystem version:**
- Worker env sets `EVIDENCE_STORAGE_BACKEND=oss` + OSS credentials
- New `assert_oss_evidence_refs` checks `object_ref` prefix and `storage_backend`
- New `assert_oss_object_readable` reads back objects via oss2 SDK
- Preflight checks OSS env vars at startup

- [ ] **Step 1: Create the test file**

Create `e2e/test_gateway_worker_pipeline_oss.py`:

```python
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

EVIDENCE_DIR = os.environ.get("EVIDENCE_STORAGE_DIR", "var/evidence")
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
```

- [ ] **Step 2: Verify it collects**

Run: `cd e2e && uv run pytest test_gateway_worker_pipeline_oss.py --collect-only 2>&1 || true`
Expected: May fail to import — that's OK if oss2 is installed. The file will only be run via `scripts/e2e_oss_pipeline.sh`.

Actually, verify oss2 is importable:
Run: `cd e2e && uv run python -c "import oss2; print('oss2 OK')"`
Expected: `oss2 OK`

Then verify the script is syntactically valid:
Run: `cd e2e && uv run python -c "import py_compile; py_compile.compile('test_gateway_worker_pipeline_oss.py', doraise=True); print('syntax OK')"`
Expected: `syntax OK`

- [ ] **Step 3: Commit**

```bash
git add e2e/test_gateway_worker_pipeline_oss.py
git commit -m "test(e2e): add OSS pipeline e2e test"
```

---

### Task 3: Create OSS media extraction e2e test

**Files:**
- Create: `e2e/test_media_extraction_oss.py`

This test verifies base64 media extraction with OSS storage. It mirrors `e2e/test_media_extraction.py` but uses OSS for evidence read-back.

**Key differences from the filesystem version:**
- Worker env sets `EVIDENCE_STORAGE_BACKEND=oss` + OSS credentials
- `assert_oss_media_assets` checks media asset refs are `oss://` prefixed
- `assert_oss_media_content` reads back binary from OSS and compares with original `SMALL_PNG`
- `assert_oss_evidence_rewritten` reads request_body evidence from OSS and checks `audit-media:` replacement

- [ ] **Step 1: Create the test file**

Create `e2e/test_media_extraction_oss.py`:

```python
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

    assert_oss_media_content(PG_DSN, trace_id)
    assert_oss_evidence_rewritten(PG_DSN, trace_id)

    report_results(1)


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Verify syntax**

Run: `cd e2e && uv run python -c "import py_compile; py_compile.compile('test_media_extraction_oss.py', doraise=True); print('syntax OK')"`
Expected: `syntax OK`

- [ ] **Step 3: Commit**

```bash
git add e2e/test_media_extraction_oss.py
git commit -m "test(e2e): add OSS media extraction e2e test"
```

---

### Task 4: Create OSS e2e pipeline shell script

**Files:**
- Create: `scripts/e2e_oss_pipeline.sh`

This script manages the Gateway lifecycle: stops the existing Gateway, restarts it with `EVIDENCE_STORAGE_BACKEND=oss`, runs both Python e2e tests, then cleans up.

- [ ] **Step 1: Create the shell script**

Create `scripts/e2e_oss_pipeline.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

# OSS evidence storage e2e test runner.
# Stops the current Gateway, restarts it with EVIDENCE_STORAGE_BACKEND=oss,
# runs the OSS e2e tests, and cleans up on exit.
#
# Required environment variables:
#   OSS_ENDPOINT, OSS_BUCKET, OSS_ACCESS_KEY_ID, OSS_ACCESS_KEY_SECRET
#
# Usage:
#   export OSS_ENDPOINT=oss-cn-hangzhou.aliyuncs.com
#   export OSS_BUCKET=my-audit-evidence
#   export OSS_ACCESS_KEY_ID=...
#   export OSS_ACCESS_KEY_SECRET=...
#   ./scripts/e2e_oss_pipeline.sh

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly GATEWAY_URL="${AUDIT_GATEWAY_URL:-http://localhost:8080}"

# ---------------------------------------------------------------------------
# Check prerequisites
# ---------------------------------------------------------------------------

for var in OSS_ENDPOINT OSS_BUCKET OSS_ACCESS_KEY_ID OSS_ACCESS_KEY_SECRET; do
    if [[ -z "${!var:-}" ]]; then
        echo "ERROR: $var is required. Set it before running this script." >&2
        exit 1
    fi
done

echo "=== OSS E2E Test Runner ==="
echo "  OSS_ENDPOINT=$OSS_ENDPOINT"
echo "  OSS_BUCKET=$OSS_BUCKET"

# ---------------------------------------------------------------------------
# Stop existing Gateway
# ---------------------------------------------------------------------------

echo ""
echo "=== Stopping existing Gateway ==="
gateway_pid=$(pgrep -f "audit-gateway" || true)
if [[ -n "$gateway_pid" ]]; then
    echo "  Found Gateway PID(s): $(echo $gateway_pid | tr '\n' ' ')"
    echo $gateway_pid | xargs kill 2>/dev/null || true
    sleep 2
    # Force kill if still running
    for pid in $gateway_pid; do
        if kill -0 "$pid" 2>/dev/null; then
            echo "  Force killing PID $pid"
            kill -9 "$pid" 2>/dev/null || true
        fi
    done
    echo "  Gateway stopped"
else
    echo "  No running Gateway found"
fi

# ---------------------------------------------------------------------------
# Start Gateway with OSS backend
# ---------------------------------------------------------------------------

echo ""
echo "=== Starting Gateway with OSS backend ==="
(
    cd "$REPO_ROOT"
    EVIDENCE_STORAGE_BACKEND=oss \
    OSS_ENDPOINT="$OSS_ENDPOINT" \
    OSS_BUCKET="$OSS_BUCKET" \
    OSS_ACCESS_KEY_ID="$OSS_ACCESS_KEY_ID" \
    OSS_ACCESS_KEY_SECRET="$OSS_ACCESS_KEY_SECRET" \
    go run ./cmd/audit-gateway &>/tmp/oss-e2e-gateway.log &
)
GATEWAY_PID=$!
echo "  Gateway PID: $GATEWAY_PID"

# Ensure cleanup on exit
cleanup() {
    echo ""
    echo "=== Cleaning up ==="
    if kill -0 "$GATEWAY_PID" 2>/dev/null; then
        echo "  Stopping Gateway PID $GATEWAY_PID"
        kill "$GATEWAY_PID" 2>/dev/null || true
        sleep 1
        kill -9 "$GATEWAY_PID" 2>/dev/null || true
    fi
    echo "  Done"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Wait for Gateway to be ready
# ---------------------------------------------------------------------------

echo ""
echo "=== Waiting for Gateway ==="
max_attempts=30
for i in $(seq 1 $max_attempts); do
    if curl -sf "$GATEWAY_URL/healthz" >/dev/null 2>&1; then
        echo "  Gateway ready (attempt $i)"
        break
    fi
    if [[ $i -eq $max_attempts ]]; then
        echo "ERROR: Gateway did not become ready after $max_attempts attempts" >&2
        echo "  Last log lines:"
        tail -20 /tmp/oss-e2e-gateway.log 2>/dev/null || true
        exit 1
    fi
    sleep 1
done

# ---------------------------------------------------------------------------
# Run e2e tests
# ---------------------------------------------------------------------------

echo ""
echo "=== Running OSS pipeline e2e test ==="
cd "$REPO_ROOT"
uv run e2e/test_gateway_worker_pipeline_oss.py

echo ""
echo "=== Running OSS media extraction e2e test ==="
uv run e2e/test_media_extraction_oss.py

echo ""
echo "=== All OSS e2e tests passed ==="
```

- [ ] **Step 2: Make executable**

Run: `chmod +x scripts/e2e_oss_pipeline.sh`

- [ ] **Step 3: Verify shellcheck (if available)**

Run: `shellcheck scripts/e2e_oss_pipeline.sh 2>&1 || echo "shellcheck not installed, skipping"`
Expected: No errors (or "shellcheck not installed").

- [ ] **Step 4: Commit**

```bash
git add scripts/e2e_oss_pipeline.sh
git commit -m "test(e2e): add OSS e2e pipeline runner script"
```

---

### Task 5: Fix bug in test_media_extraction_oss.py assertions

**Files:**
- Modify: `e2e/test_media_extraction_oss.py`

In Task 3, the `assert_oss_media_content` and `assert_oss_evidence_rewritten` functions were incorrectly called with `PG_DSN` (a string) instead of a `psycopg.Connection`. Fix these calls in `main()`.

- [ ] **Step 1: Fix the connection parameter bug**

In `e2e/test_media_extraction_oss.py`, the bottom of `main()` currently reads:

```python
    assert_oss_media_content(PG_DSN, trace_id)
    assert_oss_evidence_rewritten(PG_DSN, trace_id)
```

Change to wrap them in a connection:

```python
    with psycopg.connect(PG_DSN) as conn:
        assert_oss_media_content(conn, trace_id)
        assert_oss_evidence_rewritten(conn, trace_id)
```

- [ ] **Step 2: Verify syntax**

Run: `cd e2e && uv run python -c "import py_compile; py_compile.compile('test_media_extraction_oss.py', doraise=True); print('syntax OK')"`
Expected: `syntax OK`

- [ ] **Step 3: Commit**

```bash
git add e2e/test_media_extraction_oss.py
git commit -m "fix(e2e): pass connection object to OSS assertion functions"
```

---

### Task 6: Run existing tests to verify no regressions

- [ ] **Step 1: Run Go tests**

Run: `go test ./...`
Expected: All tests pass.

- [ ] **Step 2: Run Python worker tests**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: All tests pass (130 passed, 2 skipped).

- [ ] **Step 3: Verify new e2e files are importable**

Run: `cd e2e && uv run python -c "import py_compile; py_compile.compile('test_gateway_worker_pipeline_oss.py', doraise=True); py_compile.compile('test_media_extraction_oss.py', doraise=True); print('all OK')"`
Expected: `all OK`
