"""E2E: gateway capture -> resident worker analysis (Redis Streams).

The gateway publishes a trace_captured job to the analysis.core stream on
every captured request; the resident analysis-worker consumes it and writes
analysis_results. This test does NOT push jobs or run the worker itself — it
verifies the real deployed pipeline end-to-end by polling analysis_results.
"""

from __future__ import annotations

import psycopg

import helpers
from helpers import (
    OPENAI_MODEL,
    assert_no_errors,
    assert_trace_fields,
    bail,
    gateway_post,
    preflight,
    wait_for_rows,
    wait_for_traces,
)


def test_gateway_worker_pipeline():
    preflight(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=OPENAI_MODEL,
    )

    # 1. send request through gateway
    resp, err = gateway_post(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "hello"}], "max_tokens": 10},
        "worker-pipeline:request",
    )
    if err or resp is None:
        bail("gateway request failed")
    trace_id = resp.headers.get("x-audit-trace-id", "")
    if not trace_id:
        bail("No trace_id returned from gateway request")
    print(f"  Request: trace_id={trace_id}")

    # 2. wait for trace row
    wait_for_traces([trace_id])

    # 3. assert capture fields
    with psycopg.connect(helpers.PG_DSN) as conn:
        assert_trace_fields(conn, trace_id, "gateway-capture", "openai_chat")

    # 4. wait for resident worker to produce analysis_results (streams)
    n = wait_for_rows(
        "SELECT count(*) FROM analysis_results WHERE trace_id = %s",
        (trace_id,),
        expected=1,
        timeout=30,
        label="analysis_results",
    )
    helpers.check("analysis_results.exists", n > 0, f"no analysis_results for {trace_id}")

    # 5. inspect analysis_results rows
    with psycopg.connect(helpers.PG_DSN) as conn:
        rows = conn.execute(
            "SELECT analyzer_name, category, label FROM analysis_results WHERE trace_id = %s",
            (trace_id,),
        ).fetchall()
        for analyzer_name, category, label in rows:
            helpers.not_empty("analysis_results", "analyzer_name", analyzer_name)
            helpers.not_empty("analysis_results", "category", category)
            helpers.not_empty("analysis_results", "label", label)
            print(f"    analyzer={analyzer_name} category={category} label={label}")

    assert_no_errors()
