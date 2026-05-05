# Media Base64 Extraction Design

## Background

The gateway captures raw request/response bodies as evidence files. When chat messages contain base64-encoded images or audio (e.g., `data:image/png;base64,...` in `image_url` fields), the entire base64 string is stored verbatim in the JSON evidence. This wastes storage and makes it hard to view the message structure without the noise of megabytes of base64 data.

The Python worker's normalizer already identifies base64 media content and strips it during normalization (recording only a `base64_media` marker), but the raw evidence JSON retains the original base64 data.

## Goal

Extract base64-encoded binary data from JSON evidence, store it as separate evidence objects, and replace the base64 strings in the JSON with logical references. This applies to all protocol families (OpenAI Chat, OpenAI Responses, Claude Messages, Gemini).

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Processing location | Worker-side only | Gateway stays pure transparent proxy; Worker already has JSON parsing logic |
| Reference format | `audit-media:{object_type}` | Storage-backend-agnostic; resolved via `raw_evidence_objects` table |
| Original evidence | Overwrite with modified JSON | Saves storage; binary assets stored separately |
| Implementation approach | Synchronous extraction during normalization | Single pass; natural integration with existing `_media_message()` |
| Large body protection | 20MB decode limit per asset | Skip oversized, mark as `base64_media_oversized`, don't overwrite |
| Go-side `extractMediaReferences()` | Delete | Redundant with Worker-side implementation |

## Architecture

### Data Flow

```
Gateway (unchanged):
  Request → capture body → forward upstream → store raw evidence → publish job

Worker (enhanced):
  1. Read raw evidence JSON
  2. Normalize (existing) + extract base64 (new):
     a. Detect base64 data URL in content parts
     b. Decode to binary bytes
     c. Write binary to evidence store as media_asset_NNN.bin
     d. Insert raw_evidence_objects record
     e. Record replacement mapping (original base64 string → audit-media reference)
  3. After normalization:
     a. If any base64 extracted: replace base64 strings in JSON → overwrite original evidence
     b. Update traces.request_body_sha256
  4. Continue with existing analysis pipeline
```

### Evidence Objects After Processing

For a trace with one base64 image in the request:

**raw_evidence_objects table:**
| object_type | object_ref | storage_backend | content_type | size_bytes |
|-------------|-----------|-----------------|-------------|------------|
| request_body | raw/2026/05/05/trace123/request_body.bin | filesystem | application/json | 256 |
| response_body | raw/2026/05/05/trace123/response_body.bin | filesystem | application/json | 150 |
| media_asset_000001 | raw/2026/05/05/trace123/media_asset_000001.bin | filesystem | image/png | 12345 |

**request_body.bin (modified):**
```json
{
  "model": "gpt-4.1",
  "messages": [
    {
      "role": "user",
      "content": [
        {"type": "text", "text": "What is in this image?"},
        {"type": "image_url", "image_url": {"url": "audit-media:media_asset_000001"}}
      ]
    }
  ]
}
```

**media_asset_000001.bin:** Decoded binary PNG data.

### Reference Resolution

To resolve `audit-media:media_asset_000001`:
1. Parse the reference to get `object_type = "media_asset_000001"`
2. Query: `SELECT object_ref FROM raw_evidence_objects WHERE trace_id = ? AND object_type = 'media_asset_000001'`
3. Read the binary file from the evidence store

When migrating to OSS, only `object_ref` and `storage_backend` columns change. The logical reference in the JSON remains the same.

## Detailed Design

### 1. EvidenceStore Abstraction (Python)

Replace `FileEvidenceStore` with an abstract protocol and concrete implementations:

```python
class EvidenceStore(Protocol):
    def read_text(self, object_ref: str) -> str: ...
    def read_bytes(self, object_ref: str) -> bytes: ...
    def write_text(self, object_ref: str, data: str) -> str: ...
    def write_bytes(self, object_ref: str, data: bytes, content_type: str) -> str: ...

class FilesystemEvidenceStore:
    """Current implementation using local filesystem."""
    # write_bytes creates the file in the same evidence directory structure
    # write_text overwrites existing files atomically (temp + rename)

class OSSEvidenceStore:
    """Future implementation for OSS/S3-compatible storage."""
```

`FilesystemEvidenceStore` validates all paths stay within the evidence root (same security checks as Go `ensureWithinRoot`).

### 2. Normalizer Changes (normalizers.py)

Modify `_media_message()` to capture and extract base64 data:

- When `_is_base64_data_url()` returns true:
  1. Decode the base64 payload to binary
  2. If decoded size > 20MB: return `protocol_item_type="base64_media_oversized"`, skip extraction
  3. Infer MIME type from data URL header (e.g., `image/png`)
  4. Call `evidence_store.write_bytes(trace_dir + "/media_asset_NNNNNN.bin", binary_data, mime_type)`
  5. Insert `raw_evidence_objects` record via repository
  6. Return `protocol_item_type="base64_media_extracted"` with metadata: `{object_type, media_type, size_bytes}`
  7. Record the replacement: `{original_base64_string, new_ref}`

- For non-data-URL base64 fields (`b64_json`, `data` in audio paths):
  - Same extraction logic, infer MIME type from context (key name, surrounding JSON structure)

The `normalize_json_trace()` function signature gains an optional `evidence_store` parameter. When provided, extraction is active. When not provided (e.g., unit tests), behavior is identical to current (marker only).

### 3. JSON Replacement and Evidence Overwrite

After `normalize_json_trace()` completes:

1. Collect all replacement mappings from the normalization pass
2. If no replacements: done (no overwrite needed)
3. If any replacements:
   a. Read original evidence JSON text
   b. For each mapping: `json_text = json_text.replace(original_base64, f"audit-media:media_asset_{seq:06d}")`
   c. Write modified text back via `evidence_store.write_text(object_ref, modified_json)`
   d. Compute new SHA256
   e. Update `traces.request_body_sha256` in the database

String-level replacement is used instead of structural JSON modification to preserve the original JSON formatting and avoid accidentally changing non-media fields.

### 4. Large Body Protection

- **Per-asset limit**: 20MB decoded size. Base64 data that decodes to > 20MB is skipped.
- **Skipped assets**: `protocol_item_type="base64_media_oversized"`, original JSON is NOT overwritten.
- **Partial extraction**: If some assets are extracted and others are skipped (oversized), only the successfully extracted assets are replaced in the JSON. The oversized base64 strings remain.
- **Rationale**: A partially modified JSON is better than no modification; the replaced references work correctly alongside the remaining base64 strings.

### 5. Error Handling

| Error | Behavior |
|-------|----------|
| Base64 decode fails | Skip extraction, mark as `base64_media` (current behavior) |
| Evidence store write fails | Skip extraction for this asset, log error, continue with others |
| DB insert fails for `raw_evidence_objects` | Skip extraction for this asset, log error |
| JSON overwrite fails | Log error, original evidence preserved, trace SHA256 unchanged |

If any error occurs during the extraction pipeline, the system falls back to current behavior (base64 stays in the JSON, only the marker is recorded).

### 6. Go-Side Cleanup

Delete the following files:
- `internal/gateway/media.go` -- `extractMediaReferences()` and related types/functions
- `internal/gateway/media_test.go` -- corresponding tests

No other Go code changes needed. The gateway remains a transparent proxy.

### 7. Protocol Family Coverage

All four protocol families are covered:

| Protocol | Content Types | Source |
|----------|--------------|--------|
| OpenAI Chat | `image_url` with data URL, `input_audio.data` | `normalizers.py` `_media_message()` |
| OpenAI Responses | `input_image` with data URL | `normalizers.py` `_media_message()` |
| Claude Messages | `image` with `image_url` data URL, `image.source.data` | `normalizers.py` `_media_message()` |
| Gemini | `inlineData.data` (base64), `inline_data.data` | `normalizers.py` `_media_message()` |

## Testing Plan

### E2E Tests

1. **OpenAI Chat + base64 image**: Send chat completion with a small base64 PNG. Verify: proxy forwards correctly, worker extracts binary, evidence JSON contains `audit-media:` reference, `media_asset` evidence exists in `raw_evidence_objects`.

2. **OpenAI Chat + image_url (HTTP)**: Send chat completion with `image_url` pointing to HTTP URL. Verify: worker identifies as `image_url` type, media snapshot job queued, no base64 extraction needed.

3. **Claude Messages + image**: Send Claude message with base64 image. Verify: extraction works for Claude format.

4. **Oversized base64**: Send base64 data > 20MB. Verify: marked as `base64_media_oversized`, no extraction, original JSON preserved.

### Unit Tests

- `test_evidence.py`: `FilesystemEvidenceStore.write_bytes()` and `write_text()` with path validation
- `test_normalizers.py`: Update existing base64 tests to verify extraction behavior when `evidence_store` is provided
- `test_repository.py`: `raw_evidence_objects` insertion for `media_asset` type

## Migration to OSS

When migrating evidence storage from filesystem to OSS:

1. Implement `OSSEvidenceStore` with the same `EvidenceStore` protocol
2. Update `object_ref` format (e.g., from `raw/2026/05/05/trace123/media_asset_000001.bin` to `oss://bucket/trace123/media_asset_000001`)
3. Update `storage_backend` in `raw_evidence_objects` from `filesystem` to `oss`
4. JSON references (`audit-media:media_asset_000001`) remain unchanged
5. Migrate existing binary files to OSS, update DB records
6. Worker configuration switches from `FilesystemEvidenceStore` to `OSSEvidenceStore`
