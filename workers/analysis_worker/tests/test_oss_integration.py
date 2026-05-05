import os

import pytest

from oss_evidence import OSSEvidenceStore


@pytest.fixture
def oss_store():
    endpoint = os.environ.get("OSS_ENDPOINT", "")
    bucket = os.environ.get("OSS_BUCKET", "")
    access_key_id = os.environ.get("OSS_ACCESS_KEY_ID", "")
    access_key_secret = os.environ.get("OSS_ACCESS_KEY_SECRET", "")
    if not all([endpoint, bucket, access_key_id, access_key_secret]):
        pytest.skip("OSS environment variables not set")
    return OSSEvidenceStore.from_env(endpoint, bucket, access_key_id, access_key_secret)


@pytest.mark.integration
def test_oss_round_trip_text(oss_store):
    ref = f"oss://{oss_store._bucket_name}/raw/test/integration/text.txt"
    content = '{"integration":"test"}'

    result = oss_store.write_text(ref, content)
    assert result == ref

    read = oss_store.read_text(ref)
    assert read == content


@pytest.mark.integration
def test_oss_round_trip_bytes(oss_store):
    ref = f"oss://{oss_store._bucket_name}/raw/test/integration/binary.bin"
    data = b"\x89PNG\r\n\x1a\n"

    result = oss_store.write_bytes(ref, data)
    assert result == ref

    read = oss_store.read_bytes(ref)
    assert read == data
