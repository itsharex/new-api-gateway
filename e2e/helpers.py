#!/usr/bin/env python3
"""Shared helpers for e2e test scripts."""

from __future__ import annotations

import glob
import json
import os
import signal
import subprocess
import sys
import time
from urllib.parse import urlparse, urlunparse

import psycopg
import redis
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
    token_name_snapshot, username_snapshot,
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
    require_usage: bool = True,
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
    if require_usage:
        gt(ctx, "usage_total_tokens", t["usage_total_tokens"], 0)
        gt(ctx, "usage_prompt_tokens", t["usage_prompt_tokens"], 0)
    else:
        print(f"    usage_total_tokens={t['usage_total_tokens']} usage_prompt_tokens={t['usage_prompt_tokens']} (upstream may omit usage)")
    if t["model_upstream"]:
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
    check(f"identity_cache({fingerprint[:12]}...)", row is not None, "no cache entry")
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


# ---------------------------------------------------------------------------
# Test database helpers
# ---------------------------------------------------------------------------

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
WORKER_DIR = os.path.join(REPO_ROOT, "workers", "analysis_worker")
MIGRATIONS_DIR = os.path.join(REPO_ROOT, "migrations")


def make_dsn(db_name: str) -> str:
    """Return PG_DSN with the database name replaced."""
    parsed = urlparse(PG_DSN)
    return urlunparse(parsed._replace(path=f"/{db_name}"))


def create_test_database(db_name: str) -> str:
    """Drop and recreate a test database. Returns the new DSN."""
    admin_dsn = make_dsn("postgres")
    with psycopg.connect(admin_dsn, autocommit=True) as conn:
        conn.execute(f"DROP DATABASE IF EXISTS {db_name} WITH (FORCE)")
        conn.execute(f"CREATE DATABASE {db_name}")
    print(f"  Created test database: {db_name}")
    return make_dsn(db_name)


def apply_migrations(dsn: str) -> None:
    """Apply all migration SQL files to the given database."""
    sql_files = sorted(glob.glob(os.path.join(MIGRATIONS_DIR, "*.sql")))
    with psycopg.connect(dsn) as conn:
        for path in sql_files:
            with open(path) as f:
                conn.execute(f.read())
    print(f"  Applied {len(sql_files)} migration(s)")


def flush_redis() -> None:
    """Flush the current Redis database."""
    r = redis.Redis.from_url(REDIS_URL)
    r.flushdb()
    print("  Flushed Redis DB")


def run_worker_once(*, postgres_dsn: str, evidence_dir: str) -> dict:
    """Run the analysis worker once (--redis-once), return parsed JSON output."""
    env = {
        **os.environ,
        "POSTGRES_DSN": postgres_dsn,
        "EVIDENCE_STORAGE_DIR": evidence_dir,
        "REDIS_URL": REDIS_URL,
    }
    result = subprocess.run(
        ["uv", "run", "python", "main.py", "--redis-once"],
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
    lines = [ln.strip() for ln in result.stdout.strip().splitlines() if ln.strip()]
    if not lines:
        bail(f"Worker produced no stdout. stderr: {result.stderr[:500]}")
    worker_json = json.loads(lines[-1])
    print(f"  Worker result: {json.dumps(worker_json, indent=2)}")
    return worker_json


# ---------------------------------------------------------------------------
# Gateway process management
# ---------------------------------------------------------------------------

class GatewayManager:
    """Manages the audit-gateway process lifecycle for e2e tests."""

    def __init__(self) -> None:
        self._process: subprocess.Popen | None = None
        self._log_file = None
        self._log_path = "/tmp/e2e-gateway.log"

    def start(self, mode: str) -> None:
        """Start gateway with the given storage backend mode (filesystem/oss)."""
        if mode not in ("filesystem", "oss"):
            bail(f"Unknown gateway mode: {mode!r}")

        self._kill_existing()

        env = dict(os.environ)
        if mode == "oss":
            for var in (
                "OSS_ENDPOINT",
                "OSS_BUCKET",
                "OSS_ACCESS_KEY_ID",
                "OSS_ACCESS_KEY_SECRET",
            ):
                if not os.environ.get(var):
                    bail(f"{var} is required for OSS mode")
            env["EVIDENCE_STORAGE_BACKEND"] = "oss"

        self._log_file = open(self._log_path, "w")
        self._process = subprocess.Popen(
            ["go", "run", "./cmd/audit-gateway"],
            cwd=REPO_ROOT,
            env=env,
            stdout=self._log_file,
            stderr=self._log_file,
        )
        print(f"  Gateway starting (mode={mode}, pid={self._process.pid})")

        for attempt in range(1, 31):
            try:
                resp = requests.get(f"{GATEWAY_URL}/healthz", timeout=1)
                if resp.status_code == 200:
                    print(f"  Gateway ready ({mode} mode, attempt {attempt})")
                    return
            except requests.RequestException:
                pass
            time.sleep(1)

        self._print_log_tail()
        bail(f"Gateway did not become ready after 30s (mode={mode})")

    def stop(self) -> None:
        """Stop the gateway process."""
        if self._process is None:
            return
        self._process.terminate()
        try:
            self._process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            self._process.kill()
            self._process.wait()
        if self._log_file:
            self._log_file.close()
            self._log_file = None
        print("  Gateway stopped")
        self._process = None

    def restart(self, mode: str) -> None:
        """Stop then start with a new mode."""
        self.stop()
        self.start(mode)

    def _kill_existing(self) -> None:
        """Kill any running audit-gateway processes."""
        result = subprocess.run(
            ["pgrep", "-f", "audit-gateway"],
            capture_output=True,
            text=True,
        )
        pids = [p.strip() for p in result.stdout.splitlines() if p.strip()]
        if not pids:
            return
        print(f"  Killing existing gateway PIDs: {' '.join(pids)}")
        for pid in pids:
            try:
                os.kill(int(pid), signal.SIGTERM)
            except (ProcessLookupError, ValueError):
                pass
        time.sleep(2)
        for pid in pids:
            try:
                os.kill(int(pid), signal.SIGKILL)
            except (ProcessLookupError, ValueError):
                pass

    def _print_log_tail(self) -> None:
        """Print last 20 lines of gateway log for debugging."""
        try:
            with open(self._log_path) as f:
                lines = f.readlines()
            if lines:
                print("  Last gateway log lines:")
                for line in lines[-20:]:
                    print(f"    {line.rstrip()}")
        except FileNotFoundError:
            pass
