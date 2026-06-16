"""Smoke test: one request per protocol endpoint, verify trace captured.

Covers OpenAI Chat, OpenAI Responses, Claude Messages.
Backend-agnostic: evidence object_ref prefix is not asserted here.
"""

from __future__ import annotations

import psycopg

import helpers
from helpers import (
    CLAUDE_MODEL,
    OPENAI_MODEL,
    assert_evidence_objects,
    assert_no_errors,
    assert_trace_fields,
    bail,
    gateway_post,
    preflight,
    wait_for_traces,
)

CASES = [
    ("/v1/chat/completions",
     {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 5},
     "openai_chat"),
    ("/v1/responses",
     {"model": OPENAI_MODEL, "input": "ping", "max_output_tokens": 5},
     "openai_responses"),
    ("/v1/messages",
     {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 5},
     "claude_messages"),
]


def test_smoke_three_protocols():
    preflight(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=OPENAI_MODEL,
    )

    results = []
    for endpoint, body, proto in CASES:
        resp, err = gateway_post(endpoint, body, proto)
        if err or resp is None:
            continue
        trace_id = resp.headers.get("x-audit-trace-id", "")
        print(f"  {proto}: status={resp.status_code} trace_id={trace_id}")
        results.append((proto, trace_id))

    trace_ids = [tid for _, tid in results if tid]
    if not trace_ids:
        bail("No trace IDs received from any endpoint")

    wait_for_traces(trace_ids)

    with psycopg.connect(helpers.PG_DSN) as conn:
        for proto, trace_id in results:
            if not trace_id:
                continue
            assert_trace_fields(conn, trace_id, proto, proto, require_usage=False)
            assert_evidence_objects(conn, trace_id, proto)

    assert_no_errors()
