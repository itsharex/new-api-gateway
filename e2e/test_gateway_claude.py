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
            fp = assert_trace_fields(
                conn, r["trace_id"], ctx, PROTOCOL_FAMILY,
                model=MODEL, require_usage=False,
            )
            if fp:
                fingerprints.add(fp)
            assert_evidence_objects(conn, r["trace_id"], ctx)

        print("\n  Checking token_identity_cache ...")
        for fp in fingerprints:
            assert_identity_cache(conn, fp)

    report_results(len(all_results))


if __name__ == "__main__":
    main()
