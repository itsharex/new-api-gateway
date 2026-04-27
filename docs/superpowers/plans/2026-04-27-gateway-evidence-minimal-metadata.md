# Gateway Evidence Minimal Metadata Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the gateway-side evidence and minimal metadata slice so every captured trace has redacted header evidence, broader route coverage, extractable request model, extractable usage tokens, and richer analysis job envelopes.

**Architecture:** Keep the gateway as the synchronous capture boundary and keep analysis asynchronous. Add small helper files for header sanitization and minimal JSON metadata extraction, extend trace persistence with extra nullable/defaulted fields, and enrich job envelopes only after trace and raw evidence rows are durable.

**Tech Stack:** Go 1.22+, standard `net/http`, PostgreSQL migrations, `pgx`, Redis list publisher, filesystem evidence store, `httptest`, `go test ./...`.

---

## Scope Check

The approved design covers gateway capture, Python analyzers, Admin API, RBAC, UI dashboards, review workflows, SSO, and production storage. That is more than one independently testable subsystem. This plan implements only the next gateway completion slice:

- redacted request and response header evidence;
- trace columns for header refs and usage tokens;
- minimal request model and response usage extraction;
- route registry coverage for MVP routes named in the approved design;
- job envelopes with evidence refs and minimal metadata for the Python worker.

This plan does not implement Admin API, Web UI, RBAC, anomaly review screens, or full Python normalizers.

## Current Code Context

- Module path: `github.com/your-company/new-api-gateway`.
- Main gateway handler: `internal/gateway/proxy.go`.
- Gateway capture helpers: `internal/gateway/capture.go`, `internal/gateway/stream.go`, `internal/gateway/multipart.go`.
- Trace domain and repository: `internal/traces/model.go`, `internal/traces/repository.go`.
- Job envelope: `internal/jobs/jobs.go`.
- Route registry: `internal/routes/registry.go`.
- Current migration: `migrations/0001_core_schema.sql`.
- Existing tests are package-local and use in-memory fakes. Follow that style instead of adding a live PostgreSQL dependency.

## File Structure

- Create `internal/gateway/headers.go`: sanitize request and response headers into stable JSON bytes that never contain plaintext API keys.
- Create `internal/gateway/headers_test.go`: verifies header redaction and stable JSON output.
- Create `internal/gateway/minimal.go`: extract request model and response token usage from JSON bodies.
- Create `internal/gateway/minimal_test.go`: verifies OpenAI-style and Anthropic-style usage extraction.
- Modify `internal/gateway/proxy.go`: write `request_headers` and `response_headers` evidence objects, insert raw evidence rows, populate trace metadata, and publish richer jobs after durable evidence persistence.
- Modify `internal/gateway/proxy_test.go`: add end-to-end assertions for header evidence, trace metadata, usage extraction, and enriched jobs.
- Modify `internal/traces/model.go`: add header refs and token usage fields to `Trace`.
- Modify `internal/traces/repository.go`: persist the new trace fields.
- Modify `internal/traces/repository_test.go`: keep SQL placeholders aligned with repository args.
- Create `migrations/0002_trace_minimal_metadata.sql`: add new trace columns for existing developer databases.
- Modify `internal/jobs/jobs.go`: add evidence refs, content types, model, usage fields, and constructor inputs.
- Modify `internal/jobs/jobs_test.go`: assert JSON output includes the new fields.
- Modify `workers/analysis_worker/contract_example.json`: include the enriched job shape.
- Modify `workers/analysis_worker/main.py`: accept the enriched job shape and keep backward-compatible parsing for current tests.
- Modify `internal/routes/registry.go`: support segment parameters and add missing MVP routes.
- Modify `internal/routes/registry_test.go`: verify all newly registered route families and wildcard boundaries.
- Modify `docs/development.md`: document that header evidence is redacted and jobs now contain evidence refs.

---

### Task 1: Trace Schema and Repository Fields

**Files:**
- Modify: `internal/traces/model.go`
- Modify: `internal/traces/repository.go`
- Modify: `internal/traces/repository_test.go`
- Create: `migrations/0002_trace_minimal_metadata.sql`

- [ ] **Step 1: Write the repository test changes first**

Update `internal/traces/repository_test.go` so `TestPostgresRepositoryNormalizesZeroResponseFinishedAtToNull` expects the new argument count and checks representative new fields:

```go
func TestPostgresRepositoryNormalizesZeroResponseFinishedAtToNull(t *testing.T) {
	execer := &recordingExecer{}
	repo := PostgresRepository{execer: execer}
	trace := validTrace()
	trace.ResponseFinishedAt = time.Time{}

	if err := repo.InsertTrace(context.Background(), trace); err != nil {
		t.Fatalf("InsertTrace error: %v", err)
	}

	if !strings.Contains(execer.query, "INSERT INTO traces") {
		t.Fatalf("query = %q, want traces insert", execer.query)
	}
	assertPlaceholderAlignment(t, execer.query, execer.args)
	if len(execer.args) != 36 {
		t.Fatalf("arg count = %d, want 36", len(execer.args))
	}
	assertArg(t, execer.args, 0, trace.TraceID)
	assertArg(t, execer.args, 1, trace.Method)
	assertArg(t, execer.args, 2, trace.Path)
	assertArg(t, execer.args, 3, trace.RoutePattern)
	assertArg(t, execer.args, 4, trace.ProtocolFamily)
	assertArg(t, execer.args, 6, trace.StatusCode)
	assertArg(t, execer.args, 9, trace.RequestStartedAt)
	if execer.args[10] != nil {
		t.Fatalf("response_finished_at arg = %#v, want nil", execer.args[10])
	}
	assertArg(t, execer.args, 17, trace.RequestHeadersRef)
	assertArg(t, execer.args, 18, trace.ResponseRawRef)
	assertArg(t, execer.args, 19, trace.ResponseHeadersRef)
	assertArg(t, execer.args, 20, trace.TokenFingerprint)
	assertArg(t, execer.args, 22, trace.NewAPITokenIDSnapshot)
	assertArg(t, execer.args, 24, trace.EmployeeNoSnapshot)
	assertArg(t, execer.args, 28, trace.UsagePromptTokens)
	assertArg(t, execer.args, 30, trace.UsageTotalTokens)
	assertArg(t, execer.args, 34, trace.AnalysisStatus)
	assertArg(t, execer.args, 35, trace.CreatedAt)
}
```

Update `validTrace()` in the same file:

```go
func validTrace() Trace {
	startedAt := time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC)
	finishedAt := startedAt.Add(750 * time.Millisecond)
	return Trace{
		TraceID:                  "trace_1",
		Method:                   "POST",
		Path:                     "/v1/chat/completions",
		RoutePattern:             "/v1/chat/completions",
		ProtocolFamily:           "openai",
		CaptureMode:              "full",
		StatusCode:               200,
		UpstreamStatusCode:       200,
		Stream:                   true,
		RequestStartedAt:         startedAt,
		ResponseFinishedAt:       finishedAt,
		DurationMillis:           750,
		RequestBodySize:          128,
		ResponseBodySize:         256,
		RequestBodySHA256:        "request-sha",
		ResponseBodySHA256:       "response-sha",
		RequestRawRef:            "raw/trace_1/request.body",
		RequestHeadersRef:        "raw/trace_1/request_headers.bin",
		ResponseRawRef:           "raw/trace_1/response.body",
		ResponseHeadersRef:       "raw/trace_1/response_headers.bin",
		TokenFingerprint:         "fp_123",
		FingerprintDisplay:       "sk-...123",
		NewAPITokenIDSnapshot:    42,
		TokenNameSnapshot:        "prod-token",
		EmployeeNoSnapshot:       "E123",
		IdentityResolutionStatus: "resolved",
		IdentityCacheStatus:      "miss",
		ModelRequested:           "gpt-4.1",
		UsagePromptTokens:        10,
		UsageCompletionTokens:    20,
		UsageTotalTokens:         30,
		UsageReasoningTokens:     4,
		UsageCachedTokens:        2,
		AnalysisStatus:           "pending",
		CreatedAt:                startedAt.Add(time.Second),
	}
}
```

- [ ] **Step 2: Run the repository test and verify it fails**

Run:

```bash
go test ./internal/traces -run TestPostgresRepositoryNormalizesZeroResponseFinishedAtToNull -v
```

Expected: FAIL because `Trace` does not have the new fields and `InsertTrace` still writes 28 args.

- [ ] **Step 3: Extend the trace model**

Modify `internal/traces/model.go` by replacing the `Trace` struct with:

```go
type Trace struct {
	TraceID                  string
	Method                   string
	Path                     string
	RoutePattern             string
	ProtocolFamily           string
	CaptureMode              string
	StatusCode               int
	UpstreamStatusCode       int
	Stream                   bool
	RequestStartedAt         time.Time
	ResponseFinishedAt       time.Time
	DurationMillis           int64
	RequestBodySize          int64
	ResponseBodySize         int64
	RequestBodySHA256        string
	ResponseBodySHA256       string
	RequestRawRef            string
	RequestHeadersRef        string
	ResponseRawRef           string
	ResponseHeadersRef       string
	TokenFingerprint         string
	FingerprintDisplay       string
	NewAPITokenIDSnapshot    int
	TokenNameSnapshot        string
	EmployeeNoSnapshot       string
	IdentityResolutionStatus string
	IdentityCacheStatus      string
	ModelRequested           string
	UsagePromptTokens        int
	UsageCompletionTokens    int
	UsageTotalTokens         int
	UsageReasoningTokens     int
	UsageCachedTokens        int
	EstimatedCost            string
	AnalysisStatus           string
	CreatedAt                time.Time
}
```

Keep `RawEvidenceObject` unchanged.

- [ ] **Step 4: Add the migration**

Create `migrations/0002_trace_minimal_metadata.sql`:

```sql
ALTER TABLE traces
    ADD COLUMN IF NOT EXISTS request_headers_ref TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS response_headers_ref TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS usage_prompt_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS usage_completion_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS usage_total_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS usage_reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS usage_cached_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS estimated_cost TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 5: Update the repository insert**

Modify `internal/traces/repository.go` so `InsertTrace` inserts the new columns:

```go
_, err := r.execer.Exec(ctx, `
INSERT INTO traces (
  trace_id, method, path, route_pattern, protocol_family, capture_mode,
  status_code, upstream_status_code, stream, request_started_at, response_finished_at,
  duration_ms, request_body_size, response_body_size, request_body_sha256, response_body_sha256,
  request_raw_ref, request_headers_ref, response_raw_ref, response_headers_ref,
  token_fingerprint, fingerprint_display,
  new_api_token_id_snapshot, token_name_snapshot, employee_no_snapshot,
  identity_resolution_status, identity_cache_status, model_requested,
  usage_prompt_tokens, usage_completion_tokens, usage_total_tokens, usage_reasoning_tokens,
  usage_cached_tokens, estimated_cost, analysis_status, created_at
) VALUES (
  $1,$2,$3,$4,$5,$6,
  $7,$8,$9,$10,$11,
  $12,$13,$14,$15,$16,
  $17,$18,$19,$20,
  $21,$22,
  $23,$24,$25,
  $26,$27,$28,
  $29,$30,$31,$32,
  $33,$34,$35,$36
)`,
	trace.TraceID, trace.Method, trace.Path, trace.RoutePattern, trace.ProtocolFamily, trace.CaptureMode,
	trace.StatusCode, trace.UpstreamStatusCode, trace.Stream, trace.RequestStartedAt, responseFinishedAt,
	trace.DurationMillis, trace.RequestBodySize, trace.ResponseBodySize, trace.RequestBodySHA256, trace.ResponseBodySHA256,
	trace.RequestRawRef, trace.RequestHeadersRef, trace.ResponseRawRef, trace.ResponseHeadersRef,
	trace.TokenFingerprint, trace.FingerprintDisplay,
	trace.NewAPITokenIDSnapshot, trace.TokenNameSnapshot, trace.EmployeeNoSnapshot,
	trace.IdentityResolutionStatus, trace.IdentityCacheStatus, trace.ModelRequested,
	trace.UsagePromptTokens, trace.UsageCompletionTokens, trace.UsageTotalTokens, trace.UsageReasoningTokens,
	trace.UsageCachedTokens, trace.EstimatedCost, trace.AnalysisStatus, trace.CreatedAt,
)
```

- [ ] **Step 6: Run the repository tests**

Run:

```bash
go test ./internal/traces -v
```

Expected: PASS.

- [ ] **Step 7: Commit the schema and trace repository changes**

Run:

```bash
git add internal/traces/model.go internal/traces/repository.go internal/traces/repository_test.go migrations/0002_trace_minimal_metadata.sql
git commit -m "feat: extend trace metadata schema"
```

Expected: commit succeeds.

---

### Task 2: Redacted Header Evidence Helpers

**Files:**
- Create: `internal/gateway/headers.go`
- Create: `internal/gateway/headers_test.go`

- [ ] **Step 1: Write header sanitization tests**

Create `internal/gateway/headers_test.go`:

```go
package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestHeaderEvidenceJSONRedactsSecrets(t *testing.T) {
	header := http.Header{}
	header.Set("Authorization", "Bearer sk-secret-value")
	header.Set("x-api-key", "sk-anthropic-secret")
	header.Set("x-goog-api-key", "sk-gemini-secret")
	header.Set("mj-api-secret", "mj-secret")
	header.Set("Sec-WebSocket-Protocol", "realtime, openai-insecure-api-key.sk-real-secret, openai-beta.realtime-v1")
	header.Set("Content-Type", "application/json")

	data, err := headerEvidenceJSON(header)
	if err != nil {
		t.Fatalf("headerEvidenceJSON error: %v", err)
	}
	text := string(data)
	for _, secret := range []string{"sk-secret-value", "sk-anthropic-secret", "sk-gemini-secret", "mj-secret", "sk-real-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("header evidence leaked %q in %s", secret, text)
		}
	}

	var decoded map[string][]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("header evidence is not JSON: %v", err)
	}
	if decoded["Authorization"][0] != "[REDACTED]" {
		t.Fatalf("Authorization = %q", decoded["Authorization"][0])
	}
	if decoded["Content-Type"][0] != "application/json" {
		t.Fatalf("Content-Type = %q", decoded["Content-Type"][0])
	}
	if !strings.Contains(decoded["Sec-WebSocket-Protocol"][0], "openai-insecure-api-key.[REDACTED]") {
		t.Fatalf("Sec-WebSocket-Protocol = %q", decoded["Sec-WebSocket-Protocol"][0])
	}
}

func TestHeaderEvidenceJSONIsStable(t *testing.T) {
	header := http.Header{}
	header.Add("X-Zeta", "z")
	header.Add("X-Alpha", "a")

	first, err := headerEvidenceJSON(header)
	if err != nil {
		t.Fatalf("first headerEvidenceJSON error: %v", err)
	}
	second, err := headerEvidenceJSON(header)
	if err != nil {
		t.Fatalf("second headerEvidenceJSON error: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("header JSON is not stable:\nfirst=%s\nsecond=%s", first, second)
	}
}
```

- [ ] **Step 2: Run the header tests and verify they fail**

Run:

```bash
go test ./internal/gateway -run TestHeaderEvidenceJSON -v
```

Expected: FAIL because `headerEvidenceJSON` is undefined.

- [ ] **Step 3: Implement the helper**

Create `internal/gateway/headers.go`:

```go
package gateway

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

var sensitiveHeaderNames = map[string]struct{}{
	"authorization":          {},
	"x-api-key":              {},
	"x-goog-api-key":         {},
	"mj-api-secret":          {},
	"proxy-authorization":    {},
	"openai-organization":    {},
	"anthropic-api-key":      {},
	"anthropic-auth-token":   {},
}

func headerEvidenceJSON(header http.Header) ([]byte, error) {
	snapshot := make(map[string][]string, len(header))
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		values := header.Values(key)
		copied := make([]string, 0, len(values))
		for _, value := range values {
			copied = append(copied, sanitizeHeaderValue(key, value))
		}
		snapshot[http.CanonicalHeaderKey(key)] = copied
	}
	return json.Marshal(snapshot)
}

func sanitizeHeaderValue(key, value string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if _, ok := sensitiveHeaderNames[normalized]; ok {
		return "[REDACTED]"
	}
	if normalized == "sec-websocket-protocol" {
		parts := strings.Split(value, ",")
		for i, part := range parts {
			trimmed := strings.TrimSpace(part)
			if strings.HasPrefix(trimmed, "openai-insecure-api-key.") {
				parts[i] = " openai-insecure-api-key.[REDACTED]"
				if i == 0 {
					parts[i] = "openai-insecure-api-key.[REDACTED]"
				}
			} else {
				parts[i] = part
			}
		}
		return strings.Join(parts, ",")
	}
	return value
}
```

- [ ] **Step 4: Run the gateway header tests**

Run:

```bash
go test ./internal/gateway -run TestHeaderEvidenceJSON -v
```

Expected: PASS.

- [ ] **Step 5: Commit the header helper**

Run:

```bash
git add internal/gateway/headers.go internal/gateway/headers_test.go
git commit -m "feat: add redacted header evidence helper"
```

Expected: commit succeeds.

---

### Task 3: Minimal JSON Metadata Extraction

**Files:**
- Create: `internal/gateway/minimal.go`
- Create: `internal/gateway/minimal_test.go`

- [ ] **Step 1: Write metadata extraction tests**

Create `internal/gateway/minimal_test.go`:

```go
package gateway

import "testing"

func TestExtractRequestModelFromJSONBody(t *testing.T) {
	got := extractRequestModel("/v1/chat/completions", []byte(`{"model":"gpt-test","messages":[]}`))
	if got != "gpt-test" {
		t.Fatalf("model = %q", got)
	}
}

func TestExtractRequestModelFromEngineEmbeddingPath(t *testing.T) {
	got := extractRequestModel("/v1/engines/text-embedding-3-small/embeddings", []byte(`{"input":"hello"}`))
	if got != "text-embedding-3-small" {
		t.Fatalf("model = %q", got)
	}
}

func TestExtractOpenAIUsage(t *testing.T) {
	usage := extractResponseUsage([]byte(`{
		"usage": {
			"prompt_tokens": 11,
			"completion_tokens": 7,
			"total_tokens": 18,
			"prompt_tokens_details": {"cached_tokens": 3},
			"completion_tokens_details": {"reasoning_tokens": 2}
		}
	}`))
	if usage.PromptTokens != 11 || usage.CompletionTokens != 7 || usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.CachedTokens != 3 || usage.ReasoningTokens != 2 {
		t.Fatalf("usage details = %+v", usage)
	}
}

func TestExtractAnthropicUsage(t *testing.T) {
	usage := extractResponseUsage([]byte(`{
		"usage": {
			"input_tokens": 5,
			"output_tokens": 9,
			"cache_read_input_tokens": 4,
			"cache_creation_input_tokens": 2
		}
	}`))
	if usage.PromptTokens != 5 || usage.CompletionTokens != 9 || usage.TotalTokens != 14 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.CachedTokens != 6 {
		t.Fatalf("CachedTokens = %d, want 6", usage.CachedTokens)
	}
}

func TestExtractResponseUsageReturnsZeroForInvalidJSON(t *testing.T) {
	usage := extractResponseUsage([]byte(`not-json`))
	if usage != (minimalUsage{}) {
		t.Fatalf("usage = %+v, want zero", usage)
	}
}
```

- [ ] **Step 2: Run the metadata tests and verify they fail**

Run:

```bash
go test ./internal/gateway -run 'TestExtractRequestModel|TestExtract.*Usage' -v
```

Expected: FAIL because the extraction functions are undefined.

- [ ] **Step 3: Implement minimal extraction**

Create `internal/gateway/minimal.go`:

```go
package gateway

import (
	"encoding/json"
	"strings"
)

type minimalUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ReasoningTokens  int
	CachedTokens     int
}

func extractRequestModel(path string, body []byte) string {
	if model := modelFromEngineEmbeddingPath(path); model != "" {
		return model
	}
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Model
}

func modelFromEngineEmbeddingPath(path string) string {
	const prefix = "/v1/engines/"
	const suffix = "/embeddings"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	model := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if model == "" || strings.Contains(model, "/") {
		return ""
	}
	return model
}

func extractResponseUsage(body []byte) minimalUsage {
	var payload struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			CacheReadTokens  int `json:"cache_read_input_tokens"`
			CacheCreateTokens int `json:"cache_creation_input_tokens"`
			PromptDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return minimalUsage{}
	}

	usage := minimalUsage{
		PromptTokens:     payload.Usage.PromptTokens,
		CompletionTokens: payload.Usage.CompletionTokens,
		TotalTokens:      payload.Usage.TotalTokens,
		ReasoningTokens:  payload.Usage.CompletionDetails.ReasoningTokens,
		CachedTokens:     payload.Usage.PromptDetails.CachedTokens,
	}
	if usage.PromptTokens == 0 && payload.Usage.InputTokens > 0 {
		usage.PromptTokens = payload.Usage.InputTokens
	}
	if usage.CompletionTokens == 0 && payload.Usage.OutputTokens > 0 {
		usage.CompletionTokens = payload.Usage.OutputTokens
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if usage.CachedTokens == 0 {
		usage.CachedTokens = payload.Usage.CacheReadTokens + payload.Usage.CacheCreateTokens
	}
	return usage
}
```

- [ ] **Step 4: Run the metadata tests**

Run:

```bash
go test ./internal/gateway -run 'TestExtractRequestModel|TestExtract.*Usage' -v
```

Expected: PASS.

- [ ] **Step 5: Commit the metadata helper**

Run:

```bash
git add internal/gateway/minimal.go internal/gateway/minimal_test.go
git commit -m "feat: extract minimal gateway metadata"
```

Expected: commit succeeds.

---

### Task 4: Enriched Job Envelope

**Files:**
- Modify: `internal/jobs/jobs.go`
- Modify: `internal/jobs/jobs_test.go`
- Modify: `workers/analysis_worker/contract_example.json`
- Modify: `workers/analysis_worker/main.py`

- [ ] **Step 1: Update job tests first**

Modify `internal/jobs/jobs_test.go` so `TestRedisListPublisherPushesTraceCapturedEnvelope` constructs and verifies an enriched job:

```go
func TestRedisListPublisherPushesTraceCapturedEnvelope(t *testing.T) {
	client := &fakeRedisListClient{}
	publisher := NewRedisListPublisher(client, "analysis_jobs")

	job := NewTraceCaptured(TraceCapturedInput{
		TraceID:             "trace_1",
		RoutePattern:        "/v1/chat/completions",
		ProtocolFamily:      "openai_chat",
		CaptureMode:         "raw_and_normalized",
		EmployeeNo:          "E12345",
		RequestRawRef:       "raw/trace_1/request_body.bin",
		RequestHeadersRef:   "raw/trace_1/request_headers.bin",
		ResponseRawRef:      "raw/trace_1/response_body.bin",
		ResponseHeadersRef:  "raw/trace_1/response_headers.bin",
		RequestContentType:  "application/json",
		ResponseContentType: "application/json",
		ModelRequested:      "gpt-test",
		UsageTotalTokens:    18,
	})
	err := publisher.PublishTraceCaptured(context.Background(), job)
	if err != nil {
		t.Fatalf("PublishTraceCaptured error: %v", err)
	}
	if client.key != "analysis_jobs" {
		t.Fatalf("key = %q", client.key)
	}
	if len(client.values) != 1 {
		t.Fatalf("values = %d, want 1", len(client.values))
	}
	var decoded TraceCapturedJob
	if err := json.Unmarshal([]byte(client.values[0].(string)), &decoded); err != nil {
		t.Fatalf("job JSON error: %v", err)
	}
	if decoded.Type != "trace_captured" || decoded.TraceID != "trace_1" || decoded.EmployeeNo != "E12345" {
		t.Fatalf("job = %+v", decoded)
	}
	if decoded.ResponseRawRef != "raw/trace_1/response_body.bin" {
		t.Fatalf("ResponseRawRef = %q", decoded.ResponseRawRef)
	}
	if decoded.ModelRequested != "gpt-test" || decoded.UsageTotalTokens != 18 {
		t.Fatalf("minimal metadata = %+v", decoded)
	}
}
```

Update `TestRedisListPublisherReturnsRedisError`:

```go
func TestRedisListPublisherReturnsRedisError(t *testing.T) {
	redisErr := errors.New("redis down")
	publisher := NewRedisListPublisher(&fakeRedisListClient{err: redisErr}, "analysis_jobs")

	err := publisher.PublishTraceCaptured(context.Background(), NewTraceCaptured(TraceCapturedInput{
		TraceID:        "trace_1",
		RoutePattern:   "/v1/chat/completions",
		ProtocolFamily: "openai_chat",
		CaptureMode:    "raw_and_normalized",
		EmployeeNo:     "E12345",
	}))
	if !errors.Is(err, redisErr) {
		t.Fatalf("error = %v, want %v", err, redisErr)
	}
}
```

- [ ] **Step 2: Run the job tests and verify they fail**

Run:

```bash
go test ./internal/jobs -v
```

Expected: FAIL because `TraceCapturedInput` and the new fields are undefined.

- [ ] **Step 3: Update job models**

Modify `internal/jobs/jobs.go`:

```go
type TraceCapturedJob struct {
	Type                string `json:"type"`
	TraceID             string `json:"trace_id"`
	RoutePattern        string `json:"route_pattern"`
	ProtocolFamily      string `json:"protocol_family"`
	CaptureMode         string `json:"capture_mode"`
	EmployeeNo          string `json:"employee_no"`
	RequestRawRef       string `json:"request_raw_ref"`
	RequestHeadersRef   string `json:"request_headers_ref"`
	ResponseRawRef      string `json:"response_raw_ref"`
	ResponseHeadersRef  string `json:"response_headers_ref"`
	RequestContentType  string `json:"request_content_type"`
	ResponseContentType string `json:"response_content_type"`
	ModelRequested      string `json:"model_requested"`
	UsagePromptTokens   int    `json:"usage_prompt_tokens"`
	UsageCompletionTokens int  `json:"usage_completion_tokens"`
	UsageTotalTokens    int    `json:"usage_total_tokens"`
	UsageReasoningTokens int   `json:"usage_reasoning_tokens"`
	UsageCachedTokens   int    `json:"usage_cached_tokens"`
}

type TraceCapturedInput struct {
	TraceID               string
	RoutePattern          string
	ProtocolFamily        string
	CaptureMode           string
	EmployeeNo            string
	RequestRawRef         string
	RequestHeadersRef     string
	ResponseRawRef        string
	ResponseHeadersRef    string
	RequestContentType    string
	ResponseContentType   string
	ModelRequested        string
	UsagePromptTokens     int
	UsageCompletionTokens int
	UsageTotalTokens      int
	UsageReasoningTokens  int
	UsageCachedTokens     int
}

func NewTraceCaptured(input TraceCapturedInput) TraceCapturedJob {
	return TraceCapturedJob{
		Type:                  "trace_captured",
		TraceID:               input.TraceID,
		RoutePattern:          input.RoutePattern,
		ProtocolFamily:        input.ProtocolFamily,
		CaptureMode:           input.CaptureMode,
		EmployeeNo:            input.EmployeeNo,
		RequestRawRef:         input.RequestRawRef,
		RequestHeadersRef:     input.RequestHeadersRef,
		ResponseRawRef:        input.ResponseRawRef,
		ResponseHeadersRef:    input.ResponseHeadersRef,
		RequestContentType:    input.RequestContentType,
		ResponseContentType:   input.ResponseContentType,
		ModelRequested:        input.ModelRequested,
		UsagePromptTokens:     input.UsagePromptTokens,
		UsageCompletionTokens: input.UsageCompletionTokens,
		UsageTotalTokens:      input.UsageTotalTokens,
		UsageReasoningTokens:  input.UsageReasoningTokens,
		UsageCachedTokens:     input.UsageCachedTokens,
	}
}
```

Keep `Publisher`, `RedisListPublisher`, and `PublishTraceCaptured` unchanged.

- [ ] **Step 4: Update the Python worker contract example**

Replace `workers/analysis_worker/contract_example.json` with:

```json
{
  "type": "trace_captured",
  "trace_id": "trace_example",
  "route_pattern": "/v1/chat/completions",
  "protocol_family": "openai_chat",
  "capture_mode": "raw_and_normalized",
  "employee_no": "E12345",
  "request_raw_ref": "raw/2026/04/27/trace_example/request_body.bin",
  "request_headers_ref": "raw/2026/04/27/trace_example/request_headers.bin",
  "response_raw_ref": "raw/2026/04/27/trace_example/response_body.bin",
  "response_headers_ref": "raw/2026/04/27/trace_example/response_headers.bin",
  "request_content_type": "application/json",
  "response_content_type": "application/json",
  "model_requested": "gpt-test",
  "usage_prompt_tokens": 11,
  "usage_completion_tokens": 7,
  "usage_total_tokens": 18,
  "usage_reasoning_tokens": 2,
  "usage_cached_tokens": 3
}
```

- [ ] **Step 5: Update the Python dataclass**

Replace `workers/analysis_worker/main.py` with:

```python
import json
import sys
from dataclasses import dataclass


@dataclass(frozen=True)
class TraceCapturedJob:
    type: str
    trace_id: str
    route_pattern: str
    protocol_family: str
    capture_mode: str
    employee_no: str
    request_raw_ref: str = ""
    request_headers_ref: str = ""
    response_raw_ref: str = ""
    response_headers_ref: str = ""
    request_content_type: str = ""
    response_content_type: str = ""
    model_requested: str = ""
    usage_prompt_tokens: int = 0
    usage_completion_tokens: int = 0
    usage_total_tokens: int = 0
    usage_reasoning_tokens: int = 0
    usage_cached_tokens: int = 0


def parse_job(line: str) -> TraceCapturedJob:
    data = json.loads(line)
    known = {field: data.get(field, TraceCapturedJob.__dataclass_fields__[field].default) for field in TraceCapturedJob.__dataclass_fields__}
    return TraceCapturedJob(**known)


def main() -> int:
    payload = sys.stdin.read().strip()
    if not payload:
        return 0
    job = parse_job(payload)
    print(json.dumps({
        "accepted_trace_id": job.trace_id,
        "worker_status": "accepted",
        "response_raw_ref": job.response_raw_ref,
        "usage_total_tokens": job.usage_total_tokens
    }))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
```

- [ ] **Step 6: Run job and worker contract tests**

Run:

```bash
go test ./internal/jobs -v
cd workers/analysis_worker && uv run python main.py < contract_example.json
```

Expected: Go tests PASS and Python prints JSON containing `"accepted_trace_id": "trace_example"` and `"usage_total_tokens": 18`.

- [ ] **Step 7: Commit the enriched job contract**

Run:

```bash
git add internal/jobs/jobs.go internal/jobs/jobs_test.go workers/analysis_worker/contract_example.json workers/analysis_worker/main.py
git commit -m "feat: enrich trace captured job contract"
```

Expected: commit succeeds.

---

### Task 5: Route Registry Coverage Completion

**Files:**
- Modify: `internal/routes/registry.go`
- Modify: `internal/routes/registry_test.go`

- [ ] **Step 1: Add route coverage tests**

Append this test to `internal/routes/registry_test.go`:

```go
func TestDefaultRegistryMatchesApprovedMVPRoutes(t *testing.T) {
	tests := []struct {
		method       string
		path         string
		wantPattern  string
		wantProtocol string
		wantCapture  CaptureMode
	}{
		{"POST", "/v1/engines/text-embedding-3-small/embeddings", "/v1/engines/:model/embeddings", "embeddings", CaptureRawAndNormalized},
		{"POST", "/v1/video/generations", "/v1/video/generations", "video", CaptureRawAndMinimal},
		{"GET", "/v1/video/generations/task_123", "/v1/video/generations/:task_id", "video", CaptureRawAndMinimal},
		{"GET", "/v1/videos/task_123", "/v1/videos/:task_id", "video", CaptureRawAndMinimal},
		{"GET", "/v1/videos/task_123/content", "/v1/videos/:task_id/content", "video", CaptureRawAndMinimal},
		{"POST", "/v1/videos/video_123/remix", "/v1/videos/:video_id/remix", "video", CaptureRawAndMinimal},
		{"POST", "/kling/v1/videos/text2video", "/kling/v1/videos/text2video", "kling_video", CaptureRawAndMinimal},
		{"POST", "/kling/v1/videos/image2video", "/kling/v1/videos/image2video", "kling_video", CaptureRawAndMinimal},
		{"GET", "/kling/v1/videos/text2video/task_123", "/kling/v1/videos/text2video/:task_id", "kling_video", CaptureRawAndMinimal},
		{"GET", "/kling/v1/videos/image2video/task_123", "/kling/v1/videos/image2video/:task_id", "kling_video", CaptureRawAndMinimal},
		{"POST", "/jimeng/", "/jimeng/", "jimeng", CaptureRawAndMinimal},
		{"POST", "/relay/mj/submit/imagine", "/:mode/mj/*", "midjourney", CaptureRawAndMinimal},
		{"POST", "/suno/submit/music", "/suno/*", "suno", CaptureRawAndMinimal},
	}

	registry := DefaultRegistry()
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			entry, ok := registry.Match(tt.method, tt.path)
			if !ok {
				t.Fatalf("expected match for %s %s", tt.method, tt.path)
			}
			if entry.PathPattern != tt.wantPattern {
				t.Fatalf("PathPattern = %q, want %q", entry.PathPattern, tt.wantPattern)
			}
			if entry.ProtocolFamily != tt.wantProtocol {
				t.Fatalf("ProtocolFamily = %q, want %q", entry.ProtocolFamily, tt.wantProtocol)
			}
			if entry.CaptureMode != tt.wantCapture {
				t.Fatalf("CaptureMode = %q, want %q", entry.CaptureMode, tt.wantCapture)
			}
		})
	}
}

func TestRouteSegmentParametersRequireNonEmptySegment(t *testing.T) {
	registry := DefaultRegistry()
	if _, ok := registry.Match("GET", "/v1/videos//content"); ok {
		t.Fatal("expected no match for empty task id")
	}
	if _, ok := registry.Match("POST", "/relay/mj/"); ok {
		t.Fatal("expected no match for empty mj child path")
	}
}
```

- [ ] **Step 2: Run route tests and verify they fail**

Run:

```bash
go test ./internal/routes -v
```

Expected: FAIL because several approved MVP routes are not registered and `:segment` patterns are unsupported.

- [ ] **Step 3: Extend the registry entries**

Modify `DefaultRegistry()` in `internal/routes/registry.go` so the `entries` slice contains these additional entries before broader wildcard entries:

```go
{Method: "POST", PathPattern: "/v1/engines/:model/embeddings", ProtocolFamily: "embeddings", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "embeddings"},
{Method: "POST", PathPattern: "/v1/video/generations", ProtocolFamily: "video", BodyKind: "json_or_multipart", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
{Method: "GET", PathPattern: "/v1/video/generations/:task_id", ProtocolFamily: "video", BodyKind: "none", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
{Method: "GET", PathPattern: "/v1/videos/:task_id", ProtocolFamily: "video", BodyKind: "none", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
{Method: "GET", PathPattern: "/v1/videos/:task_id/content", ProtocolFamily: "video", BodyKind: "none", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
{Method: "POST", PathPattern: "/v1/videos/:video_id/remix", ProtocolFamily: "video", BodyKind: "json_or_multipart", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
{Method: "POST", PathPattern: "/kling/v1/videos/text2video", ProtocolFamily: "kling_video", BodyKind: "json", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
{Method: "POST", PathPattern: "/kling/v1/videos/image2video", ProtocolFamily: "kling_video", BodyKind: "json_or_multipart", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
{Method: "GET", PathPattern: "/kling/v1/videos/text2video/:task_id", ProtocolFamily: "kling_video", BodyKind: "none", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
{Method: "GET", PathPattern: "/kling/v1/videos/image2video/:task_id", ProtocolFamily: "kling_video", BodyKind: "none", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
{Method: "POST", PathPattern: "/jimeng/", ProtocolFamily: "jimeng", BodyKind: "json", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
{Method: "POST", PathPattern: "/:mode/mj/*", ProtocolFamily: "midjourney", BodyKind: "json", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
```

Keep the existing `/mj/*`, `/suno/*`, and `/v1/videos*` entries after the more specific entries.

- [ ] **Step 4: Replace path matching with segment-aware matching**

Replace `matchPath` in `internal/routes/registry.go` with:

```go
func matchPath(pattern, path string) bool {
	if pattern == path {
		return true
	}
	if strings.Contains(pattern, ":") {
		return matchSegmentPath(pattern, path)
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if strings.HasSuffix(prefix, "/") {
			return strings.HasPrefix(path, prefix) && len(path) > len(prefix)
		}
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	return false
}

func matchSegmentPath(pattern, path string) bool {
	patternSegments := splitPath(pattern)
	pathSegments := splitPath(path)
	if len(patternSegments) == 0 || len(pathSegments) == 0 {
		return false
	}
	if patternSegments[len(patternSegments)-1] == "*" {
		if len(pathSegments) < len(patternSegments) {
			return false
		}
		patternSegments = patternSegments[:len(patternSegments)-1]
		pathSegments = pathSegments[:len(patternSegments)]
	} else if len(patternSegments) != len(pathSegments) {
		return false
	}
	if len(patternSegments) != len(pathSegments) {
		return false
	}
	for i := range patternSegments {
		patternSegment := patternSegments[i]
		pathSegment := pathSegments[i]
		if pathSegment == "" {
			return false
		}
		if strings.HasPrefix(patternSegment, ":") {
			continue
		}
		if patternSegment != pathSegment {
			return false
		}
	}
	return true
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}
```

- [ ] **Step 5: Run route tests**

Run:

```bash
go test ./internal/routes -v
```

Expected: PASS.

- [ ] **Step 6: Commit route registry completion**

Run:

```bash
git add internal/routes/registry.go internal/routes/registry_test.go
git commit -m "feat: expand gateway route registry coverage"
```

Expected: commit succeeds.

---

### Task 6: Proxy Integration for Header Evidence, Metadata, and Jobs

**Files:**
- Modify: `internal/gateway/proxy.go`
- Modify: `internal/gateway/proxy_test.go`

- [ ] **Step 1: Add proxy tests for header evidence and minimal metadata**

Append this test to `internal/gateway/proxy_test.go`:

```go
func TestProxyRecordsHeaderEvidenceAndMinimalMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-abc123" {
			t.Fatalf("upstream Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Request", "upstream-1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl_test",
			"usage": {
				"prompt_tokens": 11,
				"completion_tokens": 7,
				"total_tokens": 18,
				"prompt_tokens_details": {"cached_tokens": 3},
				"completion_tokens_details": {"reasoning_tokens": 2}
			}
		}`))
	}))
	defer upstream.Close()

	repo := &memoryTraceRepo{}
	publisher := &recordingJobPublisher{}
	handler := testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir()))
	handler.JobPublisher = publisher

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[]}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(repo.traces))
	}
	trace := repo.traces[0]
	if trace.RequestHeadersRef == "" || trace.ResponseHeadersRef == "" {
		t.Fatalf("header refs were not recorded: %+v", trace)
	}
	if trace.ModelRequested != "gpt-test" {
		t.Fatalf("ModelRequested = %q", trace.ModelRequested)
	}
	if trace.UsagePromptTokens != 11 || trace.UsageCompletionTokens != 7 || trace.UsageTotalTokens != 18 {
		t.Fatalf("usage = %+v", trace)
	}
	if trace.UsageCachedTokens != 3 || trace.UsageReasoningTokens != 2 {
		t.Fatalf("usage details = %+v", trace)
	}

	var objectTypes []string
	for _, object := range repo.rawEvidence {
		objectTypes = append(objectTypes, object.ObjectType)
	}
	got := strings.Join(objectTypes, ",")
	for _, want := range []string{"request_body", "request_headers", "response_body", "response_headers"} {
		if !strings.Contains(got, want) {
			t.Fatalf("raw evidence object types = %s, missing %s", got, want)
		}
	}
	if len(publisher.jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(publisher.jobs))
	}
	job := publisher.jobs[0]
	if job.RequestHeadersRef == "" || job.ResponseHeadersRef == "" || job.ResponseRawRef == "" {
		t.Fatalf("job evidence refs missing: %+v", job)
	}
	if job.ModelRequested != "gpt-test" || job.UsageTotalTokens != 18 {
		t.Fatalf("job metadata = %+v", job)
	}
}
```

- [ ] **Step 2: Update ordering test expectation**

Modify `TestProxyPublishesTraceCapturedJobAfterRawEvidencePersistence` in `internal/gateway/proxy_test.go` so it expects header evidence rows before publish:

```go
want := []string{"trace", "raw:request_body", "raw:request_headers", "raw:response_body", "raw:response_headers", "publish"}
```

- [ ] **Step 3: Run proxy tests and verify they fail**

Run:

```bash
go test ./internal/gateway -run 'TestProxyRecordsHeaderEvidenceAndMinimalMetadata|TestProxyPublishesTraceCapturedJobAfterRawEvidencePersistence' -v
```

Expected: FAIL because proxy integration has not written header evidence, metadata, or enriched jobs.

- [ ] **Step 4: Extend traceRecord**

Modify `traceRecord` in `internal/gateway/proxy.go`:

```go
type traceRecord struct {
	traceID             string
	req                 *http.Request
	entry               routes.Entry
	statusCode          int
	upstreamCode        int
	startedAt           time.Time
	finishedAt          time.Time
	requestObject       evidence.Object
	requestHeadersObject evidence.Object
	responseObject      evidence.Object
	responseHeadersObject evidence.Object
	requestSize         int64
	responseSize        int64
	requestContentType  string
	responseContentType string
	modelRequested      string
	usage               minimalUsage
	snapshot            identity.Snapshot
	stream              bool
	unknownRoute        bool
	skipPostPersistence bool
}
```

- [ ] **Step 5: Add a helper for header evidence writes**

Add this method to `internal/gateway/proxy.go` near `putEvidence`:

```go
func (h Handler) putHeaderEvidence(ctx context.Context, traceID, objectType string, header http.Header) (evidence.Object, error) {
	data, err := headerEvidenceJSON(header)
	if err != nil {
		return evidence.Object{}, err
	}
	return h.putEvidence(ctx, traceID, objectType, "application/json", data)
}
```

- [ ] **Step 6: Record request header evidence before proxying**

In `ServeHTTP`, after `requestObject` is written, add request header evidence:

```go
auditCtx, cancelAudit = h.auditContext(req.Context())
requestHeadersObject, err := h.putHeaderEvidence(auditCtx, traceID, "request_headers", req.Header)
if err != nil {
	h.reportAuditError(auditCtx, err)
	cancelAudit()
	http.Error(w, "failed to store request header evidence", http.StatusInternalServerError)
	return
}
cancelAudit()
```

When constructing every `traceRecord` in `ServeHTTP`, set:

```go
requestHeadersObject: requestHeadersObject,
requestContentType:   capturedReq.ContentType,
modelRequested:       extractRequestModel(req.URL.Path, capturedReq.BodyBytes),
```

- [ ] **Step 7: Record non-streaming response header evidence and usage**

In the non-streaming response path, after `responseObject` is written, add:

```go
responseHeadersObject, headerErr := h.putHeaderEvidence(auditCtx, traceID, "response_headers", upstreamResp.Header)
if headerErr != nil {
	h.reportAuditError(auditCtx, headerErr)
	skipPostPersistence = true
}
usage := extractResponseUsage(responseBody)
```

In the `traceRecord` for the non-streaming path, set:

```go
responseHeadersObject: responseHeadersObject,
requestContentType:    capturedReq.ContentType,
responseContentType:   upstreamResp.Header.Get("Content-Type"),
modelRequested:        extractRequestModel(req.URL.Path, capturedReq.BodyBytes),
usage:                 usage,
```

- [ ] **Step 8: Record streaming response header evidence**

At the start of `serveStreamingResponse`, before copying the stream body, write response headers:

```go
headerCtx, cancelHeaders := h.auditContext(req.Context())
responseHeadersObject, headerErr := h.putHeaderEvidence(headerCtx, record.traceID, "response_headers", upstreamResp.Header)
cancelHeaders()
if headerErr != nil {
	h.reportAuditError(req.Context(), headerErr)
	record.skipPostPersistence = true
}
record.responseHeadersObject = responseHeadersObject
record.responseContentType = upstreamResp.Header.Get("Content-Type")
```

Keep streaming usage fields zero because SSE reconstruction belongs to the async worker.

- [ ] **Step 9: Record WebSocket handshake response header evidence**

In `serveWebSocketTunnel`, after `record.upstreamCode = upstreamResp.StatusCode` and before hijacking the client connection, add:

```go
headerCtx, cancelHeaders := h.auditContext(req.Context())
responseHeadersObject, headerErr := h.putHeaderEvidence(headerCtx, record.traceID, "response_headers", upstreamResp.Header)
cancelHeaders()
if headerErr != nil {
	h.reportAuditError(req.Context(), headerErr)
	record.skipPostPersistence = true
} else {
	record.responseHeadersObject = responseHeadersObject
}
record.responseContentType = upstreamResp.Header.Get("Content-Type")
```

This records 101 handshake headers and non-101 upstream responses without changing tunnel bytes.

- [ ] **Step 10: Insert header raw evidence rows**

In `insertTrace`, after inserting the request body raw evidence row, add:

```go
if record.requestHeadersObject.ObjectRef != "" {
	if err := h.insertEvidenceObject(ctx, record.traceID, "request_headers", record.requestHeadersObject); err != nil {
		errs = append(errs, err)
	}
}
```

After inserting the response body row, add:

```go
if record.responseHeadersObject.ObjectRef != "" {
	if err := h.insertEvidenceObject(ctx, record.traceID, "response_headers", record.responseHeadersObject); err != nil {
		errs = append(errs, err)
	}
}
```

- [ ] **Step 11: Populate trace metadata**

In `insertTrace`, set the new `traces.Trace` fields:

```go
RequestHeadersRef:        record.requestHeadersObject.ObjectRef,
ResponseHeadersRef:       record.responseHeadersObject.ObjectRef,
ModelRequested:           record.modelRequested,
UsagePromptTokens:        record.usage.PromptTokens,
UsageCompletionTokens:    record.usage.CompletionTokens,
UsageTotalTokens:         record.usage.TotalTokens,
UsageReasoningTokens:     record.usage.ReasoningTokens,
UsageCachedTokens:        record.usage.CachedTokens,
```

- [ ] **Step 12: Publish enriched jobs**

Replace the `jobs.NewTraceCaptured(...)` call in `insertTrace` with:

```go
job := jobs.NewTraceCaptured(jobs.TraceCapturedInput{
	TraceID:               record.traceID,
	RoutePattern:          record.entry.PathPattern,
	ProtocolFamily:        record.entry.ProtocolFamily,
	CaptureMode:           string(record.entry.CaptureMode),
	EmployeeNo:            record.snapshot.EmployeeNo,
	RequestRawRef:         record.requestObject.ObjectRef,
	RequestHeadersRef:     record.requestHeadersObject.ObjectRef,
	ResponseRawRef:        record.responseObject.ObjectRef,
	ResponseHeadersRef:    record.responseHeadersObject.ObjectRef,
	RequestContentType:    record.requestContentType,
	ResponseContentType:   record.responseContentType,
	ModelRequested:        record.modelRequested,
	UsagePromptTokens:     record.usage.PromptTokens,
	UsageCompletionTokens: record.usage.CompletionTokens,
	UsageTotalTokens:      record.usage.TotalTokens,
	UsageReasoningTokens:  record.usage.ReasoningTokens,
	UsageCachedTokens:     record.usage.CachedTokens,
})
```

- [ ] **Step 13: Run proxy tests**

Run:

```bash
go test ./internal/gateway -v
```

Expected: PASS.

- [ ] **Step 14: Commit proxy integration**

Run:

```bash
git add internal/gateway/proxy.go internal/gateway/proxy_test.go
git commit -m "feat: capture headers and minimal metadata"
```

Expected: commit succeeds.

---

### Task 7: Development Docs and Full Verification

**Files:**
- Modify: `docs/development.md`

- [ ] **Step 1: Update development documentation**

Add this section to `docs/development.md` after the Gateway Environment section:

```markdown
## Evidence and Analysis Jobs

The gateway stores request body, response body, request headers, and response headers as raw evidence objects. Header evidence is JSON and redacts API-key-bearing headers before writing to storage.

The Redis `analysis_jobs` list receives `trace_captured` envelopes only after the trace row and raw evidence rows are persisted. Job envelopes include evidence refs, content types, requested model, and token usage fields when the gateway can extract them from non-streaming JSON responses.
```

- [ ] **Step 2: Run Go formatting**

Run:

```bash
gofmt -w internal
```

Expected: no output.

- [ ] **Step 3: Run the full Go test suite**

Run:

```bash
go test ./...
```

Expected: PASS for all packages.

- [ ] **Step 4: Run the Python worker contract**

Run:

```bash
cd workers/analysis_worker && uv run python main.py < contract_example.json
```

Expected: output JSON includes `"accepted_trace_id": "trace_example"` and `"usage_total_tokens": 18`.

- [ ] **Step 5: Commit docs and verification cleanup**

Run:

```bash
git add docs/development.md internal workers/analysis_worker
git commit -m "docs: describe evidence and analysis job metadata"
```

Expected: commit succeeds if Step 1 or formatting changed files. If there are no changes after Step 4, skip the commit and record that the branch is already clean.

---

## Self-Review

**Spec coverage:** This plan covers gateway raw evidence expansion, route coverage alerts support through richer registry entries, minimal model and usage extraction, trace metadata persistence, and job handoff enrichment. It does not cover Admin API, dashboards, RBAC, full Python normalization, or anomaly detectors because those are separate subsystems.

**Placeholder scan:** The plan contains concrete file paths, test code, implementation code, commands, and expected outcomes for every task.

**Type consistency:** `TraceCapturedInput` feeds `NewTraceCaptured`, which returns `TraceCapturedJob`. `traceRecord.usage` uses `minimalUsage`, and `insertTrace` maps its fields to both `traces.Trace` and `jobs.TraceCapturedInput`. `RequestHeadersRef` and `ResponseHeadersRef` are consistently named in traces, jobs, migration columns, and tests.
