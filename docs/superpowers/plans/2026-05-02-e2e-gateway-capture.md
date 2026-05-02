# E2E Gateway Capture Test Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a Python e2e test that sends real OpenAI-format requests through the gateway, then asserts database capture, identity resolution, and evidence storage are correct.

**Architecture:** Single Python script `e2e/test_gateway_capture.py` with its own `pyproject.toml` for dependency management via `uv run`. Three-phase flow: pre-flight upstream check → gateway requests (2 endpoints × 2 turns) → database assertions via psycopg.

**Tech Stack:** Python 3.11+, requests, psycopg[binary], uv

---

## File Structure

| Action | File | Responsibility |
|---|---|---|
| Create | `e2e/pyproject.toml` | Python deps: requests, psycopg[binary] |
| Create | `e2e/test_gateway_capture.py` | The entire e2e test script |

---

### Task 1: Create `e2e/pyproject.toml`

**Files:**
- Create: `e2e/pyproject.toml`

- [ ] **Step 1: Create the e2e directory and pyproject.toml**

```toml
[project]
name = "new-api-gateway-e2e"
version = "0.1.0"
description = "End-to-end tests for the new-api audit gateway"
requires-python = ">=3.11"
dependencies = [
    "requests>=2.31.0",
    "psycopg[binary]>=3.2.0",
]

[tool.uv]
package = false
```

Follows the same pattern as `workers/analysis_worker/pyproject.toml`.

- [ ] **Step 2: Verify uv can resolve dependencies**

Run: `cd /Users/roy/codes/new-api-gateway/e2e && uv sync`
Expected: dependencies installed successfully, `uv.lock` created.

---

### Task 2: Create the test script skeleton with config and helpers

**Files:**
- Create: `e2e/test_gateway_capture.py`

- [ ] **Step 1: Write the script skeleton — imports, config, helpers, and assertion utilities**

```python
#!/usr/bin/env python3
"""E2E test: gateway captures OpenAI-format requests correctly.

Verifies: proxy forwarding, trace persistence, identity resolution,
evidence storage, and token identity cache.

Prerequisites:
  - postgres, redis, new-api, audit-gateway all running
  - migrations applied
  - NEW_API_KEY set to a valid key associated with dave.zhao

Usage:
  export NEW_API_KEY=sk-...
  uv run e2e/test_gateway_capture.py
"""

from __future__ import annotations

import json
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
API_KEY = os.environ.get("NEW_API_KEY", "")
PG_DSN = os.environ.get(
    "POSTGRES_DSN",
    "postgres://audit:audit@localhost:5432/audit_gateway?sslmode=disable",
)
MODEL = os.environ.get("TEST_MODEL", "gpt-5.2")
EXPECTED_USERNAME = "dave.zhao"

HEADERS = {
    "Authorization": f"Bearer {API_KEY}",
    "Content-Type": "application/json",
}

# ---------------------------------------------------------------------------
# Assertion helpers
# ---------------------------------------------------------------------------

errors: list[str] = []


def check(label: str, condition: bool, detail: str = "") -> None:
    """Record a failure if *condition* is False."""
    if not condition:
        msg = f"FAIL [{label}] {detail}" if detail else f"FAIL [{label}]"
        errors.append(msg)
        print(msg)


def eq(context: str, field: str, got: object, want: object) -> None:
    check(f"{context}.{field}", got == want, f"got={got!r} want={want!r}")


def not_empty(context: str, field: str, got: object) -> None:
    check(f"{context}.{field}", bool(got), f"got empty/zero value")


def starts_with(context: str, field: str, got: str, prefix: str) -> None:
    check(f"{context}.{field}", got.startswith(prefix), f"got={got!r} want prefix={prefix!r}")


def gt(context: str, field: str, got: int, threshold: int) -> None:
    check(f"{context}.{field}", got > threshold, f"got={got} want > {threshold}")


def bail(msg: str) -> None:
    print(f"FATAL: {msg}", file=sys.stderr)
    sys.exit(1)


# ---------------------------------------------------------------------------
# Phase 1: Pre-flight — verify upstream is reachable
# ---------------------------------------------------------------------------

def preflight() -> None:
    print("=== Phase 1: Pre-flight check ===")
    if not API_KEY:
        bail("NEW_API_KEY environment variable is required")
    body = {
        "model": MODEL,
        "messages": [{"role": "user", "content": "ping"}],
        "max_tokens": 1,
    }
    try:
        resp = requests.post(
            f"{UPSTREAM_URL}/v1/chat/completions",
            headers=HEADERS,
            json=body,
            timeout=30,
        )
    except requests.ConnectionError as exc:
        bail(f"Cannot reach upstream at {UPSTREAM_URL}: {exc}")
    if resp.status_code != 200:
        bail(
            f"Upstream returned {resp.status_code} for model={MODEL}: "
            f"{resp.text[:500]}"
        )
    print(f"  Upstream OK (model={MODEL}, status={resp.status_code})")


# ---------------------------------------------------------------------------
# Phase 2: Send requests through the gateway
# ---------------------------------------------------------------------------

TraceResult = dict[str, str | int | None]


def send_chat_completions() -> list[TraceResult]:
    """Two-turn conversation via /v1/chat/completions."""
    print("\n=== Phase 2a: /v1/chat/completions ===")
    results: list[TraceResult] = []

    # Turn 1
    body1 = {
        "model": MODEL,
        "messages": [{"role": "user", "content": "hello"}],
        "max_tokens": 10,
    }
    resp1 = requests.post(
        f"{GATEWAY_URL}/v1/chat/completions",
        headers=HEADERS,
        json=body1,
        timeout=60,
    )
    trace1 = resp1.headers.get("x-audit-trace-id", "")
    assistant_reply_1 = resp1.json()["choices"][0]["message"]["content"]
    print(f"  Turn 1: status={resp1.status_code} trace_id={trace1}")
    print(f"          assistant: {assistant_reply_1[:60]}")
    results.append({
        "trace_id": trace1,
        "endpoint": "/v1/chat/completions",
        "turn": 1,
        "status_code": resp1.status_code,
    })

    # Turn 2 (multi-turn with history)
    body2 = {
        "model": MODEL,
        "messages": [
            {"role": "user", "content": "hello"},
            {"role": "assistant", "content": assistant_reply_1},
            {"role": "user", "content": "what is 1+1?"},
        ],
        "max_tokens": 10,
    }
    resp2 = requests.post(
        f"{GATEWAY_URL}/v1/chat/completions",
        headers=HEADERS,
        json=body2,
        timeout=60,
    )
    trace2 = resp2.headers.get("x-audit-trace-id", "")
    print(f"  Turn 2: status={resp2.status_code} trace_id={trace2}")
    results.append({
        "trace_id": trace2,
        "endpoint": "/v1/chat/completions",
        "turn": 2,
        "status_code": resp2.status_code,
    })

    return results


def send_responses() -> list[TraceResult]:
    """Two-turn conversation via /v1/responses."""
    print("\n=== Phase 2b: /v1/responses ===")
    results: list[TraceResult] = []

    # Turn 1
    body1 = {
        "model": MODEL,
        "input": "hello",
        "max_output_tokens": 10,
    }
    resp1 = requests.post(
        f"{GATEWAY_URL}/v1/responses",
        headers=HEADERS,
        json=body1,
        timeout=60,
    )
    trace1 = resp1.headers.get("x-audit-trace-id", "")
    resp1_json = resp1.json()
    response_id_1 = resp1_json.get("id", "")
    print(f"  Turn 1: status={resp1.status_code} trace_id={trace1} resp_id={response_id_1}")
    results.append({
        "trace_id": trace1,
        "endpoint": "/v1/responses",
        "turn": 1,
        "status_code": resp1.status_code,
    })

    # Turn 2 (chained via previous_response_id)
    body2 = {
        "model": MODEL,
        "previous_response_id": response_id_1,
        "input": "what is 1+1?",
        "max_output_tokens": 10,
    }
    resp2 = requests.post(
        f"{GATEWAY_URL}/v1/responses",
        headers=HEADERS,
        json=body2,
        timeout=60,
    )
    trace2 = resp2.headers.get("x-audit-trace-id", "")
    print(f"  Turn 2: status={resp2.status_code} trace_id={trace2}")
    results.append({
        "trace_id": trace2,
        "endpoint": "/v1/responses",
        "turn": 2,
        "status_code": resp2.status_code,
    })

    return results


# ---------------------------------------------------------------------------
# Phase 3: Database assertions
# ---------------------------------------------------------------------------

PROTOCOL_FAMILY = {
    "/v1/chat/completions": "openai_chat",
    "/v1/responses": "openai_responses",
}

TRACE_FIELDS = """
    trace_id, identity_resolution_status, username_snapshot,
    token_fingerprint, fingerprint_display,
    protocol_family, capture_mode, status_code,
    request_body_size, response_body_size,
    request_raw_ref, response_raw_ref,
    model_requested
""".strip().replace("\n", "").replace("  ", " ")


def assert_traces(conn: psycopg.Connection, results: list[TraceResult]) -> None:
    """Assert trace fields in the database for each captured request."""
    print("\n=== Phase 3: Database assertions ===")
    fingerprint_values: set[str] = set()

    for r in results:
        trace_id = r["trace_id"]
        endpoint = r["endpoint"]
        turn = r["turn"]
        ctx = f"{endpoint}:turn{turn}"
        print(f"\n  Checking {ctx} (trace_id={trace_id})")

        if not trace_id:
            check(ctx, False, "x-audit-trace-id header was empty")
            continue

        row = conn.execute(
            f"SELECT {TRACE_FIELDS} FROM traces WHERE trace_id = %s",
            (trace_id,),
        ).fetchone()

        check(f"{ctx}.trace_exists", row is not None, "no row in traces table")
        if row is None:
            continue

        col_names = [desc.name for desc in conn.execute(
            f"SELECT {TRACE_FIELDS} FROM traces LIMIT 0"
        ).description]
        t = dict(zip(col_names, row))

        eq(ctx, "identity_resolution_status", t["identity_resolution_status"], "resolved")
        eq(ctx, "username_snapshot", t["username_snapshot"], EXPECTED_USERNAME)
        not_empty(ctx, "token_fingerprint", t["token_fingerprint"])
        starts_with(ctx, "fingerprint_display", t["fingerprint_display"], "tkfp_")
        eq(ctx, "protocol_family", t["protocol_family"], PROTOCOL_FAMILY[endpoint])
        eq(ctx, "capture_mode", t["capture_mode"], "raw_and_normalized")
        eq(ctx, "status_code", t["status_code"], 200)
        gt(ctx, "request_body_size", t["request_body_size"], 0)
        gt(ctx, "response_body_size", t["response_body_size"], 0)
        not_empty(ctx, "request_raw_ref", t["request_raw_ref"])
        not_empty(ctx, "response_raw_ref", t["response_raw_ref"])
        eq(ctx, "model_requested", t["model_requested"], MODEL)

        if t["token_fingerprint"]:
            fingerprint_values.add(t["token_fingerprint"])

    # Evidence objects: each trace must have request_body + response_body
    print("\n  Checking raw_evidence_objects ...")
    for r in results:
        trace_id = r["trace_id"]
        if not trace_id:
            continue
        ctx = f"{r['endpoint']}:turn{r['turn']}"
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

    # Token identity cache
    print("  Checking token_identity_cache ...")
    for fp in fingerprint_values:
        row = conn.execute(
            "SELECT username FROM token_identity_cache WHERE token_fingerprint = %s",
            (fp,),
        ).fetchone()
        check(
            f"identity_cache({fp[:12]}…)",
            row is not None,
            "no cache entry",
        )
        if row:
            eq("identity_cache.username", "username", row[0], EXPECTED_USERNAME)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    preflight()

    all_results: list[TraceResult] = []
    all_results.extend(send_chat_completions())
    all_results.extend(send_responses())

    # Brief pause to allow async trace insertion to complete
    time.sleep(1)

    with psycopg.connect(PG_DSN) as conn:
        assert_traces(conn, all_results)

    print(f"\n{'=' * 50}")
    if errors:
        print(f"FAILED: {len(errors)} assertion(s) failed:\n")
        for e in errors:
            print(f"  {e}")
        sys.exit(1)
    else:
        print(f"PASSED: all {len(all_results)} trace(s) verified.")


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Make the script executable**

Run: `chmod +x /Users/roy/codes/new-api-gateway/e2e/test_gateway_capture.py`

- [ ] **Step 3: Commit**

```bash
git add e2e/pyproject.toml e2e/test_gateway_capture.py
git commit -m "feat(e2e): add gateway capture e2e test for chat completions and responses endpoints"
```

---

### Task 3: Manual smoke run

- [ ] **Step 1: Run the test against a live environment**

Ensure the gateway and all dependencies are running, then:

```bash
cd /Users/roy/codes/new-api-gateway
export NEW_API_KEY=sk-G0YzOkt9WQAwp8S9DL9mLKlcFNEYRjdnA4x6PMrNRgZA05l8
uv run --project e2e e2e/test_gateway_capture.py
```

Expected: all assertions pass, output ends with `PASSED: all 4 trace(s) verified.`

If any assertion fails, inspect the `FAIL [...]` lines, fix the issue (in the test or in gateway code), and re-run.
