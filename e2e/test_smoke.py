#!/usr/bin/env python3
"""Smoke test: one request per protocol endpoint, verify trace captured.

Covers:
  1. OpenAI Chat   POST /v1/chat/completions
  2. OpenAI Resp   POST /v1/responses
  3. Claude         POST /v1/messages

Prerequisites:
  - new-api and audit-gateway running (gateway port exposed to host)
  - postgres reachable from host (optional; DB checks skipped if unavailable)

Usage:
  uv run e2e/test_smoke.py
"""

from __future__ import annotations

import os
import sys

import psycopg

from helpers import (
    GATEWAY_URL,
    PG_DSN,
    TraceResult,
    assert_evidence_objects,
    assert_trace_fields,
    bail,
    errors,
    gateway_post,
    preflight,
    report_results,
    wait_for_traces,
)

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

OPENAI_MODEL = os.environ.get("TEST_MODEL", "gpt-5.2")
CLAUDE_MODEL = os.environ.get("CLAUDE_MODEL", "claude-sonnet-4-6")

CASES: list[tuple[str, dict, str, str]] = [
    # (endpoint, body, protocol_family, label)
    (
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 5},
        "openai_chat",
        "openai-chat",
    ),
    (
        "/v1/responses",
        {"model": OPENAI_MODEL, "input": "ping", "max_output_tokens": 5},
        "openai_responses",
        "openai-responses",
    ),
    (
        "/v1/messages",
        {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 5},
        "claude_messages",
        "claude-messages",
    ),
]


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    # Preflight: verify the first model is reachable upstream
    preflight(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=OPENAI_MODEL,
    )

    print(f"\n=== Phase 2: Smoke requests ({len(CASES)} endpoints) ===")
    results: list[TraceResult] = []

    for endpoint, body, _pf, label in CASES:
        print(f"\n  {label}: POST {endpoint}")
        resp, err = gateway_post(endpoint, body, label)
        if err or resp is None:
            results.append({"trace_id": "", "endpoint": endpoint, "turn": 0, "status_code": 0})
            continue
        trace_id = resp.headers.get("x-audit-trace-id", "")
        print(f"    status={resp.status_code} trace_id={trace_id}")
        results.append({
            "trace_id": trace_id,
            "endpoint": endpoint,
            "turn": 0,
            "status_code": resp.status_code,
        })

    trace_ids = [r["trace_id"] for r in results if r["trace_id"]]
    if not trace_ids:
        bail("No trace IDs received from any endpoint")

    # Phase 3: DB assertions (skip if postgres unreachable from host)
    try:
        psycopg.connect(PG_DSN).close()
    except psycopg.OperationalError:
        print("\n=== Phase 3: Database assertions (SKIPPED — postgres unreachable) ===")
        report_results(len(results))
        return

    wait_for_traces(trace_ids)

    with psycopg.connect(PG_DSN) as conn:
        print("\n=== Phase 3: Database assertions ===")
        for r in results:
            if not r["trace_id"]:
                continue
            label = r["endpoint"]
            proto = next(p for ep, _, p, _ in CASES if ep == r["endpoint"])
            assert_trace_fields(
                conn, r["trace_id"], label, proto,
                require_usage=False,
            )
            assert_evidence_objects(conn, r["trace_id"], label)

    report_results(len(results))


if __name__ == "__main__":
    main()
