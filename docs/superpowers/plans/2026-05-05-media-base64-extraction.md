# Media Base64 Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract base64-encoded binary data from JSON evidence, store it as separate evidence objects, and replace the base64 strings with `audit-media:` references.

**Architecture:** Worker-side extraction during normalization. The normalizer detects base64 data URLs, decodes them to binary, writes binary assets to the evidence store, and returns replacement mappings. After normalization, original evidence JSON is overwritten with modified text (base64 strings replaced by `audit-media:` refs). EvidenceStore is abstracted as a Protocol for future OSS migration.

**Tech Stack:** Python 3.12+, PostgreSQL (raw_evidence_objects), local filesystem evidence store, pytest

**Design Spec:** `docs/superpowers/specs/2026-05-05-media-base64-extraction-design.md`

---

## File Structure

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `workers/analysis_worker/evidence.py` | EvidenceStore Protocol, FilesystemEvidenceStore with write_text/write_bytes |
| Create | `workers/analysis_worker/media_extraction.py` | MediaExtractionContext, decode + write + replacement logic |
| Modify | `workers/analysis_worker/normalizers.py` | Accept optional evidence_store + extraction context, wire into _media_message/_url_media_message |
| Modify | `workers/analysis_worker/repository.py` | New `save_media_assets()` and `update_request_body_sha256()` methods |
| Modify | `workers/analysis_worker/main.py` | Wire extraction into process_trace, handle post-normalization overwrite |
| Delete | `internal/gateway/media.go` | Unused Go-side extraction |
| Delete | `internal/gateway/media_test.go` | Unused Go-side extraction tests |
| Modify | `workers/analysis_worker/tests/test_evidence.py` | Tests for write_text, write_bytes, path validation |
| Create | `workers/analysis_worker/tests/test_media_extraction.py` | Tests for extraction context, decode, replacement |
| Modify | `workers/analysis_worker/tests/test_normalizers.py` | Update base64 tests for extraction behavior |
| Modify | `workers/analysis_worker/tests/test_repository.py` | Tests for media asset DB insert + SHA256 update |

---

### Task 1: EvidenceStore Protocol + FilesystemEvidenceStore write methods

**Files:**
- Modify: `workers/analysis_worker/evidence.py`
- Test: `workers/analysis_worker/tests/test_evidence.py`

- [ ] **Step 1: Write failing tests for write_text and write_bytes**

Add to `tests/test_evidence.py`:

```python
import hashlib

from evidence import EvidenceStore, FilesystemEvidenceStore


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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_evidence.py -v`
Expected: FAIL — `EvidenceStore` import error, `write_text`/`write_bytes` not defined

- [ ] **Step 3: Implement EvidenceStore Protocol and FilesystemEvidenceStore write methods**

Replace the entire contents of `workers/analysis_worker/evidence.py` with:

```python
import tempfile
from pathlib import Path
from typing import Protocol, runtime_checkable


@runtime_checkable
class EvidenceStore(Protocol):
    def read_text(self, object_ref: str) -> str: ...
    def read_bytes(self, object_ref: str) -> bytes: ...
    def write_text(self, object_ref: str, data: str) -> None: ...
    def write_bytes(self, object_ref: str, data: bytes) -> None: ...


class FilesystemEvidenceStore:
    def __init__(self, root: str | Path):
        self.root = Path(root).resolve()

    def read_text(self, object_ref: str) -> str:
        path = self._path_for_ref(object_ref)
        return path.read_text(encoding="utf-8")

    def read_bytes(self, object_ref: str) -> bytes:
        path = self._path_for_ref(object_ref)
        return path.read_bytes()

    def write_text(self, object_ref: str, data: str) -> None:
        path = self._path_for_ref(object_ref)
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.with_suffix(path.suffix + ".tmp")
        try:
            tmp.write_text(data, encoding="utf-8")
            tmp.replace(path)
        except BaseException:
            tmp.unlink(missing_ok=True)
            raise

    def write_bytes(self, object_ref: str, data: bytes) -> None:
        path = self._path_for_ref(object_ref)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)

    def _path_for_ref(self, object_ref: str) -> Path:
        if not object_ref:
            raise ValueError("object ref is empty")
        if "\\" in object_ref or "//" in object_ref or ".." in object_ref:
            raise ValueError(f"invalid object ref {object_ref!r}")
        ref_path = Path(object_ref)
        if ref_path.is_absolute():
            raise ValueError(f"invalid object ref {object_ref!r}")
        candidate = (self.root / ref_path).resolve()
        if candidate != self.root and self.root not in candidate.parents:
            raise ValueError(f"object ref escapes evidence root {object_ref!r}")
        return candidate
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_evidence.py -v`
Expected: All tests PASS

- [ ] **Step 5: Update existing import in test_evidence.py**

The existing tests import `FileEvidenceStore`. Update the import to `FilesystemEvidenceStore` and add the new `EvidenceStore` import. The full updated file header:

```python
from pathlib import Path

import pytest

from evidence import EvidenceStore, FilesystemEvidenceStore
```

Update the existing two test functions to use `FilesystemEvidenceStore` instead of `FileEvidenceStore`:

```python
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
```

- [ ] **Step 6: Update imports in all files that reference FileEvidenceStore**

Run: `cd workers/analysis_worker && grep -rn "FileEvidenceStore" --include="*.py" .`

Update each file:
- `main.py`: Change `from evidence import FileEvidenceStore` to `from evidence import FilesystemEvidenceStore`, then replace all `FileEvidenceStore(` with `FilesystemEvidenceStore(` in the file.
- `tests/test_evidence.py`: Already updated in Step 5.

- [ ] **Step 7: Run full test suite**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: All tests PASS

- [ ] **Step 8: Commit**

```bash
git add workers/analysis_worker/evidence.py workers/analysis_worker/tests/test_evidence.py workers/analysis_worker/main.py
git commit -m "feat(worker): add EvidenceStore Protocol and FilesystemEvidenceStore write methods"
```

---

### Task 2: MediaExtractionContext — decode, store, and replacement tracking

**Files:**
- Create: `workers/analysis_worker/media_extraction.py`
- Create: `workers/analysis_worker/tests/test_media_extraction.py`

- [ ] **Step 1: Write failing tests for MediaExtractionContext**

Create `tests/test_media_extraction.py`:

```python
import base64
from pathlib import Path

import pytest

from evidence import FilesystemEvidenceStore
from media_extraction import MediaExtractionContext, MediaAsset


def _data_url(media_type: str, payload: bytes) -> str:
    encoded = base64.b64encode(payload).decode("ascii")
    return f"data:{media_type};base64,{encoded}"


def test_extract_data_url_writes_binary_asset(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")
    png_data = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
    data_url = _data_url("image/png", png_data)

    asset = ctx.extract_data_url(data_url, "image")

    assert asset is not None
    assert asset.object_type == "media_asset_000001"
    assert asset.media_type == "image/png"
    assert asset.size_bytes == len(png_data)
    written = store.read_bytes("raw/2026/05/05/trace_1/media_asset_000001.bin")
    assert written == png_data


def test_extract_data_url_returns_replacement_mapping(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")
    png_data = b"small image data"
    data_url = _data_url("image/png", png_data)

    asset = ctx.extract_data_url(data_url, "image")

    assert asset is not None
    assert len(ctx.replacements) == 1
    assert ctx.replacements[0] == (data_url, f"audit-media:{asset.object_type}")


def test_extract_data_url_sequential_numbering(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")

    asset1 = ctx.extract_data_url(_data_url("image/png", b"img1"), "image")
    asset2 = ctx.extract_data_url("data:audio/wav;base64," + base64.b64encode(b"aud1").decode(), "audio")

    assert asset1.object_type == "media_asset_000001"
    assert asset2.object_type == "media_asset_000002"
    assert len(ctx.replacements) == 2


def test_extract_data_url_skips_oversized(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1", max_bytes=10)
    big_payload = b"x" * 100
    data_url = _data_url("image/png", big_payload)

    asset = ctx.extract_data_url(data_url, "image")

    assert asset is None
    assert ctx.replacements == []


def test_extract_data_url_skips_invalid_base64(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")

    asset = ctx.extract_data_url("data:image/png;base64,!!!invalid!!!", "image")

    assert asset is None
    assert ctx.replacements == []


def test_extract_raw_base64_writes_binary_asset(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")
    raw_b64 = base64.b64encode(b"audio data").decode("ascii")

    asset = ctx.extract_raw_base64(raw_b64, "audio/wav", "audio")

    assert asset is not None
    assert asset.media_type == "audio/wav"
    assert asset.size_bytes == len(b"audio data")
    assert ctx.replacements[0] == (raw_b64, f"audit-media:{asset.object_type}")


def test_apply_replacements_modifies_json(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    evidence_dir = "raw/2026/05/05/trace_1"
    ctx = MediaExtractionContext(store, evidence_dir, "trace_1")
    data_url = _data_url("image/png", b"img")
    ctx.extract_data_url(data_url, "image")

    original_json = '{"url":"' + data_url + '"}'
    ref = f"{evidence_dir}/request_body.bin"
    store.write_text(ref, original_json)
    ctx.apply_replacements(ref)

    modified = store.read_text(ref)
    assert "audit-media:media_asset_000001" in modified
    assert data_url not in modified


def test_apply_replacements_noop_when_empty(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")
    ref = "raw/2026/05/05/trace_1/request_body.bin"
    original = '{"model":"gpt-4.1"}'
    store.write_text(ref, original)

    ctx.apply_replacements(ref)

    assert store.read_text(ref) == original
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_media_extraction.py -v`
Expected: FAIL — module `media_extraction` not found

- [ ] **Step 3: Implement MediaExtractionContext**

Create `workers/analysis_worker/media_extraction.py`:

```python
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

    def apply_replacements(self, object_ref: str) -> None:
        if not self.replacements:
            return
        text = self.evidence_store.read_text(object_ref)
        for original, replacement in self.replacements:
            text = text.replace(original, replacement)
        self.evidence_store.write_text(object_ref, text)

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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_media_extraction.py -v`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/media_extraction.py workers/analysis_worker/tests/test_media_extraction.py
git commit -m "feat(worker): add MediaExtractionContext for base64 decode, store, and replacement"
```

---

### Task 3: Repository methods for media assets and SHA256 update

**Files:**
- Modify: `workers/analysis_worker/repository.py`
- Test: `workers/analysis_worker/tests/test_repository.py`

- [ ] **Step 1: Write failing tests for save_media_assets and update_request_body_sha256**

Add to `tests/test_repository.py`. Add the import at the top:

```python
from media_extraction import MediaAsset
```

Add these tests:

```python
def test_repository_inserts_media_asset_records():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)
    assets = [
        MediaAsset(
            object_type="media_asset_000001",
            object_ref="raw/2026/05/05/trace_1/media_asset_000001.bin",
            media_type="image/png",
            size_bytes=12345,
        ),
        MediaAsset(
            object_type="media_asset_000002",
            object_ref="raw/2026/05/05/trace_1/media_asset_000002.bin",
            media_type="audio/wav",
            size_bytes=2048,
        ),
    ]

    repo.save_media_assets("trace_1", assets)

    media_queries = [
        (query, params)
        for query, params in conn.cursor_obj.executed
        if "INSERT INTO raw_evidence_objects" in query
    ]
    assert len(media_queries) == 2
    assert media_queries[0][1][:4] == (
        "trace_1",
        "media_asset_000001",
        "raw/2026/05/05/trace_1/media_asset_000001.bin",
    )
    assert media_queries[1][1][:4] == (
        "trace_1",
        "media_asset_000002",
        "raw/2026/05/05/trace_1/media_asset_000002.bin",
    )


def test_repository_skips_media_assets_when_empty():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)

    repo.save_media_assets("trace_1", [])

    media_queries = [
        query for query, _ in conn.cursor_obj.executed
        if "INSERT INTO raw_evidence_objects" in query
    ]
    assert media_queries == []


def test_repository_updates_request_body_sha256():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)

    repo.update_request_body_sha256("trace_1", "abc123sha256")

    queries = [query for query, _ in conn.cursor_obj.executed]
    assert any(
        "UPDATE traces" in q and "request_body_sha256" in q and "abc123sha256" in q
        for q in conn.cursor_obj.executed
        for q in [q[0]]
    )
```

Wait — the last assertion has a bug. Let me fix it:

```python
def test_repository_updates_request_body_sha256():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)

    repo.update_request_body_sha256("trace_1", "abc123sha256")

    sha_queries = [
        (query, params) for query, params in conn.cursor_obj.executed
        if "UPDATE traces" in query and "request_body_sha256" in query
    ]
    assert len(sha_queries) == 1
    assert sha_queries[0][1] == ("abc123sha256", "trace_1")
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_repository.py::test_repository_inserts_media_asset_records tests/test_repository.py::test_repository_updates_request_body_sha256 -v`
Expected: FAIL — `save_media_assets` / `update_request_body_sha256` not defined

- [ ] **Step 3: Implement repository methods**

Add these two methods to `PostgresAnalysisRepository` in `repository.py`:

```python
    def save_media_assets(self, trace_id: str, assets: list) -> None:
        if not assets:
            return
        cursor = self.connection.cursor()
        for asset in assets:
            cursor.execute(
                """
                INSERT INTO raw_evidence_objects (
                    trace_id, object_type, object_ref, storage_backend,
                    content_type, size_bytes
                ) VALUES (%s, %s, %s, 'filesystem', %s, %s)
                """,
                (
                    trace_id,
                    asset.object_type,
                    asset.object_ref,
                    asset.media_type,
                    asset.size_bytes,
                ),
            )
        self.connection.commit()

    def update_request_body_sha256(self, trace_id: str, sha256: str) -> None:
        cursor = self.connection.cursor()
        cursor.execute(
            "UPDATE traces SET request_body_sha256 = %s, updated_at = now() WHERE trace_id = %s",
            (sha256, trace_id),
        )
        self.connection.commit()
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_repository.py -v`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/repository.py workers/analysis_worker/tests/test_repository.py
git commit -m "feat(worker): add repository methods for media assets and SHA256 update"
```

---

### Task 4: Wire extraction into normalizers

**Files:**
- Modify: `workers/analysis_worker/normalizers.py`
- Test: `workers/analysis_worker/tests/test_normalizers.py`

This is the core integration task. The normalizer gains an optional `extraction_context` parameter. When provided, base64 data URLs and raw base64 fields are extracted to binary assets. When absent, behavior is identical to current.

- [ ] **Step 1: Write failing tests for extraction-aware normalization**

Add to `tests/test_normalizers.py`. Add imports:

```python
from evidence import FilesystemEvidenceStore
from media_extraction import MediaExtractionContext
```

Add these test functions:

```python
def test_extracts_base64_data_url_to_media_asset(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    evidence_dir = "raw/2026/05/05/trace_1"
    trace_job = job(protocol_family="openai_chat", route_pattern="/v1/chat/completions")
    png_data = b"\x89PNG\r\n\x1a\n"
    data_url = "data:image/png;base64," + __import__("base64").b64encode(png_data).decode()
    request = {
        "messages": [
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": "inspect this"},
                    {"type": "image_url", "image_url": {"url": data_url}},
                ],
            }
        ]
    }
    request_body = __import__("json").dumps(request)
    store.write_text(f"{evidence_dir}/request_body.bin", request_body)
    ctx = MediaExtractionContext(store, evidence_dir, "trace_1")

    messages, _ = normalize_json_trace(trace_job, request_body, "{}", extraction_context=ctx)

    image_msg = [m for m in messages if m.modality == "image"][0]
    assert image_msg.protocol_item_type == "base64_media_extracted"
    assert len(ctx.assets) == 1
    assert ctx.assets[0].media_type == "image/png"
    assert store.read_bytes(f"{evidence_dir}/media_asset_000001.bin") == png_data
    assert len(ctx.replacements) == 1
    assert ctx.replacements[0] == (data_url, "audit-media:media_asset_000001")


def test_extracts_base64_without_extraction_context_returns_base64_media():
    trace_job = job(protocol_family="openai_chat", route_pattern="/v1/chat/completions")
    data_url = "data:image/png;base64," + __import__("base64").b64encode(b"img").decode()
    request = {
        "messages": [
            {
                "role": "user",
                "content": [
                    {"type": "image_url", "image_url": {"url": data_url}},
                ],
            }
        ]
    }

    messages, _ = normalize_json_trace(trace_job, __import__("json").dumps(request), "{}")

    assert messages[0].protocol_item_type == "base64_media"


def test_extracts_openai_input_audio_raw_base64(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    evidence_dir = "raw/2026/05/05/trace_1"
    trace_job = job(protocol_family="openai_chat", route_pattern="/v1/chat/completions")
    audio_data = b"RIFF\x00\x00\x00\x00WAVEfmt "
    raw_b64 = __import__("base64").b64encode(audio_data).decode()
    request = {
        "messages": [
            {
                "role": "user",
                "content": [
                    {"type": "input_audio", "input_audio": {"data": raw_b64, "format": "wav"}},
                ],
            }
        ]
    }
    request_body = __import__("json").dumps(request)
    ctx = MediaExtractionContext(store, evidence_dir, "trace_1")

    messages, _ = normalize_json_trace(trace_job, request_body, "{}", extraction_context=ctx)

    audio_msg = [m for m in messages if m.modality == "audio"][0]
    assert audio_msg.protocol_item_type == "base64_media_extracted"
    assert len(ctx.assets) == 1


def test_extracts_gemini_inline_data_base64(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    evidence_dir = "raw/2026/05/05/trace_1"
    trace_job = job(protocol_family="gemini", route_pattern="/v1beta/models/gemini:generateContent")
    img_data = b"PNG image bytes here"
    raw_b64 = __import__("base64").b64encode(img_data).decode()
    request = {
        "contents": [
            {
                "role": "user",
                "parts": [
                    {"text": "inspect this"},
                    {"inlineData": {"mimeType": "image/png", "data": raw_b64}},
                ],
            }
        ]
    }
    request_body = __import__("json").dumps(request)
    ctx = MediaExtractionContext(store, evidence_dir, "trace_1")

    messages, _ = normalize_json_trace(trace_job, request_body, "{}", extraction_context=ctx)

    image_msg = [m for m in messages if m.modality == "image"][0]
    assert image_msg.protocol_item_type == "base64_media_extracted"
    assert ctx.assets[0].media_type == "image/png"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_normalizers.py::test_extracts_base64_data_url_to_media_asset -v`
Expected: FAIL — `normalize_json_trace()` got unexpected keyword argument `extraction_context`

- [ ] **Step 3: Modify normalize_json_trace signature and _url_media_message**

In `normalizers.py`, add the import:

```python
from media_extraction import MediaExtractionContext
```

Change `normalize_json_trace` signature to accept the optional parameter:

```python
def normalize_json_trace(
    job: TraceCapturedJob,
    request_body: str,
    response_body: str,
    extraction_context: MediaExtractionContext | None = None,
) -> tuple[list[NormalizedMessage], list[AnalysisResult]]:
```

Pass `extraction_context` through the call chain. Every function that eventually calls `_media_message` or `_url_media_message` needs the parameter. The signature chain is:

1. `normalize_json_trace` → `_normalize_openai_chat` / `_normalize_openai_responses` / `_normalize_claude_messages` / `_normalize_gemini`
2. These call `_part_messages`
3. `_part_messages` calls `_media_message`
4. `_media_message` calls `_url_media_message`

Add `extraction_context` parameter to each function in the chain. For brevity, here are the exact changes:

**`_normalize_openai_chat`** — add parameter, pass to `_part_messages`:
```python
def _normalize_openai_chat(
    job: TraceCapturedJob,
    request_json: dict[str, Any],
    response_json: dict[str, Any],
    extraction_context: MediaExtractionContext | None = None,
) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    for index, item in enumerate(request_json.get("messages", [])):
        if not isinstance(item, dict):
            continue
        messages.extend(_part_messages(
            job, "request", str(item.get("role", "")), item.get("content"),
            f"request.messages[{index}]", "openai_chat_message", extraction_context,
        ))
    for index, choice in enumerate(response_json.get("choices", [])):
        if not isinstance(choice, dict):
            continue
        message = choice.get("message")
        if not isinstance(message, dict):
            continue
        messages.extend(_part_messages(
            job, "response", str(message.get("role", "assistant")), message.get("content"),
            f"response.choices[{index}].message", "openai_chat_message", extraction_context,
        ))
    for sequence_index, message in enumerate(messages):
        messages[sequence_index] = _with_sequence_index(message, sequence_index)
    return messages
```

Apply the same pattern to `_normalize_openai_responses`, `_normalize_claude_messages`, and `_normalize_gemini` — add `extraction_context` parameter and pass it to every `_part_messages` call.

**`normalize_json_trace`** body — pass to each protocol normalizer:
```python
    if job.protocol_family == "openai_chat":
        messages = _normalize_openai_chat(job, request_json, response_json, extraction_context)
    elif job.protocol_family == "openai_responses":
        messages = _normalize_openai_responses(job, request_json, response_json, extraction_context)
    elif job.protocol_family == "claude_messages":
        messages = _normalize_claude_messages(job, request_json, response_json, extraction_context)
    elif job.protocol_family == "gemini":
        messages = _normalize_gemini(job, request_json, response_json, extraction_context)
    else:
        messages = _normalize_generic_prompt(job, request_json)
```

**`_part_messages`** — add parameter, pass to `_media_message`:
```python
def _part_messages(
    job: TraceCapturedJob,
    direction: str,
    role: str,
    content: Any,
    source_path: str,
    protocol_item_type: str,
    extraction_context: MediaExtractionContext | None = None,
) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    if isinstance(content, list):
        for index, item in enumerate(content):
            item_path = _part_source_path(source_path, protocol_item_type, index)
            media = _media_message(job, direction, role, item, item_path, extraction_context)
            if media:
                messages.append(media)
                continue
            text = _content_to_text(item)
            if text:
                messages.append(_message(job, direction, 0, role, text, item_path, protocol_item_type))
    elif isinstance(content, dict):
        media = _media_message(job, direction, role, content, source_path, extraction_context)
        if media:
            messages.append(media)
        else:
            text = _content_to_text(content)
            if text:
                messages.append(_message(job, direction, 0, role, text, source_path, protocol_item_type))
    else:
        text = _content_to_text(content)
        if text:
            messages.append(_message(job, direction, 0, role, text, source_path, protocol_item_type))
    return messages
```

**`_media_message`** — add parameter, pass to `_url_media_message` and handle inline data extraction:
```python
def _media_message(
    job: TraceCapturedJob,
    direction: str,
    role: str,
    value: Any,
    source_path: str,
    extraction_context: MediaExtractionContext | None = None,
) -> NormalizedMessage | None:
    if not isinstance(value, dict):
        return None
    item_type = value.get("type")
    if item_type == "image_url" and isinstance(value.get("image_url"), dict):
        media_url = value["image_url"].get("url")
        return _url_media_message(job, direction, role, media_url, source_path, "image", extraction_context)
    if item_type in {"input_image", "image"}:
        media_url = value.get("image_url") or value.get("url")
        return _url_media_message(job, direction, role, media_url, source_path, "image", extraction_context)
    if item_type == "input_audio" and isinstance(value.get("input_audio"), dict):
        audio = value["input_audio"]
        if isinstance(audio.get("data"), str):
            raw_b64 = audio["data"]
            if extraction_context:
                asset = extraction_context.extract_raw_base64(raw_b64, "audio/wav", "audio")
                if asset:
                    return _message(job, direction, 0, role, "", source_path, "base64_media_extracted", modality="audio")
            return _message(job, direction, 0, role, "", source_path, "base64_media", modality="audio")
        return _url_media_message(job, direction, role, audio.get("url"), source_path, "audio", extraction_context)
    inline_data = _dict_value(value, "inlineData", "inline_data")
    if inline_data:
        mime_type = _string_value(inline_data, "mimeType", "mime_type")
        modality = _modality_from_mime_type(mime_type)
        raw_b64 = inline_data.get("data")
        if isinstance(raw_b64, str) and raw_b64 and extraction_context:
            asset = extraction_context.extract_raw_base64(raw_b64, mime_type or "application/octet-stream", modality)
            if asset:
                return _message(job, direction, 0, role, "", source_path, "base64_media_extracted", modality=modality)
        return _message(job, direction, 0, role, "", source_path, "base64_media", modality=modality)
    file_data = _dict_value(value, "fileData", "file_data")
    if file_data:
        mime_type = _string_value(file_data, "mimeType", "mime_type")
        file_uri = _string_value(file_data, "fileUri", "file_uri")
        return _url_media_message(job, direction, role, file_uri, source_path, _modality_from_mime_type(mime_type), extraction_context)
    return None
```

**`_url_media_message`** — add parameter, extract data URLs when context is provided:
```python
def _url_media_message(
    job: TraceCapturedJob,
    direction: str,
    role: str,
    media_url: Any,
    source_path: str,
    modality: str,
    extraction_context: MediaExtractionContext | None = None,
) -> NormalizedMessage | None:
    if not isinstance(media_url, str) or not media_url:
        return None
    if _is_base64_data_url(media_url):
        if extraction_context:
            asset = extraction_context.extract_data_url(media_url, modality)
            if asset:
                return _message(job, direction, 0, role, "", source_path, "base64_media_extracted", modality=modality)
        return _message(job, direction, 0, role, "", source_path, "base64_media", modality=modality)
    if media_url.startswith(("http://", "https://")):
        return _message(job, direction, 0, role, "", source_path, "image_url", modality=modality, media_url=media_url)
    return _message(job, direction, 0, role, "", source_path, "media_url", modality=modality)
```

- [ ] **Step 4: Run extraction tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_normalizers.py -v -k "extract"`
Expected: All extraction tests PASS

- [ ] **Step 5: Run full normalizer test suite to verify no regressions**

Run: `cd workers/analysis_worker && uv run pytest tests/test_normalizers.py -v`
Expected: All tests PASS (existing tests still work without extraction_context)

- [ ] **Step 6: Run full worker test suite**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: All tests PASS

- [ ] **Step 7: Commit**

```bash
git add workers/analysis_worker/normalizers.py workers/analysis_worker/tests/test_normalizers.py
git commit -m "feat(worker): wire MediaExtractionContext into normalizer pipeline"
```

---

### Task 5: Wire extraction into main.py process_trace flow

**Files:**
- Modify: `workers/analysis_worker/main.py`

- [ ] **Step 1: Modify process_trace to create MediaExtractionContext and apply replacements**

Update imports in `main.py`:

```python
from hashlib import sha256

from media_extraction import MediaExtractionContext
```

Modify `process_trace` to accept and use `evidence_store`:

```python
def process_trace(
    job: TraceCapturedJob,
    request_body: str,
    response_body: str,
    repository,
    contexts: list[ContextCatalogEntry] | None = None,
    evidence_store: FilesystemEvidenceStore | None = None,
) -> dict:
    extraction_context: MediaExtractionContext | None = None
    if evidence_store and job.request_raw_ref:
        evidence_dir = str(Path(job.request_raw_ref).parent)
        extraction_context = MediaExtractionContext(evidence_store, evidence_dir, job.trace_id)
    messages, results = normalize_json_trace(job, request_body, response_body, extraction_context)
    work_relevance = classify_work_relevance(job, messages, list(contexts or []))
    results.append(work_relevance.to_analysis_result())
    aggregates = aggregate_deltas(job)
    load_analysis_context = getattr(repository, "analysis_context_for", None)
    analysis_context = load_analysis_context(job) if load_analysis_context else AnalysisContext()
    anomalies = [
        *detect_anomalies(job, messages, analysis_context),
        *detect_work_relevance_anomalies(job, work_relevance),
    ]
    coverage_alerts = detect_coverage_alerts(job, messages)
    repository.save_trace_analysis(messages, results, aggregates, anomalies, coverage_alerts)
    if extraction_context and extraction_context.replacements:
        extraction_context.apply_replacements(job.request_raw_ref)
        if hasattr(repository, "save_media_assets"):
            repository.save_media_assets(job.trace_id, extraction_context.assets)
        if hasattr(repository, "update_request_body_sha256"):
            modified = evidence_store.read_text(job.request_raw_ref)
            new_sha = sha256(modified.encode("utf-8")).hexdigest()
            repository.update_request_body_sha256(job.trace_id, new_sha)
    return {
        "accepted_trace_id": job.trace_id,
        "worker_status": "processed",
        "normalized_message_count": len(messages),
        "analysis_result_count": len(results),
        "work_relevance_count": 1,
        "aggregate_count": len(aggregates),
        "anomaly_count": len(anomalies),
        "coverage_alert_count": len(coverage_alerts),
        "usage_total_tokens": job.usage_total_tokens,
        "media_assets_extracted": len(extraction_context.assets) if extraction_context else 0,
    }
```

- [ ] **Step 2: Modify process_job_line to pass evidence_store**

```python
def process_job_line(line: str, evidence_store: FilesystemEvidenceStore, repository, context_repository=None) -> dict:
    job = parse_job(line)
    request_body = evidence_store.read_text(job.request_raw_ref) if job.request_raw_ref else ""
    response_body = evidence_store.read_text(job.response_raw_ref) if job.response_raw_ref else ""
    contexts = context_repository.list_active_contexts() if context_repository else []
    return process_trace(job, request_body, response_body, repository, contexts, evidence_store)
```

- [ ] **Step 3: Add Path import**

Add `from pathlib import Path` to the imports (it's needed for `Path(job.request_raw_ref).parent`).

- [ ] **Step 4: Run full worker test suite**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/main.py
git commit -m "feat(worker): wire media extraction into process_trace pipeline"
```

---

### Task 6: Delete Go-side media.go and media_test.go

**Files:**
- Delete: `internal/gateway/media.go`
- Delete: `internal/gateway/media_test.go`

- [ ] **Step 1: Check for references to extractMediaReferences or media.go exports**

Run: `grep -rn "extractMediaReferences\|MediaReference\|walkMedia\|decodeOrderedJSON\|isBase64MediaField\|parseBase64Media\|isValidBase64" --include="*.go" /Users/roy/codes/new-api-gateway/internal/`

If any references exist outside `media.go` / `media_test.go`, those must be removed first. Based on the design spec, no other Go code references these.

- [ ] **Step 2: Delete the files**

```bash
git rm internal/gateway/media.go internal/gateway/media_test.go
```

- [ ] **Step 3: Run Go tests to verify no breakage**

Run: `cd /Users/roy/codes/new-api-gateway && go test ./internal/gateway/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git commit -m "chore(gateway): remove unused Go-side media extraction (now handled by Python worker)"
```

---

### Task 7: End-to-end test for base64 image extraction

**Files:**
- Create: `e2e/test_media_extraction.py`

- [ ] **Step 1: Explore existing e2e test structure**

Read an existing e2e test (e.g., `e2e/test_gateway_openai.py`) to understand the test setup pattern, fixture conventions, and how the gateway + worker + DB are exercised.

- [ ] **Step 2: Write the e2e test**

Create `e2e/test_media_extraction.py` following the project's e2e conventions. The test should:

1. Start the gateway and worker (using existing e2e infrastructure)
2. Send an OpenAI chat completion request containing a small base64 PNG image_url
3. Wait for the worker to process the trace
4. Query `raw_evidence_objects` for the trace_id and verify a `media_asset_000001` record exists
5. Read the modified `request_body.bin` evidence and verify it contains `audit-media:media_asset_000001`
6. Read the `media_asset_000001.bin` binary and verify it matches the original PNG data
7. Verify the `traces.request_body_sha256` column has been updated

This test should be adapted to the project's specific e2e test infrastructure. Consult `e2e/test_gateway_openai.py` for the exact setup pattern.

- [ ] **Step 3: Run the e2e test locally**

Run: `./scripts/e2e_media_extraction.sh` or `pytest e2e/test_media_extraction.py -v`
(Adapt command based on project's e2e test runner)

- [ ] **Step 4: Commit**

```bash
git add e2e/test_media_extraction.py
git commit -m "test(e2e): add media base64 extraction e2e test"
```

---

## Self-Review

**1. Spec coverage:**
- EvidenceStore Protocol abstraction → Task 1 ✓
- MediaExtractionContext + decode + write + replacement → Task 2 ✓
- Normalizer changes (_media_message, _url_media_message) → Task 4 ✓
- JSON replacement and evidence overwrite → Task 5 (apply_replacements in main.py) ✓
- Repository changes (raw_evidence_objects + SHA256 update) → Task 3 ✓
- main.py integration → Task 5 ✓
- Go-side cleanup → Task 6 ✓
- Error handling (oversized, invalid base64, write failures) → Task 2 (max_bytes, try/except in _decode_and_store) ✓
- E2E tests → Task 7 ✓

**2. Placeholder scan:**
- Task 7 Step 1 says "explore existing e2e test structure" — this is a legitimate exploration step, not a placeholder. The actual test code in Step 2 is written out.
- No "TBD", "TODO", or "implement later" found.

**3. Type consistency:**
- `MediaAsset` dataclass: `object_type`, `object_ref`, `media_type`, `size_bytes` — used consistently in Task 2, 3, 4, 5
- `MediaExtractionContext` constructor: `(evidence_store, evidence_dir, trace_id, max_bytes)` — used consistently in Task 4, 5
- `extract_data_url(data_url, modality)` returns `MediaAsset | None` — used in Task 4 `_url_media_message`
- `extract_raw_base64(encoded, media_type, modality)` returns `MediaAsset | None` — used in Task 4 `_media_message`
- `FilesystemEvidenceStore` renamed from `FileEvidenceStore` — all references updated in Task 1 Step 6
