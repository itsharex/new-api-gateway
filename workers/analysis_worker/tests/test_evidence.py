from pathlib import Path

import pytest

from evidence import EvidenceStore, FilesystemEvidenceStore


def test_file_evidence_store_reads_ref_under_root(tmp_path: Path):
    evidence_path = tmp_path / "raw" / "2026" / "04" / "28" / "trace_1"
    evidence_path.mkdir(parents=True)
    (evidence_path / "request_body.bin").write_text('{"model":"gpt-4.1"}', encoding="utf-8")

    store = FilesystemEvidenceStore(tmp_path)

    assert store.read_text("raw/2026/04/28/trace_1/request_body.bin") == '{"model":"gpt-4.1"}'


def test_file_evidence_store_rejects_path_escape(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)

    with pytest.raises(ValueError, match="invalid object ref"):
        store.read_text("../secrets.env")


def test_filesystem_evidence_store_write_text_creates_file(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ref = "raw/2026/05/05/trace_1/request_body.bin"
    store.write_text(ref, '{"model":"gpt-4.1"}')

    assert store.read_text(ref) == '{"model":"gpt-4.1"}'


def test_filesystem_evidence_store_write_text_overwrites_existing(tmp_path: Path):
    ref = "raw/2026/05/05/trace_1/request_body.bin"
    store = FilesystemEvidenceStore(tmp_path)
    store.write_text(ref, "original")
    store.write_text(ref, "updated")

    assert store.read_text(ref) == "updated"


def test_filesystem_evidence_store_write_text_rejects_path_escape(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    with pytest.raises(ValueError, match="invalid object ref"):
        store.write_text("../../etc/passwd", "data")


def test_filesystem_evidence_store_write_bytes_creates_file(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    binary = b"\x89PNG\r\n\x1a\n"
    ref = "raw/2026/05/05/trace_1/media_asset_000001.bin"
    store.write_bytes(ref, binary)

    path = tmp_path / "raw" / "2026" / "05" / "05" / "trace_1" / "media_asset_000001.bin"
    assert path.read_bytes() == binary


def test_filesystem_evidence_store_write_bytes_rejects_path_escape(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    with pytest.raises(ValueError, match="invalid object ref"):
        store.write_bytes("../../../etc/shadow", b"secret")


def test_evidence_store_is_protocol():
    assert issubclass(FilesystemEvidenceStore, EvidenceStore)
