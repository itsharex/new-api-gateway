# new-api External Audit Gateway Design

Date: 2026-04-25
Status: approved design draft

## 1. Context

This project is an independent gateway in front of [QuantumNous/new-api](https://github.com/QuantumNous/new-api). The gateway is for internal company audit, not public multi-tenant moderation. Clients keep using their existing new-api API keys but change the base URL to this audit gateway.

The main audit goal is to understand and review company-provided API key usage:

- which employee number used which token;
- how many requests/tokens/cost each token consumed;
- whether usage appears work-related;
- whether a token shows abnormal consumption, repeated prompting, off-hours spikes, high-cost model overuse, or possible leakage;
- whether the gateway has route or content coverage gaps.

The gateway does not block requests in MVP. It captures complete evidence and analyzes asynchronously.

Primary references:

- new-api repository and relay routers: <https://github.com/QuantumNous/new-api>
- new-api API docs: <https://docs.newapi.pro/en/docs/api/>
- OpenAI Chat Completions, Responses, Images API docs: <https://platform.openai.com/docs/api-reference>
- Anthropic Messages API docs: <https://docs.anthropic.com/en/api/messages>
- Gemini generateContent docs: <https://ai.google.dev/api/generate-content>

## 2. Confirmed Decisions

- Deployment: independent gateway service in front of existing new-api.
- Mode: observe/audit only; no request or response blocking in MVP.
- Tenant model: no multi-tenancy; internal company deployment only.
- Capture scope: full request/response capture, including JSON, multipart, base64, images, audio, files, and stream events when supported.
- Remote URL snapshots: hybrid strategy. Store URL by default; snapshot generated outputs, company/allowlisted domains, and anomaly-related media.
- Identity source: new-api `tokens.name` is internally constrained to equal the employee number.
- Employee identity key: `employee_no`.
- new-api `users` are not audit identities and are not part of the core audit model.
- API key storage: plaintext API keys are never persisted; use HMAC fingerprint.
- Identity resolution: read-only query against new-api `tokens` table, with Redis and PostgreSQL local cache.
- UI API key lookup: supported by deriving the same fingerprint from a user-entered API key; plaintext key is discarded immediately.
- Route support: deep normalize common routes; raw + minimal metadata + alert for unsupported or later-normalized routes.
- Unsupported content: always alert, aggregate, and expose as coverage gaps.
- Anomaly detection: start with explainable rules; later add baselines and semantic similarity.
- Work relevance: start with admin-maintained context catalog; later add document/repo embeddings and RAG.
- Tech stack: Go gateway/admin API + Python analysis workers.
- Auth: MVP local accounts/RBAC; later SSO/OIDC using the same RBAC and audit log model.

## 3. Goals and Non-goals

### Goals

- Transparent proxy compatibility with new-api model relay endpoints.
- Complete raw evidence capture for audit reconstruction.
- Normalized content extraction for search, review, and analysis.
- Employee-number-based usage views.
- API key lookup by authorized auditors without persisting keys.
- Usage/cost aggregation by employee number, token, route, model, and time.
- Rule-based anomaly detection and review workflow.
- Route coverage alerts for raw-only, unsupported, unknown, or failed normalization cases.
- Clear audit trail for raw evidence access, exports, API key lookups, mapping/config changes, and review decisions.

### Non-goals for MVP

- Synchronous blocking or content enforcement.
- Multi-tenant isolation, billing, or per-tenant policy management.
- Treating new-api users as employee identities.
- Full RAG/embedding relevance engine.
- Advanced statistical baselines or semantic token-farming detection.
- Kafka/NATS-scale event platform.
- Deep normalization for every Midjourney/Suno/video/realtime protocol variant.

## 4. Overall Architecture

```text
Client / SDK / Agent
  -> Go Audit Gateway
  -> Existing new-api
  -> Upstream model providers

Go Audit Gateway
  -> PostgreSQL metadata
  -> Object Storage raw evidence
  -> Redis cache / queue
  -> Python Analysis Workers
  -> Go Admin API + Web UI
```

### Go Audit Gateway

Responsibilities:

- Reverse proxy requests to new-api with minimal mutation.
- Match requests through Route Registry.
- Extract API key from OpenAI, Claude, Gemini, Midjourney, and realtime request styles.
- Canonicalize key using new-api-compatible logic.
- Compute `token_fingerprint` using HMAC-SHA256.
- Resolve token identity from Redis/PostgreSQL cache or new-api read-only DB.
- Validate `tokens.name` as `employee_no` by configured regex.
- Generate `audit_trace_id`.
- Capture raw request/response bodies, headers, multipart parts, SSE chunks, and object references.
- Write trace metadata and evidence object metadata.
- Enqueue analysis jobs.
- Emit coverage alerts for unsupported or degraded capture/normalization.

### Go Admin API and Web UI

Responsibilities:

- Local login/RBAC in MVP.
- Trace Explorer.
- Employee/Token Usage dashboards.
- Anomaly Inbox.
- Coverage Alerts.
- API Key Lookup.
- Employee Directory / Token Identity views.
- Context Catalog.
- Review Decisions.
- Audit Action Logs.
- System Settings.

### Python Analysis Workers

Responsibilities:

- Protocol normalization.
- Usage extraction and aggregation.
- Work relevance classification.
- Rule-based anomaly detection.
- Coverage alert generation.
- Media snapshot jobs.
- Search index update.
- Later embedding/RAG/baseline/similarity analyzers.

### Storage

- PostgreSQL: structured metadata, identities, aggregates, alerts, analysis, reviews, audit logs, settings.
- Object Storage: raw evidence, media, base64-decoded objects, multipart files, SSE logs.
- Redis: hot identity cache, worker queue, short-window counters, alert dedupe/cooldown.

## 5. Request and Response Data Flow

### Synchronous Path

```text
1. Receive request
2. Match Route Registry
3. Extract and canonicalize API key
4. Compute token_fingerprint
5. Resolve token identity and employee_no
6. Generate audit_trace_id
7. Capture request raw evidence
8. Forward unchanged request to new-api
9. Tee/capture response raw evidence
10. Return unchanged response to client
11. Enqueue async analysis
```

### JSON Requests

- Read body with configured limits.
- Save raw JSON evidence.
- Compute body hash.
- Reconstruct upstream request with identical body.
- Extract minimal metadata synchronously when cheap.

### Multipart Requests

- Stream parts without loading large files fully into memory.
- Save form fields and file parts as evidence objects.
- Record part metadata: name, filename, content type, size, hash, object ref.
- Reconstruct multipart request for upstream forwarding.

### SSE Streaming Responses

- Tee chunks to client and evidence writer.
- Preserve event order.
- Save SSE log as NDJSON or original event stream.
- Reconstruct assistant/tool/usage content asynchronously.

### Binary or Unknown Responses

- Save raw object and metadata.
- Mark as `raw_only` or `minimal_parsed`.
- Emit coverage alert if route/content type is unsupported.

## 6. Route Coverage Strategy

Use a Route Registry instead of hard-coded ad hoc handling.

Route fields:

```text
method
path_pattern
protocol_family
body_kind
capture_mode
normalizer
minimal_extractor
identity_key_sources
media_snapshot_policy
unsupported_alert_policy
task_correlation_key
```

Example:

```json
{
  "method": "POST",
  "path_pattern": "/v1/images/generations",
  "protocol_family": "openai_images",
  "body_kind": "json",
  "capture_mode": "raw_and_normalized",
  "normalizer": "openai_image_generation",
  "minimal_extractor": "generic_json_prompt",
  "identity_key_sources": ["authorization"],
  "media_snapshot_policy": "generated_outputs",
  "unsupported_alert_policy": "normalizer_failed_high"
}
```

### MVP Deep Normalization Routes

- `POST /v1/chat/completions`
- `POST /pg/chat/completions`
- `POST /v1/responses`
- `POST /v1/responses/compact`
- `POST /v1/messages`
- `POST /v1/completions`
- `POST /v1/embeddings`
- `POST /v1/engines/:model/embeddings`
- `POST /v1/rerank`
- Gemini `POST /v1beta/models/*path`
- Gemini `POST /v1/models/*path`
- `POST /v1/images/generations`
- `POST /v1/images/edits`
- `POST /v1/edits`
- `POST /v1/audio/transcriptions`
- `POST /v1/audio/translations`
- `POST /v1/audio/speech`

### MVP Raw + Minimal Metadata Routes

- `GET /v1/realtime`
- `POST /v1/video/generations`
- `GET /v1/video/generations/:task_id`
- `POST /v1/videos`
- `GET /v1/videos/:task_id`
- `GET /v1/videos/:task_id/content`
- `POST /v1/videos/:video_id/remix`
- `POST /kling/v1/videos/text2video`
- `POST /kling/v1/videos/image2video`
- `GET /kling/v1/videos/text2video/:task_id`
- `GET /kling/v1/videos/image2video/:task_id`
- `POST /jimeng/`
- `/mj/*`
- `/:mode/mj/*`
- `/suno/*`

Minimal metadata includes method, route, employee number, token fingerprint, new-api token ID, model if extractable, prompt-like fields if extractable, task ID, media URLs, status, usage/cost if extractable, body sizes, and raw evidence refs.

### Unknown and Not Implemented Routes

- Capture raw if possible.
- Mark route status as `unknown_route` or `upstream_not_implemented`.
- Emit coverage alert.

Known upstream not-implemented examples include `/v1/images/variations`, `/v1/files*`, and `/v1/fine-tunes*`.

## 7. Identity Model

### Final Identity Rule

```text
new-api token.name must equal employee_no.
```

The audit identity is `employee_no`, not new-api user. The new-api user table is not used in the core audit model.

### Key Extraction Sources

- `Authorization: Bearer <key>`
- `x-api-key`
- query `key`
- `x-goog-api-key`
- `mj-api-secret`
- `Sec-WebSocket-Protocol` containing `openai-insecure-api-key.<key>`

### Canonicalization

```text
trim space
remove Bearer/bearer prefix
remove sk- prefix
split by "-"
take first segment as canonical_new_api_key
```

### Fingerprint

```text
token_fingerprint = HMAC-SHA256(canonical_new_api_key, audit_secret)
fingerprint_display = "tkfp_" + first_12_chars(base32(token_fingerprint))
```

Plaintext API keys are never persisted.

### Identity Resolution Flow

```text
1. Extract API key
2. Canonicalize API key
3. Compute token_fingerprint
4. Check Redis identity cache
5. On miss, check PostgreSQL token_identity_cache
6. On miss/stale, query new-api tokens table read-only
7. Read token.id and token.name
8. Validate token.name as employee_no
9. Enrich from audit_subjects if available
10. Save trace identity snapshot
```

### new-api DB Query

MVP only needs `tokens`, not `users`:

```sql
SELECT
  id AS token_id,
  name AS token_name,
  status AS token_status,
  "group" AS token_group,
  expired_time,
  accessed_time,
  remain_quota,
  used_quota,
  unlimited_quota,
  model_limits_enabled,
  model_limits
FROM tokens
WHERE key = ?
LIMIT 1;
```

The concrete SQL builder must quote reserved columns such as `group` according to the configured new-api database dialect.

### Identity Statuses

- `resolved`
- `cache_hit`
- `stale_cache`
- `not_found`
- `invalid_format`
- `missing_employee_no`
- `invalid_employee_no`
- `db_error`
- `extract_failed`

If identity is unresolved while new-api returns success, emit high-priority alert because gateway key extraction may differ from new-api behavior.

## 8. Data Model

### `traces`

One row per model/API call.

```text
id
trace_id
parent_trace_id
request_id_from_client
new_api_request_id
method
path
route_pattern
protocol_family
capture_mode
route_support_level
body_kind
status_code
upstream_status_code
stream
request_started_at
response_started_at
response_finished_at
duration_ms
client_ip_hash
user_agent_hash
request_body_size
response_body_size
request_body_sha256
response_body_sha256
request_raw_ref
response_raw_ref
request_headers_ref
response_headers_ref
token_fingerprint
fingerprint_display
new_api_token_id_snapshot
token_name_snapshot
employee_no_snapshot
audit_subject_display_name_snapshot
department_snapshot
identity_resolution_status
identity_cache_status
identity_resolved_at
model_requested
model_upstream
usage_prompt_tokens
usage_completion_tokens
usage_total_tokens
usage_reasoning_tokens
usage_cached_tokens
estimated_cost
error_type
error_message_redacted
analysis_status
created_at
updated_at
```

### `raw_evidence_objects`

```text
id
trace_id
object_type
object_ref
storage_backend
content_type
content_encoding
original_filename
size_bytes
sha256
redaction_status
encryption_status
created_at
```

Object types include request body, response body, request headers, response headers, multipart part, SSE log, media snapshot, decoded base64, and websocket log.

### `normalized_messages`

```text
id
trace_id
direction
sequence_index
role
modality
content_text
content_text_hash
media_object_id
media_url
source_path
protocol_item_type
token_count_estimate
metadata_json
created_at
```

### `token_identity_cache`

```text
token_fingerprint
fingerprint_display
new_api_token_id
token_name_raw
employee_no
audit_subject_display_name
department
token_status
token_group
token_expired_time
token_accessed_time
remain_quota
used_quota
unlimited_quota
model_limits_enabled
model_limits
source
resolved_at
refreshed_at
expires_at
last_seen_at
resolution_error
```

### `audit_subjects`

Local enrichment keyed by employee number.

```text
employee_no primary key
display_name
department
email
status
source
created_at
updated_at
```

### `usage_aggregates`

```text
id
bucket_start
bucket_size
token_fingerprint
new_api_token_id
employee_no
token_name_snapshot
model
route_pattern
protocol_family
request_count
success_count
error_count
stream_count
prompt_tokens
completion_tokens
total_tokens
reasoning_tokens
cached_tokens
estimated_cost
request_body_bytes
response_body_bytes
created_at
updated_at
```

### `usage_anomalies`

```text
id
anomaly_id
anomaly_type
severity
status
token_fingerprint
fingerprint_display
new_api_token_id
employee_no
token_name_snapshot
window_start
window_end
observed_value
threshold_value
baseline_value
model
route_pattern
sample_trace_ids
reason
detector_version
reviewer_id
review_note
reviewed_at
created_at
updated_at
```

### `coverage_alerts`

```text
id
alert_id
alert_code
severity
status
method
route_pattern
raw_path
content_type
protocol_family
payload_shape_hash
normalizer
normalizer_version
first_seen_at
last_seen_at
occurrence_count
affected_trace_count
affected_token_count
affected_employee_count
sample_trace_ids
message
owner_note
created_at
updated_at
```

### `analysis_results`

```text
id
trace_id
analyzer_name
analyzer_version
policy_version
category
label
score
confidence
severity
evidence_message_ids
evidence_spans_json
result_json
created_at
```

### `context_catalog`

```text
id
context_type
name
description
keywords
aliases
owner
expected_task_categories
expected_models
expected_usage_level
active
created_by
updated_by
created_at
updated_at
```

### `review_decisions`

```text
id
target_type
target_id
decision
reviewer_id
reviewer_username
note
created_at
```

### `audit_users`

```text
id
username
password_hash
auth_provider
external_subject
display_name
email
role
status
created_at
updated_at
```

### `audit_action_logs`

```text
id
actor_user_id
actor_username
action
target_type
target_id
token_fingerprint
fingerprint_display
trace_id
ip_hash
user_agent_hash
metadata_json
created_at
```

### `anomaly_rules`

```text
id
rule_key
enabled
scope_type
scope_value
window
threshold_json
severity
cooldown
created_by
updated_by
created_at
updated_at
```

### `media_snapshot_jobs`

```text
id
trace_id
source_url
source_context
policy_reason
status
object_id
error
created_at
updated_at
```

## 9. Analysis Design

### Pipeline

```text
trace_captured
  -> protocol_normalization
  -> usage_extraction
  -> usage_aggregation
  -> work_relevance_classification
  -> anomaly_detection
  -> coverage_alert_generation
  -> media_snapshot_if_needed
  -> search_index_update
```

### Work Relevance MVP

Inputs:

- normalized request text;
- normalized response summary;
- route/protocol/model;
- employee number;
- token identity;
- context catalog;
- review feedback.

Outputs:

```json
{
  "task_category": "coding",
  "work_related_score": 0.82,
  "personal_use_score": 0.08,
  "confidence": 0.74,
  "matched_context": [
    {
      "type": "repo",
      "name": "new-api-gateway",
      "matched_terms": ["new-api", "gateway", "relay"]
    }
  ],
  "evidence": ["Request mentions new-api relay routes and audit gateway."],
  "needs_review": false
}
```

Initial task categories:

- coding
- debugging
- code_review
- documentation
- translation
- data_analysis
- meeting_summary
- product_design
- customer_support
- research
- operations
- personal_chat
- entertainment
- unknown

### Usage Extraction

Priority:

1. upstream response `usage`;
2. new-api-related usage/quota metadata when available;
3. tokenizer estimate;
4. mark `usage_extraction_failed`.

Fields:

- prompt tokens;
- completion tokens;
- total tokens;
- reasoning tokens;
- cached tokens;
- audio tokens;
- image count;
- input/output bytes;
- estimated cost.

### Rule-based Anomaly MVP

Rules:

- daily token limit exceeded;
- short-window token spike;
- expensive model overuse;
- long output anomaly;
- repeated prompt;
- retry storm;
- off-hours high usage;
- raw-only high volume;
- low work relevance + high cost;
- identity unresolved while upstream succeeded;
- invalid/missing employee number;
- possible token leak signals.

Every alert must include observed value, threshold/baseline, time window, token/employee number, routes/models, sample traces, and a human-readable reason.

### Later Analysis

- 7/30-day personal baselines.
- Same-hour and same-weekday baselines.
- Department/project peer comparison.
- Prompt embeddings and near-duplicate detection.
- RAG/embedding relevance against internal docs/repos/tickets.
- Token leak/share clustering across IP/UA/time patterns.

## 10. Admin Product

### Navigation

```text
Overview
Employee / Token Usage
Trace Explorer
Anomaly Inbox
Coverage Alerts
API Key Lookup
Employee Directory / Token Identity
Context Catalog
Review Decisions
Audit Action Logs
System Settings
```

### Overview

Show request count, token count, estimated cost, top employee numbers, top tokens, top models, high-cost trend, suspected non-work high cost, open anomalies, open coverage alerts, raw-only percentage, capture/analysis failures, and queue lag.

### Employee / Token Usage

Employee page shows usage trend, associated tokens, model mix, route mix, task categories, work relevance buckets, high-cost traces, anomalies, and review outcomes.

Token page shows fingerprint display, new-api token ID, token name, employee number, first/last seen, quota/status/model limits, usage trend, model/route mix, traces, and anomalies.

### Trace Explorer

Filters include trace ID, employee number, fingerprint, token name, route, model, status, stream, task category, work relevance, anomaly, keyword, body size, and time.

Trace detail tabs:

- Summary;
- Conversation / Normalized;
- Raw Evidence;
- Analysis;
- Review.

Raw Evidence requires permission and writes audit logs.

### Anomaly Inbox

Show severity, anomaly type, employee number, token, window, observed value, threshold/baseline, samples, and status. Actions: acknowledge, dismiss, confirm, mark personal use, mark abuse, add note.

### Coverage Alerts

Show unsupported/unknown/raw-only/failed-normalizer cases. Actions: acknowledge, ignore for now, mark needs normalizer, mark fixed, open sample trace.

### API Key Lookup

Authorized auditors can paste an API key. Backend computes fingerprint, discards plaintext, optionally refreshes token cache from new-api DB, and returns employee number, token metadata, usage, traces, and anomalies. Every lookup writes audit action log.

### Employee Directory / Token Identity

Show employee numbers discovered from token names, associated tokens, invalid/missing employee number alerts, and optional display name/department enrichment.

### Context Catalog

Manage projects, products, repos, services, keywords, aliases, expected task categories, expected models, and expected usage levels.

### Permissions

Roles:

- Viewer: aggregate views only.
- Auditor: normalized traces, reviews, anomalies, context catalog.
- Raw Access: raw evidence, evidence download, API key lookup.
- Admin: users, roles, route registry, anomaly rules, settings, coverage handling, directory enrichment.

## 11. Security

### API Keys

- Plaintext API keys only exist in memory.
- Never log API keys.
- Never store API keys in DB, object storage, URLs, access logs, or error logs.
- For query-string key sources, redact the stored raw URL.
- Store HMAC fingerprint only.
- Protect `audit_secret` in environment/secret manager.

### Raw Evidence

- Object storage encryption enabled.
- Headers such as Authorization, Cookie, x-api-key, x-goog-api-key, and mj-api-secret are redacted in header evidence.
- Request/response bodies are stored for audit, but UI defaults to normalized/redacted views.
- Raw reveal/download requires permission and writes audit log.

### UI/API

- Passwords hashed with argon2id or bcrypt.
- Secure session cookies.
- CSRF protection.
- Action-level RBAC.
- API key lookup rate limits.
- Raw reveal/download rate limits.
- No secret plaintext in settings UI.

### Remote Media Snapshots

Downloader restrictions:

- http/https only;
- maximum size;
- maximum duration;
- redirect limit;
- MIME allowlist;
- default deny for link-local, metadata, and private IPs;
- allow company internal domains only by explicit config;
- record source URL, final URL, content type, size, hash, and status.

## 12. Error Handling and Operations

Default policy:

```text
forward first
capture best effort
alert failures
analyze asynchronously
```

Important cases:

- new-api errors are returned unchanged and recorded.
- identity failures do not block forwarding.
- capture failures generate P0/P1 alerts.
- object storage outage falls back to local spool when configured.
- Redis outage falls back to PostgreSQL cache and optional PostgreSQL job table.
- PostgreSQL outage triggers emergency spool of minimal trace envelope and P0 alert.
- worker failures retry and then create analysis alerts.

Suggested MVP targets:

- JSON proxy p95 overhead below 50-100ms excluding upstream model time.
- SSE first-byte overhead below 50ms.
- Redis identity hit rate above 95% after warmup.
- capture enqueue success above 99.9%.
- analysis p95 lag below 5 minutes.

Metrics:

- gateway requests/latency/status;
- upstream latency/status;
- capture success/failure;
- object storage latency/failure;
- Redis hit/miss;
- identity statuses;
- invalid employee number count;
- queue lag;
- worker success/failure;
- raw-only route count;
- coverage/anomaly counts;
- storage growth.

Health checks:

- gateway liveness/readiness;
- PostgreSQL;
- Redis;
- object storage;
- new-api read-only DB;
- worker heartbeat.

Backup:

- PostgreSQL;
- object storage;
- route registry/settings;
- `audit_secret` through secret manager. Losing `audit_secret` prevents matching future API key lookup fingerprints to historical traces.

## 13. Testing and Acceptance

### Test Areas

- Transparent proxy behavior.
- API key extraction/canonicalization.
- HMAC fingerprint stability.
- Identity cache and new-api token lookup.
- Employee number validation.
- Raw evidence object integrity.
- Header redaction.
- JSON/multipart/SSE capture.
- Protocol normalizers.
- Usage extraction and aggregation.
- Rule anomaly detection.
- Work relevance classification.
- Coverage alert generation/deduplication.
- RBAC and audit action logs.
- API key lookup privacy.
- Remote media snapshot SSRF protections.

### MVP Acceptance Criteria

- Clients can switch base URL to audit gateway and call common new-api routes successfully.
- Each request gets an `audit_trace_id`.
- Request/response raw evidence is saved and hashable.
- API key fingerprint is stable and plaintext keys are not persisted.
- new-api token name is resolved as `employee_no`.
- Invalid or missing employee number triggers alert.
- Employee/token usage dashboards show hourly/daily request, token, model, route, and cost data.
- Trace Explorer supports employee number, token, model, route, status, and time filters.
- Authorized users can view raw evidence with audit logging.
- API Key Lookup returns fingerprint, employee number, token metadata, traces, usage, and anomalies without storing plaintext key.
- At least five rule-based anomalies work end to end.
- Raw/minimal routes produce coverage alerts.
- Worker failures do not break request forwarding.
- Storage/cache/DB failures produce clear degraded statuses or alerts.

## 14. Phased Implementation Plan Boundary

A detailed implementation plan will be created separately after this design is approved.

Suggested phases:

1. Project skeleton and infrastructure.
2. Transparent proxy and evidence capture.
3. Identity resolution and API key lookup.
4. Route normalization and usage aggregation.
5. Anomaly detection and work relevance MVP.
6. Admin UI, RBAC, audit logs.
7. Operational hardening, retention, metrics, and backfill/reanalysis.
