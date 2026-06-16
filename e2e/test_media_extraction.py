"""E2E: media base64 extraction via resident worker (Redis Streams).

Sends a chat completion with a base64 PNG; the resident worker extracts the
binary asset, rewrites the evidence JSON with audit-media: references, and
records a media_asset row. Backend-agnostic: reads the evidence file via the
shared /evidence volume regardless of filesystem/oss backend (oss backend
asserts only DB rows, skipping file checks).
"""

from __future__ import annotations

import base64
import os

import psycopg

import helpers
from helpers import (
    OPENAI_MODEL,
    assert_no_errors,
    assert_trace_fields,
    bail,
    gateway_post,
    preflight,
    read_request_raw_ref,
    wait_for_rows,
    wait_for_traces,
)

# Minimal valid 1x1 RGBA PNG (70 bytes)
SMALL_PNG = (
    b"\x89PNG\r\n\x1a\n"
    b"\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01"
    b"\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"
    b"\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f"
    b"\x00\x05\x00\x01\xff\x89\x99=\x1d"
    b"\x00\x00\x00\x00IEND\xaeB`\x82"
)
SMALL_PNG_B64 = base64.b64encode(SMALL_PNG).decode("ascii")
DATA_URL = f"data:image/png;base64,{SMALL_PNG_B64}"


def _sanitized_ref(rel_ref: str) -> str:
    """Map an evidence object_ref to its sanitized-rewrite counterpart.

    The worker's MediaExtractionContext.write_sanitized_copy does NOT overwrite
    the original request body; it writes a derived copy with the base64 payload
    replaced by an `audit-media:<asset>` reference. The derived ref is formed as
    `<name>.sanitized.<ext>` (see media_extraction._sanitized_object_ref). The
    original file is intentionally preserved verbatim.
    """
    head, dot, tail = rel_ref.rpartition(".")
    if not dot:
        return f"{rel_ref}.sanitized"
    return f"{head}.sanitized.{tail}"


def _check_evidence_file(request_ref: str) -> None:
    """Filesystem-only: verify the sanitized evidence + extracted binary on /evidence.

    Asserts on the DERIVED sanitized copy (audit-media: ref present, base64 gone)
    and that the original request body is preserved unchanged (base64 still there).
    """
    if helpers.EVIDENCE_STORAGE_BACKEND != "filesystem":
        print("  (skip file checks: backend is not filesystem)")
        return
    if not request_ref:
        helpers.check("evidence.request_raw_ref", False, "empty request_raw_ref")
        return
    rel = request_ref[len("file:///"):] if request_ref.startswith("file:///") else request_ref
    base_path = os.path.join(helpers.EVIDENCE_STORAGE_DIR, rel)
    if not os.path.exists(base_path):
        helpers.check("evidence.file_exists", False, f"not found: {base_path}")
        return

    # original request body is preserved verbatim (still contains the base64)
    original = open(base_path, "r", encoding="utf-8").read()
    helpers.check("evidence.original_preserved",
                  SMALL_PNG_B64 in original, "original request_body missing base64")

    # derived sanitized copy has the audit-media ref and no base64
    sanitized_path = os.path.join(helpers.EVIDENCE_STORAGE_DIR, _sanitized_ref(rel))
    if not os.path.exists(sanitized_path):
        helpers.check("evidence.sanitized_file_exists", False, f"not found: {sanitized_path}")
    else:
        body = open(sanitized_path, "r", encoding="utf-8").read()
        helpers.check("evidence.contains_audit_media_ref",
                      "audit-media:media_asset_000001" in body, "audit-media ref missing")
        helpers.check("evidence.base64_removed",
                      SMALL_PNG_B64 not in body, "base64 still present in sanitized copy")

    asset_path = os.path.join(os.path.dirname(base_path), "media_asset_000001.bin")
    helpers.check("evidence.asset_file_exists", os.path.exists(asset_path), asset_path)
    if os.path.exists(asset_path):
        helpers.check("evidence.asset_content_matches",
                      open(asset_path, "rb").read() == SMALL_PNG, "binary mismatch")


def test_media_extraction():
    preflight(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=OPENAI_MODEL,
    )

    resp, err = gateway_post(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL,
         "messages": [{"role": "user", "content": [
             {"type": "text", "text": "describe this image"},
             {"type": "image_url", "image_url": {"url": DATA_URL}},
         ]}],
         "max_tokens": 10},
        "media-extraction:request",
    )
    if err or resp is None:
        bail("gateway request failed")
    trace_id = resp.headers.get("x-audit-trace-id", "")
    if not trace_id:
        bail("No trace_id returned from gateway request")
    print(f"  Request: trace_id={trace_id}")

    wait_for_traces([trace_id])

    with psycopg.connect(helpers.PG_DSN) as conn:
        assert_trace_fields(conn, trace_id, "media-capture", "openai_chat")

    # wait for resident worker to extract media asset
    n = wait_for_rows(
        "SELECT count(*) FROM raw_evidence_objects "
        "WHERE trace_id = %s AND object_type LIKE 'media_asset_%%'",
        (trace_id,),
        expected=1,
        timeout=30,
        label="media_assets",
    )
    helpers.check("media_assets.exists", n > 0, f"no media_asset for {trace_id}")

    with psycopg.connect(helpers.PG_DSN) as conn:
        rows = conn.execute(
            "SELECT object_type, object_ref, content_type, size_bytes "
            "FROM raw_evidence_objects WHERE trace_id = %s AND object_type LIKE 'media_asset_%%'",
            (trace_id,),
        ).fetchall()
        for asset_type, asset_ref, content_type, size_bytes in rows:
            helpers.eq("media_assets", "object_type", asset_type, "media_asset_000001")
            helpers.not_empty("media_assets", "object_ref", asset_ref)
            helpers.eq("media_assets", "content_type", content_type, "image/png")
            helpers.gt("media_assets", "size_bytes", size_bytes, 0)
            print(f"    media_asset: type={asset_type} size={size_bytes}")

        # sha256 updated
        row = conn.execute("SELECT request_body_sha256 FROM traces WHERE trace_id = %s", (trace_id,)).fetchone()
        helpers.check("sha256.trace_exists", row is not None, "no trace row")
        if row:
            helpers.not_empty("sha256", "request_body_sha256", row[0])

    # evidence file rewritten (filesystem only)
    _check_evidence_file(read_request_raw_ref(trace_id))

    assert_no_errors()
