# E2E 测试扩展实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 Claude `/v1/messages` 和 Worker 分析闭环的 e2e 测试，基于现有 Python 脚本风格。

**Architecture:** 从 `test_gateway_openai.py` 提取共享代码到 `helpers.py`，新增 `test_gateway_claude.py`（3 个场景）和 `test_gateway_worker_pipeline.py`（网关→Worker 闭环）。所有脚本独立运行，不引入 pytest。

**Tech Stack:** Python 3.11+, requests, psycopg, redis, uv

---

### Task 1: 创建 `e2e/helpers.py`

**Files:**
- Create: `e2e/helpers.py`

- [ ] **Step 1: 编写 helpers.py**

创建 `e2e/helpers.py`，从 `test_gateway_openai.py` 提取共享基础设施：

```python
#!/usr/bin/env python3
"""Shared helpers for e2e test scripts."""

from __future__ import annotations

import os
import sys
import time

import psycopg
import requests

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

GATEWAY_URL = os.environ.get("AUDIT_GATEWAY_URL", "http://localhost:8080").rstrip("/")
UPSTREAM_URL = os.environ.get("NEW_API_BASE_URL", "http://localhost:3000").rstrip("/")
API_KEY = "sk-G0YzOkt9WQAwp8S9DL9mLKlcFNEYRjdnA4x6PMrNRgZA05l8"
PG_DSN = os.environ.get(
    "POSTGRES_DSN",
    "postgres://audit:audit@localhost:5432/audit_gateway?sslmode=disable",
)
REDIS_URL = os.environ.get("REDIS_URL", "redis://localhost:6379/0")
EXPECTED_USERNAME = "dave.zhao"

HEADERS = {
    "Authorization": f"Bearer {API_KEY}",
    "Content-Type": "application/json",
}

_http = requests.Session()
_http.trust_env = False

# ---------------------------------------------------------------------------
# Type alias
# ---------------------------------------------------------------------------

TraceResult = dict[str, str | int | None]

# ---------------------------------------------------------------------------
# Assertion helpers
# ---------------------------------------------------------------------------

errors: list[str] = []


def check(label: str, condition: bool, detail: str = "") -> None:
    if not condition:
        msg = f"FAIL [{label}] {detail}" if detail else f"FAIL [{label}]"
        errors.append(msg)
        print(msg)


def eq(context: str, field: str, got: object, want: object) -> None:
    check(f"{context}.{field}", got == want, f"got={got!r} want={want!r}")


def not_empty(context: str, field: str, got: object) -> None:
    check(f"{context}.{field}", bool(got), "got empty/zero value")


def starts_with(context: str, field: str, got: str, prefix: str) -> None:
    check(f"{context}.{field}", got.startswith(prefix), f"got={got!r} want prefix={prefix!r}")


def gt(context: str, field: str, got: int, threshold: int) -> None:
    check(f"{context}.{field}", got > threshold, f"got={got} want > {threshold}")


def bail(msg: str) -> None:
    print(f"FATAL: {msg}", file=sys.stderr)
    sys.exit(1)


# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------


def gateway_post(
    endpoint: str, body: dict, label: str
) -> tuple[requests.Response | None, str]:
    url = f"{GATEWAY_URL}{endpoint}"
    try:
        resp = _http.post(url, headers=HEADERS, json=body, timeout=60)
    except requests.RequestException as exc:
        msg = f"connection error: {exc}"
        check(label, False, msg)
        return None, msg
    if resp.status_code >= 300:
        msg = f"status={resp.status_code} body={resp.text[:200]}"
        check(label, False, msg)
        return resp, msg
    return resp, ""


def gateway_stream(
    endpoint: str, body: dict, label: str
) -> tuple[requests.Response | None, str]:
    url = f"{GATEWAY_URL}{endpoint}"
    try:
        resp = _http.post(url, headers=HEADERS, json=body, stream=True, timeout=60)
    except requests.RequestException as exc:
        msg = f"connection error: {exc}"
        check(label, False, msg)
        return None, msg
    if resp.status_code >= 300:
        msg = f"status={resp.status_code}"
        check(label, False, msg)
        return resp, msg
    for _ in resp.iter_lines():
        pass
    return resp, ""


# ---------------------------------------------------------------------------
# Preflight
# ---------------------------------------------------------------------------


def preflight(endpoint: str, body: dict, model_label: str = "") -> None:
    print("=== Phase 1: Pre-flight check ===")
    if not API_KEY:
        bail("API_KEY is empty")
    try:
        resp = _http.post(
            f"{UPSTREAM_URL}{endpoint}",
            headers=HEADERS,
            json=body,
            timeout=30,
        )
    except requests.ConnectionError as exc:
        bail(f"Cannot reach upstream at {UPSTREAM_URL}: {exc}")
    if resp.status_code != 200:
        label = f"model={model_label}" if model_label else f"endpoint={endpoint}"
        bail(f"Upstream returned {resp.status_code} for {label}: {resp.text[:500]}")
    info = f"model={model_label}" if model_label else f"endpoint={endpoint}"
    print(f"  Upstream OK ({info}, status={resp.status_code})")


# ---------------------------------------------------------------------------
# DB helpers
# ---------------------------------------------------------------------------

TRACE_FIELDS = """
    trace_id, identity_resolution_status, username_snapshot,
    token_fingerprint, fingerprint_display,
    protocol_family, capture_mode, status_code,
    request_body_size, response_body_size,
    request_raw_ref, response_raw_ref,
    model_requested, model_upstream,
    usage_total_tokens, usage_prompt_tokens, usage_completion_tokens
""".strip().replace("\n", "").replace("  ", " ")

TRACE_FIELDS_FOR_JOB = """
    trace_id, method, path, route_pattern, protocol_family, capture_mode,
    status_code, upstream_status_code, stream, request_started_at,
    request_body_size, response_body_size,
    request_raw_ref, response_raw_ref,
    token_fingerprint, fingerprint_display, new_api_token_id_snapshot,
    token_name_snapshot, username_snapshot, employee_no_snapshot,
    identity_resolution_status,
    model_requested, usage_total_tokens, usage_prompt_tokens, usage_completion_tokens
""".strip().replace("\n", "").replace("  ", " ")


def wait_for_traces(trace_ids: list[str], timeout: int = 10) -> None:
    print(f"\n  Waiting for {len(trace_ids)} trace(s) to appear in DB ...")
    found = 0
    for attempt in range(timeout):
        with psycopg.connect(PG_DSN) as conn:
            found = conn.execute(
                "SELECT count(*) FROM traces WHERE trace_id = ANY(%s)",
                (trace_ids,),
            ).fetchone()[0]
            if found >= len(trace_ids):
                print(f"  All {found} trace(s) found (attempt {attempt + 1})")
                return
        print(f"  Waiting ({found}/{len(trace_ids)}, attempt {attempt + 1})...")
        time.sleep(1)
    print(f"  WARNING: only {found}/{len(trace_ids)} traces found after {timeout}s")


def assert_trace_fields(
    conn: psycopg.Connection,
    trace_id: str,
    ctx: str,
    protocol_family: str,
    model: str | None = None,
) -> str | None:
    print(f"  Checking {ctx} (trace_id={trace_id})")

    cur = conn.execute(
        f"SELECT {TRACE_FIELDS} FROM traces WHERE trace_id = %s",
        (trace_id,),
    )
    row = cur.fetchone()
    if row is None:
        check(f"{ctx}.trace_exists", False, "no row in traces table")
        return None

    col_names = [desc.name for desc in cur.description]
    t = dict(zip(col_names, row))

    eq(ctx, "identity_resolution_status", t["identity_resolution_status"], "resolved")
    eq(ctx, "username_snapshot", t["username_snapshot"], EXPECTED_USERNAME)
    not_empty(ctx, "token_fingerprint", t["token_fingerprint"])
    starts_with(ctx, "fingerprint_display", t["fingerprint_display"], "tkfp_")
    eq(ctx, "protocol_family", t["protocol_family"], protocol_family)
    eq(ctx, "capture_mode", t["capture_mode"], "raw_and_normalized")
    eq(ctx, "status_code", t["status_code"], 200)
    gt(ctx, "request_body_size", t["request_body_size"], 0)
    gt(ctx, "response_body_size", t["response_body_size"], 0)
    not_empty(ctx, "request_raw_ref", t["request_raw_ref"])
    not_empty(ctx, "response_raw_ref", t["response_raw_ref"])
    if model:
        eq(ctx, "model_requested", t["model_requested"], model)
    gt(ctx, "usage_total_tokens", t["usage_total_tokens"], 0)
    gt(ctx, "usage_prompt_tokens", t["usage_prompt_tokens"], 0)
    not_empty(ctx, "model_upstream", t["model_upstream"])

    return t.get("token_fingerprint")


def assert_evidence_objects(
    conn: psycopg.Connection, trace_id: str, ctx: str
) -> None:
    rows = conn.execute(
        "SELECT object_type FROM raw_evidence_objects WHERE trace_id = %s",
        (trace_id,),
    ).fetchall()
    types_found = {row[0] for row in rows}
    check(
        f"{ctx}.evidence.request_body",
        "request_body" in types_found,
        f"object_types={types_found}",
    )
    check(
        f"{ctx}.evidence.response_body",
        "response_body" in types_found,
        f"object_types={types_found}",
    )


def assert_identity_cache(conn: psycopg.Connection, fingerprint: str) -> None:
    row = conn.execute(
        "SELECT username FROM token_identity_cache WHERE token_fingerprint = %s",
        (fingerprint,),
    ).fetchone()
    check(f"identity_cache({fingerprint[:12]}…)", row is not None, "no cache entry")
    if row:
        eq("identity_cache.username", "username", row[0], EXPECTED_USERNAME)


def read_trace_for_job(
    conn: psycopg.Connection, trace_id: str
) -> dict | None:
    cur = conn.execute(
        f"SELECT {TRACE_FIELDS_FOR_JOB} FROM traces WHERE trace_id = %s",
        (trace_id,),
    )
    row = cur.fetchone()
    if row is None:
        return None
    col_names = [desc.name for desc in cur.description]
    return dict(zip(col_names, row))


def build_job_payload(trace: dict) -> dict:
    return {
        "type": "trace_captured",
        "trace_id": trace["trace_id"],
        "route_pattern": trace["route_pattern"],
        "protocol_family": trace["protocol_family"],
        "capture_mode": trace["capture_mode"],
        "username": trace.get("username_snapshot", ""),
        "employee_no": trace.get("employee_no_snapshot", ""),
        "token_fingerprint": trace["token_fingerprint"],
        "fingerprint_display": trace["fingerprint_display"],
        "new_api_token_id": trace.get("new_api_token_id_snapshot", 0) or 0,
        "token_name_snapshot": trace.get("token_name_snapshot", ""),
        "identity_resolution_status": trace["identity_resolution_status"],
        "status_code": trace["status_code"],
        "upstream_status_code": trace.get("upstream_status_code", 200) or 200,
        "stream": trace["stream"],
        "request_started_at": str(trace["request_started_at"]),
        "request_body_size": trace["request_body_size"],
        "response_body_size": trace["response_body_size"],
        "request_raw_ref": trace["request_raw_ref"],
        "response_raw_ref": trace["response_raw_ref"],
        "request_content_type": "application/json",
        "response_content_type": "application/json",
        "model_requested": trace["model_requested"],
        "usage_prompt_tokens": trace.get("usage_prompt_tokens", 0) or 0,
        "usage_completion_tokens": trace.get("usage_completion_tokens", 0) or 0,
        "usage_total_tokens": trace["usage_total_tokens"],
        "usage_reasoning_tokens": 0,
        "usage_cached_tokens": 0,
    }


def report_results(total_count: int) -> None:
    print(f"\n{'=' * 50}")
    if errors:
        print(f"FAILED: {len(errors)} assertion(s) failed:\n")
        for e in errors:
            print(f"  {e}")
        sys.exit(1)
    else:
        print(f"PASSED: all {total_count} trace(s) verified.")
```

- [ ] **Step 2: 语法检查**

Run: `python3 -c "import ast; ast.parse(open('e2e/helpers.py').read()); print('OK')"`
Expected: `OK`

- [ ] **Step 3: Commit**

```bash
git add e2e/helpers.py
git commit -m "feat(e2e): add shared helpers module for e2e tests"
```

---

### Task 2: 创建 `e2e/test_gateway_claude.py`

**Files:**
- Create: `e2e/test_gateway_claude.py`

- [ ] **Step 1: 编写 test_gateway_claude.py**

```python
#!/usr/bin/env python3
"""E2E test: gateway captures Claude /v1/messages requests correctly.

Verifies proxy forwarding, trace persistence, identity resolution,
evidence storage, and token identity cache for the Claude protocol family.

Prerequisites:
  - postgres, redis, new-api, audit-gateway all running
  - migrations applied
  - new-api supports routing Claude requests

Usage:
  uv run e2e/test_gateway_claude.py
"""

from __future__ import annotations

import os
import sys

import psycopg

from helpers import (
    HEADERS,
    MODEL as OPENAI_MODEL,
    PG_DSN,
    TraceResult,
    assert_evidence_objects,
    assert_identity_cache,
    assert_trace_fields,
    bail,
    errors,
    gateway_post,
    gateway_stream,
    preflight,
    report_results,
    wait_for_traces,
)

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

MODEL = os.environ.get("CLAUDE_MODEL", "claude-sonnet-4-6")
ENDPOINT = "/v1/messages"
PROTOCOL_FAMILY = "claude_messages"


# ---------------------------------------------------------------------------
# Phase 2: Send requests through the gateway
# ---------------------------------------------------------------------------


def send_claude_single_turn() -> list[TraceResult]:
    """Single-turn non-streaming via /v1/messages."""
    print("\n=== Phase 2a: /v1/messages single-turn (non-streaming) ===")
    resp, err = gateway_post(
        ENDPOINT,
        {
            "model": MODEL,
            "messages": [{"role": "user", "content": "hello"}],
            "max_tokens": 10,
        },
        "/v1/messages:turn1",
    )
    if err or resp is None:
        return [{"trace_id": "", "endpoint": ENDPOINT, "turn": 1, "status_code": 0}]
    trace_id = resp.headers.get("x-audit-trace-id", "")
    print(f"  Turn 1: status={resp.status_code} trace_id={trace_id}")
    return [{"trace_id": trace_id, "endpoint": ENDPOINT, "turn": 1, "status_code": resp.status_code}]


def send_claude_multi_turn() -> list[TraceResult]:
    """Multi-turn non-streaming via /v1/messages."""
    print("\n=== Phase 2b: /v1/messages multi-turn (non-streaming) ===")
    results: list[TraceResult] = []

    # Turn 1
    resp1, err1 = gateway_post(
        ENDPOINT,
        {
            "model": MODEL,
            "messages": [{"role": "user", "content": "hello"}],
            "max_tokens": 10,
        },
        "/v1/messages:multi1",
    )
    if err1 or resp1 is None:
        return [{"trace_id": "", "endpoint": ENDPOINT, "turn": 1, "status_code": 0}]
    trace1 = resp1.headers.get("x-audit-trace-id", "")
    body1 = resp1.json()
    # Claude response format: {"content": [{"type": "text", "text": "..."}], ...}
    assistant_reply = ""
    for block in body1.get("content", []):
        if block.get("type") == "text":
            assistant_reply = block.get("text", "")
            break
    print(f"  Turn 1: status={resp1.status_code} trace_id={trace1}")
    print(f"          assistant: {assistant_reply[:60]}")
    results.append({"trace_id": trace1, "endpoint": ENDPOINT, "turn": 1, "status_code": resp1.status_code})

    # Turn 2 (multi-turn with history)
    resp2, err2 = gateway_post(
        ENDPOINT,
        {
            "model": MODEL,
            "messages": [
                {"role": "user", "content": "hello"},
                {"role": "assistant", "content": assistant_reply},
                {"role": "user", "content": "what is 1+1?"},
            ],
            "max_tokens": 10,
        },
        "/v1/messages:multi2",
    )
    if err2 or resp2 is None:
        results.append({"trace_id": "", "endpoint": ENDPOINT, "turn": 2, "status_code": 0})
        return results
    trace2 = resp2.headers.get("x-audit-trace-id", "")
    print(f"  Turn 2: status={resp2.status_code} trace_id={trace2}")
    results.append({"trace_id": trace2, "endpoint": ENDPOINT, "turn": 2, "status_code": resp2.status_code})

    return results


def send_claude_stream() -> list[TraceResult]:
    """Single-turn SSE streaming via /v1/messages."""
    print("\n=== Phase 2c: /v1/messages (stream) ===")
    resp, err = gateway_stream(
        ENDPOINT,
        {
            "model": MODEL,
            "messages": [{"role": "user", "content": "hello"}],
            "max_tokens": 10,
            "stream": True,
        },
        "/v1/messages:stream",
    )
    if err or resp is None:
        return [{"trace_id": "", "endpoint": ENDPOINT, "turn": 0, "status_code": 0}]
    trace_id = resp.headers.get("x-audit-trace-id", "")
    print(f"  Stream: status={resp.status_code} trace_id={trace_id}")
    return [{"trace_id": trace_id, "endpoint": ENDPOINT, "turn": 0, "status_code": resp.status_code}]


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    preflight(
        ENDPOINT,
        {"model": MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=MODEL,
    )

    all_results: list[TraceResult] = []
    all_results.extend(send_claude_single_turn())
    all_results.extend(send_claude_multi_turn())
    all_results.extend(send_claude_stream())

    trace_ids = [r["trace_id"] for r in all_results if r["trace_id"]]
    wait_for_traces(trace_ids)

    fingerprints: set[str] = set()
    with psycopg.connect(PG_DSN) as conn:
        print("\n=== Phase 3: Database assertions ===")
        for r in all_results:
            if not r["trace_id"]:
                continue
            ctx = f"{r['endpoint']}:turn{r['turn']}"
            fp = assert_trace_fields(conn, r["trace_id"], ctx, PROTOCOL_FAMILY, model=MODEL)
            if fp:
                fingerprints.add(fp)
            assert_evidence_objects(conn, r["trace_id"], ctx)

        print("\n  Checking token_identity_cache ...")
        for fp in fingerprints:
            assert_identity_cache(conn, fp)

    report_results(len(all_results))


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: 语法检查**

Run: `cd e2e && python3 -c "import ast; ast.parse(open('test_gateway_claude.py').read()); print('OK')"`
Expected: `OK`

- [ ] **Step 3: Commit**

```bash
git add e2e/test_gateway_claude.py
git commit -m "feat(e2e): add Claude /v1/messages e2e test (non-streaming, multi-turn, SSE)"
```

---

### Task 3: 添加 redis 依赖到 e2e

**Files:**
- Modify: `e2e/pyproject.toml`

- [ ] **Step 1: 更新 pyproject.toml**

在 `dependencies` 列表中添加 `redis`：

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
]
```

- [ ] **Step 2: 更新 lockfile**

Run: `cd e2e && uv lock`

- [ ] **Step 3: Commit**

```bash
git add e2e/pyproject.toml e2e/uv.lock
git commit -m "feat(e2e): add redis dependency for worker pipeline test"
```

---

### Task 4: 创建 `e2e/test_gateway_worker_pipeline.py`

**Files:**
- Create: `e2e/test_gateway_worker_pipeline.py`

- [ ] **Step 1: 编写 test_gateway_worker_pipeline.py**

```python
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
```

- [ ] **Step 2: 语法检查**

Run: `cd e2e && python3 -c "import ast; ast.parse(open('test_gateway_worker_pipeline.py').read()); print('OK')"`
Expected: `OK`

- [ ] **Step 3: Commit**

```bash
git add e2e/test_gateway_worker_pipeline.py
git commit -m "feat(e2e): add Worker analysis pipeline e2e test (gateway → Redis → worker → DB)"
```

---

### Task 5: 更新 docstring 引用

**Files:**
- Modify: `e2e/test_gateway_openai.py:14`

- [ ] **Step 1: 更新 Usage docstring**

将 `test_gateway_openai.py` 第 14 行的 `uv run e2e/test_gateway_capture.py` 改为 `uv run e2e/test_gateway_openai.py`：

```python
# 改前
  uv run e2e/test_gateway_capture.py

# 改后
  uv run e2e/test_gateway_openai.py
```

- [ ] **Step 2: Commit**

```bash
git add e2e/test_gateway_openai.py
git commit -m "fix(e2e): update docstring reference after rename to test_gateway_openai.py"
```

---

### Task 6: 验证

- [ ] **Step 1: 运行 Go 单元测试确认无破坏**

Run: `make test`
Expected: 所有测试通过

- [ ] **Step 2: 验证 Python 语法正确**

Run: `cd e2e && python3 -c "import helpers; print('helpers OK')" && python3 -c "import ast; ast.parse(open('test_gateway_claude.py').read()); print('claude OK')" && python3 -c "import ast; ast.parse(open('test_gateway_worker_pipeline.py').read()); print('worker OK')"`
Expected: 三个 `OK`

- [ ] **Step 3: 验证 uv 依赖可解析**

Run: `cd e2e && uv sync`
Expected: 成功安装 requests, psycopg, redis
