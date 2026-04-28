from pathlib import Path

import pytest

from evidence import FileEvidenceStore


def test_file_evidence_store_reads_ref_under_root(tmp_path: Path):
    evidence_path = tmp_path / "raw" / "2026" / "04" / "28" / "trace_1"
    evidence_path.mkdir(parents=True)
    (evidence_path / "request_body.bin").write_text('{"model":"gpt-4.1"}', encoding="utf-8")

    store = FileEvidenceStore(tmp_path)

    assert store.read_text("raw/2026/04/28/trace_1/request_body.bin") == '{"model":"gpt-4.1"}'


def test_file_evidence_store_rejects_path_escape(tmp_path: Path):
    store = FileEvidenceStore(tmp_path)

    with pytest.raises(ValueError, match="invalid object ref"):
        store.read_text("../secrets.env")
