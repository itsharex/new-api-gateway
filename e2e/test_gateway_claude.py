"""E2E: gateway captures Claude /v1/messages requests."""

from __future__ import annotations

import psycopg

import helpers
from helpers import (
    CLAUDE_MODEL,
    assert_evidence_objects,
    assert_identity_cache,
    assert_no_errors,
    assert_trace_fields,
    gateway_post,
    gateway_stream,
    preflight,
    wait_for_traces,
)

ENDPOINT = "/v1/messages"
PROTOCOL_FAMILY = "claude_messages"


def test_gateway_claude_capture():
    preflight(
        ENDPOINT,
        {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=CLAUDE_MODEL,
    )

    results = []  # (turn, trace_id)

    # single turn
    r1, e1 = gateway_post(ENDPOINT,
                          {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "hello"}], "max_tokens": 10},
                          "/v1/messages:turn1")
    if not e1 and r1 is not None:
        results.append((1, r1.headers.get("x-audit-trace-id", "")))

    # multi-turn
    reply = ""
    r2, e2 = gateway_post(ENDPOINT,
                          {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "hello"}], "max_tokens": 10},
                          "/v1/messages:multi1")
    if not e2 and r2 is not None:
        tid2 = r2.headers.get("x-audit-trace-id", "")
        results.append((1, tid2))
        for block in r2.json().get("content", []):
            if block.get("type") == "text":
                reply = block.get("text", "")
                break
        r3, e3 = gateway_post(ENDPOINT,
                              {"model": CLAUDE_MODEL,
                               "messages": [{"role": "user", "content": "hello"},
                                            {"role": "assistant", "content": reply},
                                            {"role": "user", "content": "what is 1+1?"}],
                               "max_tokens": 10},
                              "/v1/messages:multi2")
        if not e3 and r3 is not None:
            results.append((2, r3.headers.get("x-audit-trace-id", "")))

    # stream
    r4, e4 = gateway_stream(ENDPOINT,
                            {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "hello"}],
                             "max_tokens": 10, "stream": True},
                            "/v1/messages:stream")
    if not e4 and r4 is not None:
        results.append((0, r4.headers.get("x-audit-trace-id", "")))

    trace_ids = [tid for _, tid in results if tid]
    wait_for_traces(trace_ids)

    fingerprints = set()
    with psycopg.connect(helpers.PG_DSN) as conn:
        for turn, trace_id in results:
            if not trace_id:
                continue
            ctx = f"{ENDPOINT}:turn{turn}"
            fp = assert_trace_fields(conn, trace_id, ctx, PROTOCOL_FAMILY, model=CLAUDE_MODEL, require_usage=False)
            if fp:
                fingerprints.add(fp)
            assert_evidence_objects(conn, trace_id, ctx)
        for fp in fingerprints:
            assert_identity_cache(conn, fp)

    assert_no_errors()
