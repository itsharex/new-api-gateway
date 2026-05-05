from unittest.mock import MagicMock

import pytest

from oss_evidence import OSSEvidenceStore


class FakeBucket:
    def __init__(self):
        self.objects: dict[str, bytes] = {}

    def get_object(self, key: str) -> MagicMock:
        if key not in self.objects:
            raise FileNotFoundError(f"object not found: {key}")
        resp = MagicMock()
        resp.read.return_value = self.objects[key]
        return resp

    def put_object(self, key: str, data: bytes) -> None:
        self.objects[key] = data


def _oss_ref(bucket: str, key: str) -> str:
    return f"oss://{bucket}/{key}"


def test_oss_store_read_text(tmp_path):
    bucket = FakeBucket()
    bucket.objects["raw/2026/05/05/trace_1/request_body.bin"] = b'{"model":"gpt-4.1"}'
    store = OSSEvidenceStore("test-bucket", bucket)

    result = store.read_text(_oss_ref("test-bucket", "raw/2026/05/05/trace_1/request_body.bin"))
    assert result == '{"model":"gpt-4.1"}'


def test_oss_store_read_bytes(tmp_path):
    bucket = FakeBucket()
    binary = b"\x89PNG\r\n\x1a\n"
    bucket.objects["raw/2026/05/05/trace_1/media.bin"] = binary
    store = OSSEvidenceStore("test-bucket", bucket)

    result = store.read_bytes(_oss_ref("test-bucket", "raw/2026/05/05/trace_1/media.bin"))
    assert result == binary


def test_oss_store_write_text(tmp_path):
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)
    ref = _oss_ref("test-bucket", "raw/2026/05/05/trace_1/request_body.bin")

    result = store.write_text(ref, '{"model":"gpt-4.1"}')

    assert result == ref
    assert bucket.objects["raw/2026/05/05/trace_1/request_body.bin"] == b'{"model":"gpt-4.1"}'


def test_oss_store_write_bytes(tmp_path):
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)
    binary = b"\x89PNG\r\n\x1a\n"
    ref = _oss_ref("test-bucket", "raw/2026/05/05/trace_1/media.bin")

    result = store.write_bytes(ref, binary)

    assert result == ref
    assert bucket.objects["raw/2026/05/05/trace_1/media.bin"] == binary


def test_oss_store_rejects_wrong_scheme(tmp_path):
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)

    with pytest.raises(ValueError, match="must start with oss://"):
        store.read_text("file:///raw/key.bin")


def test_oss_store_rejects_wrong_bucket(tmp_path):
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)

    with pytest.raises(ValueError, match="must start with oss://test-bucket/"):
        store.read_text("oss://other-bucket/raw/key.bin")


def test_oss_store_rejects_empty_key(tmp_path):
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)

    with pytest.raises(ValueError, match="empty key"):
        store.read_text("oss://test-bucket/")


def test_oss_store_is_evidence_store():
    from evidence import EvidenceStore
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)
    assert isinstance(store, EvidenceStore)
