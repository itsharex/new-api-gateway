"""E2E: gateway captures OpenAI-format requests (chat + responses, stream/non-stream)."""

from __future__ import annotations

import psycopg

import helpers
from helpers import (
    OPENAI_MODEL,
    assert_evidence_objects,
    assert_identity_cache,
    assert_no_errors,
    assert_trace_fields,
    gateway_post,
    gateway_stream,
    preflight,
    wait_for_traces,
)

PROTOCOL_FAMILY = {
    "/v1/chat/completions": "openai_chat",
    "/v1/responses": "openai_responses",
}


def _post(endpoint, body, label):
    return gateway_post(endpoint, body, label)


def test_gateway_openai_capture():
    preflight(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=OPENAI_MODEL,
    )

    results = []  # (endpoint, turn, trace_id)

    # chat turn 1
    r1, e1 = _post("/v1/chat/completions",
                   {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "hello"}], "max_tokens": 10},
                   "/v1/chat/completions:turn1")
    if not e1 and r1 is not None:
        reply1 = r1.json()["choices"][0]["message"].get("content", "") or ""
        results.append(("/v1/chat/completions", 1, r1.headers.get("x-audit-trace-id", "")))
        # chat turn 2 (multi-turn)
        r2, e2 = _post("/v1/chat/completions",
                       {"model": OPENAI_MODEL,
                        "messages": [{"role": "user", "content": "hello"},
                                     {"role": "assistant", "content": reply1},
                                     {"role": "user", "content": "what is 1+1?"}],
                        "max_tokens": 10},
                       "/v1/chat/completions:turn2")
        if not e2 and r2 is not None:
            results.append(("/v1/chat/completions", 2, r2.headers.get("x-audit-trace-id", "")))

    # responses turn 1
    r3, e3 = _post("/v1/responses",
                   {"model": OPENAI_MODEL, "input": "hello", "max_output_tokens": 10},
                   "/v1/responses:turn1")
    if not e3 and r3 is not None:
        results.append(("/v1/responses", 1, r3.headers.get("x-audit-trace-id", "")))
        # responses turn 2
        r4, e4 = _post("/v1/responses",
                       {"model": OPENAI_MODEL, "input": "what is 1+1?", "max_output_tokens": 10},
                       "/v1/responses:turn2")
        if not e4 and r4 is not None:
            results.append(("/v1/responses", 2, r4.headers.get("x-audit-trace-id", "")))

    # chat stream
    r5, e5 = gateway_stream("/v1/chat/completions",
                            {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "hello"}],
                             "max_tokens": 10, "stream": True},
                            "/v1/chat/completions:stream")
    if not e5 and r5 is not None:
        results.append(("/v1/chat/completions", 0, r5.headers.get("x-audit-trace-id", "")))

    # responses stream
    r6, e6 = gateway_stream("/v1/responses",
                            {"model": OPENAI_MODEL, "input": "hello", "max_output_tokens": 10, "stream": True},
                            "/v1/responses:stream")
    if not e6 and r6 is not None:
        results.append(("/v1/responses", 0, r6.headers.get("x-audit-trace-id", "")))

    trace_ids = [tid for _, _, tid in results if tid]
    wait_for_traces(trace_ids)

    fingerprints = set()
    with psycopg.connect(helpers.PG_DSN) as conn:
        for endpoint, turn, trace_id in results:
            if not trace_id:
                continue
            ctx = f"{endpoint}:turn{turn}"
            fp = assert_trace_fields(conn, trace_id, ctx, PROTOCOL_FAMILY[endpoint], model=OPENAI_MODEL)
            if fp:
                fingerprints.add(fp)
            assert_evidence_objects(conn, trace_id, ctx)
        for fp in fingerprints:
            assert_identity_cache(conn, fp)

    assert_no_errors()
