#!/usr/bin/env python3
"""Shared helpers for e2e tests (pytest).

Connects to the deployed docker stack via service names (no host port
publishing, no go-run gateway). All endpoints/models are env-driven.
"""

from __future__ import annotations

import os
import sys
import time
from typing import NoReturn

import psycopg
import requests

# ---------------------------------------------------------------------------
# Config (env-driven; defaults match the compose e2e service)
# ---------------------------------------------------------------------------

GATEWAY_URL = os.environ.get("AUDIT_GATEWAY_URL", "http://audit-gateway:8080").rstrip("/")
UPSTREAM_URL = os.environ.get("NEW_API_BASE_URL", "http://host.docker.internal:3000").rstrip("/")
API_KEY = os.environ.get("E2E_API_KEY", "")
OPENAI_MODEL = os.environ.get("E2E_OPENAI_MODEL", "gpt-5.4")
CLAUDE_MODEL = os.environ.get("E2E_CLAUDE_MODEL", "claude-sonnet-4-6")
PG_DSN = os.environ.get(
    "POSTGRES_DSN",
    "postgres://audit:audit@postgres:5432/audit_gateway?sslmode=disable",
)
REDIS_URL = os.environ.get("REDIS_URL", "redis://redis:6379/0")
EVIDENCE_STORAGE_BACKEND = os.environ.get("EVIDENCE_STORAGE_BACKEND", "filesystem")
EVIDENCE_STORAGE_DIR = os.environ.get("EVIDENCE_STORAGE_DIR", "/evidence")
EXPECTED_USERNAME = "dave.zhao"

HEADERS = {
    "Authorization": f"Bearer {API_KEY}",
    "Content-Type": "application/json",
}

_http = requests.Session()
_http.trust_env = False  # bypass local proxies (Clash/V2Ray)

# ---------------------------------------------------------------------------
# Assertion helpers (collect into module-level `errors`; conftest resets it)
# ---------------------------------------------------------------------------

TraceResult = dict[str, str | int | None]

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


def bail(msg: str) -> NoReturn:
    print(f"FATAL: {msg}", file=sys.stderr)
    raise AssertionError(msg)


def assert_no_errors() -> None:
    """Call at the end of each test: fail if any assertion was recorded."""
    if errors:
        raise AssertionError(f"{len(errors)} assertion(s) failed:\n" + "\n".join(f"  {e}" for e in errors))


# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------


def gateway_post(endpoint: str, body: dict, label: str) -> tuple[requests.Response | None, str]:
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


def gateway_stream(endpoint: str, body: dict, label: str) -> tuple[requests.Response | None, str]:
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


def preflight(endpoint: str, body: dict, model_label: str = "") -> None:
    """Verify upstream new-api is reachable and the model resolves."""
    print("=== Pre-flight check ===")
    if not API_KEY:
        bail("E2E_API_KEY is empty")
    try:
        resp = _http.post(f"{UPSTREAM_URL}{endpoint}", headers=HEADERS, json=body, timeout=30)
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


def wait_for_rows(sql: str, params: tuple, expected: int, timeout: int = 30, label: str = "rows") -> int:
    """Poll until a query returns >= expected rows; returns the count found."""
    n = 0
    for attempt in range(timeout):
        with psycopg.connect(PG_DSN) as conn:
            n = conn.execute(sql, params).fetchone()[0]
        if n >= expected:
            print(f"  {label}: {n} found (attempt {attempt + 1})")
            return n
        print(f"  {label}: {n}/{expected} (attempt {attempt + 1})...")
        time.sleep(1)
    print(f"  WARNING: only {n}/{expected} {label} after {timeout}s")
    return n


def assert_trace_fields(
    conn: psycopg.Connection,
    trace_id: str,
    ctx: str,
    protocol_family: str,
    model: str | None = None,
    require_usage: bool = True,
) -> str | None:
    print(f"  Checking {ctx} (trace_id={trace_id})")
    cur = conn.execute(f"SELECT {TRACE_FIELDS} FROM traces WHERE trace_id = %s", (trace_id,))
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
    if require_usage:
        gt(ctx, "usage_total_tokens", t["usage_total_tokens"], 0)
        gt(ctx, "usage_prompt_tokens", t["usage_prompt_tokens"], 0)
    else:
        print(f"    usage_total_tokens={t['usage_total_tokens']} (upstream may omit usage)")
    if t["model_upstream"]:
        not_empty(ctx, "model_upstream", t["model_upstream"])
    return t.get("token_fingerprint")


def assert_evidence_objects(conn: psycopg.Connection, trace_id: str, ctx: str) -> None:
    rows = conn.execute(
        "SELECT object_type FROM raw_evidence_objects WHERE trace_id = %s",
        (trace_id,),
    ).fetchall()
    types_found = {row[0] for row in rows}
    check(f"{ctx}.evidence.request_body", "request_body" in types_found, f"object_types={types_found}")
    check(f"{ctx}.evidence.response_body", "response_body" in types_found, f"object_types={types_found}")


def assert_identity_cache(conn: psycopg.Connection, fingerprint: str) -> None:
    row = conn.execute(
        "SELECT username FROM token_identity_cache WHERE token_fingerprint = %s",
        (fingerprint,),
    ).fetchone()
    check(f"identity_cache({fingerprint[:12]}…)", row is not None, "no cache entry")
    if row:
        eq("identity_cache.username", "username", row[0], EXPECTED_USERNAME)


def read_request_raw_ref(trace_id: str) -> str:
    """Return the trace's request_raw_ref (for evidence-file inspection)."""
    with psycopg.connect(PG_DSN) as conn:
        row = conn.execute(
            "SELECT request_raw_ref FROM traces WHERE trace_id = %s",
            (trace_id,),
        ).fetchone()
    return row[0] if row else ""
