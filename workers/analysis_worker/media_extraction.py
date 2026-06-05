import base64
from dataclasses import dataclass

from evidence import EvidenceStore


@dataclass(frozen=True)
class MediaAsset:
    object_type: str
    object_ref: str
    media_type: str
    size_bytes: int


class MediaExtractionContext:
    def __init__(
        self,
        evidence_store: EvidenceStore,
        evidence_dir: str,
        trace_id: str,
        max_bytes: int = 20 * 1024 * 1024,
    ):
        self.evidence_store = evidence_store
        self.evidence_dir = evidence_dir
        self.trace_id = trace_id
        self.max_bytes = max_bytes
        self.assets: list[MediaAsset] = []
        self.replacements: list[tuple[str, str]] = []
        self._counter = 0

    def _next_object_type(self) -> str:
        self._counter += 1
        return f"media_asset_{self._counter:06d}"

    def extract_data_url(
        self, data_url: str, modality: str
    ) -> MediaAsset | None:
        header, _, encoded = data_url.partition(",")
        if not _ or not encoded:
            return None
        if ";base64" not in header.lower():
            return None
        media_type = _mime_from_data_url_header(header)
        return self._decode_and_store(encoded, media_type, data_url)

    def extract_raw_base64(
        self, encoded: str, media_type: str, modality: str
    ) -> MediaAsset | None:
        return self._decode_and_store(encoded, media_type, encoded)

    def write_sanitized_copy(self, object_ref: str) -> str:
        if not self.replacements:
            return object_ref
        text = self.evidence_store.read_text(object_ref)
        for original, replacement in self.replacements:
            text = text.replace(original, replacement)
        derived_ref = _sanitized_object_ref(object_ref)
        self.evidence_store.write_text(derived_ref, text)
        return derived_ref

    def _decode_and_store(
        self, encoded: str, media_type: str, original_string: str
    ) -> MediaAsset | None:
        try:
            binary = base64.b64decode(encoded, validate=True)
        except Exception:
            return None
        if len(binary) > self.max_bytes:
            return None
        object_type = self._next_object_type()
        object_ref = f"{self.evidence_dir}/{object_type}.bin"
        self.evidence_store.write_bytes(object_ref, binary)
        asset = MediaAsset(
            object_type=object_type,
            object_ref=object_ref,
            media_type=media_type,
            size_bytes=len(binary),
        )
        self.assets.append(asset)
        self.replacements.append((original_string, f"audit-media:{object_type}"))
        return asset


def _mime_from_data_url_header(header: str) -> str:
    mime = header.lower().removeprefix("data:").split(";")[0]
    return mime if mime else "application/octet-stream"


def _sanitized_object_ref(object_ref: str) -> str:
    head, dot, tail = object_ref.rpartition(".")
    if not dot:
        return f"{object_ref}.sanitized"
    return f"{head}.sanitized.{tail}"
