import oss2


class _BucketWrapper:
    """Wraps an oss2.Bucket for testability."""

    def __init__(self, bucket: oss2.Bucket):
        self._bucket = bucket

    def get_object(self, key: str):
        return self._bucket.get_object(key)

    def put_object(self, key: str, data: bytes):
        self._bucket.put_object(key, data)


class OSSEvidenceStore:
    def __init__(self, bucket_name: str, bucket_client):
        self._bucket_name = bucket_name
        self._client = bucket_client

    @classmethod
    def from_env(cls, endpoint: str, bucket_name: str, access_key_id: str, access_key_secret: str) -> "OSSEvidenceStore":
        auth = oss2.Auth(access_key_id, access_key_secret)
        bucket = oss2.Bucket(auth, endpoint, bucket_name)
        return cls(bucket_name, _BucketWrapper(bucket))

    def read_text(self, object_ref: str) -> str:
        key = self._parse_ref(object_ref)
        result = self._client.get_object(key)
        return result.read().decode("utf-8")

    def read_bytes(self, object_ref: str) -> bytes:
        key = self._parse_ref(object_ref)
        result = self._client.get_object(key)
        return result.read()

    def write_text(self, object_ref: str, data: str) -> str:
        key = self._parse_ref(object_ref)
        self._client.put_object(key, data.encode("utf-8"))
        return object_ref

    def write_bytes(self, object_ref: str, data: bytes) -> str:
        key = self._parse_ref(object_ref)
        self._client.put_object(key, data)
        return object_ref

    def _parse_ref(self, object_ref: str) -> str:
        prefix = f"oss://{self._bucket_name}/"
        if not object_ref.startswith(prefix):
            raise ValueError(f"invalid object ref {object_ref!r}: must start with {prefix}")
        key = object_ref[len(prefix):]
        if not key:
            raise ValueError(f"invalid object ref {object_ref!r}: empty key")
        return key
