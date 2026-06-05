import base64
from pathlib import Path

import pytest

from evidence import FilesystemEvidenceStore
from media_extraction import MediaExtractionContext, MediaAsset


def _data_url(media_type: str, payload: bytes) -> str:
    encoded = base64.b64encode(payload).decode("ascii")
    return f"data:{media_type};base64,{encoded}"


def _ref(path: str) -> str:
    return f"file:///{path}"


def test_extract_data_url_writes_binary_asset(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "file:///raw/2026/05/05/trace_1", "trace_1")
    png_data = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
    data_url = _data_url("image/png", png_data)

    asset = ctx.extract_data_url(data_url, "image")

    assert asset is not None
    assert asset.object_type == "media_asset_000001"
    assert asset.media_type == "image/png"
    assert asset.size_bytes == len(png_data)
    written = store.read_bytes("file:///raw/2026/05/05/trace_1/media_asset_000001.bin")
    assert written == png_data


def test_extract_data_url_returns_replacement_mapping(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "file:///raw/2026/05/05/trace_1", "trace_1")
    png_data = b"small image data"
    data_url = _data_url("image/png", png_data)

    asset = ctx.extract_data_url(data_url, "image")

    assert asset is not None
    assert len(ctx.replacements) == 1
    assert ctx.replacements[0] == (data_url, f"audit-media:{asset.object_type}")


def test_extract_data_url_sequential_numbering(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "file:///raw/2026/05/05/trace_1", "trace_1")

    asset1 = ctx.extract_data_url(_data_url("image/png", b"img1"), "image")
    asset2 = ctx.extract_data_url("data:audio/wav;base64," + base64.b64encode(b"aud1").decode(), "audio")

    assert asset1.object_type == "media_asset_000001"
    assert asset2.object_type == "media_asset_000002"
    assert len(ctx.replacements) == 2


def test_extract_data_url_skips_oversized(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "file:///raw/2026/05/05/trace_1", "trace_1", max_bytes=10)
    big_payload = b"x" * 100
    data_url = _data_url("image/png", big_payload)

    asset = ctx.extract_data_url(data_url, "image")

    assert asset is None
    assert ctx.replacements == []


def test_extract_data_url_skips_invalid_base64(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "file:///raw/2026/05/05/trace_1", "trace_1")

    asset = ctx.extract_data_url("data:image/png;base64,!!!invalid!!!", "image")

    assert asset is None
    assert ctx.replacements == []


def test_extract_raw_base64_writes_binary_asset(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "file:///raw/2026/05/05/trace_1", "trace_1")
    raw_b64 = base64.b64encode(b"audio data").decode("ascii")

    asset = ctx.extract_raw_base64(raw_b64, "audio/wav", "audio")

    assert asset is not None
    assert asset.media_type == "audio/wav"
    assert asset.size_bytes == len(b"audio data")
    assert ctx.replacements[0] == (raw_b64, f"audit-media:{asset.object_type}")


def test_write_sanitized_copy_preserves_original_json(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    evidence_dir = "file:///raw/2026/05/05/trace_1"
    ctx = MediaExtractionContext(store, evidence_dir, "trace_1")
    data_url = _data_url("image/png", b"img")
    ctx.extract_data_url(data_url, "image")

    original_json = '{"url":"' + data_url + '"}'
    ref = f"{evidence_dir}/request_body.bin"
    store.write_text(ref, original_json)
    sanitized_ref = ctx.write_sanitized_copy(ref)

    assert store.read_text(ref) == original_json
    modified = store.read_text(sanitized_ref)
    assert sanitized_ref.endswith(".sanitized.bin")
    assert "audit-media:media_asset_000001" in modified
    assert data_url not in modified


def test_write_sanitized_copy_noop_when_empty(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "file:///raw/2026/05/05/trace_1", "trace_1")
    ref = "file:///raw/2026/05/05/trace_1/request_body.bin"
    original = '{"model":"gpt-4.1"}'
    store.write_text(ref, original)

    derived_ref = ctx.write_sanitized_copy(ref)

    assert store.read_text(ref) == original
    assert derived_ref == ref
