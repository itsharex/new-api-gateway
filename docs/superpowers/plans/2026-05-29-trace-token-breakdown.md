# Trace Token Breakdown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show new trace token usage as separate input, output, cached, and total values in admin trace and usage views.

**Architecture:** Reuse the existing split-token storage and Go-to-Python contract. Fill small extractor gaps, expose split fields through admin repository models, and render them in the bundled admin UI. Do not add migrations, historical backfill, or new-api log reconciliation.

**Tech Stack:** Go 1.x, PostgreSQL via pgx, vanilla admin UI JavaScript, Python worker with pytest/uv.

---

## File Structure

- `internal/gateway/usage_openai_images.go`: parse cached-token detail when an OpenAI Images response includes `usage.input_tokens_details.cached_tokens`.
- `internal/gateway/usage_openai_images_test.go`: verify Images cached-token extraction for non-stream and SSE paths.
- `internal/gateway/usage_generic.go`: parse common OpenAI-compatible `input_tokens`/`output_tokens` and cached/reasoning details in generic responses.
- `internal/gateway/usage_generic_test.go`: verify generic input/output/cached/reasoning extraction without changing the existing empty-response behavior.
- `internal/admin/models.go`: add split token JSON fields to `TraceSummary` and `UsageBucket`.
- `internal/admin/repository.go`: select and scan split token fields in `ListTraces`, `ListUsageAggregates`, and `GetTraceDetail`.
- `internal/admin/repository_test.go`: verify repository SQL includes split fields and scans representative values.
- `internal/admin/handlers_test.go`: verify trace detail JSON contains split fields and update the in-memory fake scanner.
- `internal/adminui/app.js`: render input/output/cached/total columns and trace detail metadata.
- `workers/analysis_worker/tests/test_pipeline.py`: assert aggregate deltas keep prompt, completion, cached, and total tokens.
- `ARCHITECTURE.md`: update the trace/admin description if it still implies token usage is only a total.

## Task 1: Gateway Extractor Cached Token Gaps

**Files:**
- Modify: `internal/gateway/usage_openai_images_test.go`
- Modify: `internal/gateway/usage_openai_images.go`
- Modify: `internal/gateway/usage_generic_test.go`
- Modify: `internal/gateway/usage_generic.go`

- [ ] **Step 1: Write failing Images extractor tests**

In `internal/gateway/usage_openai_images_test.go`, replace `TestOpenAIImagesExtractResponse` with:

```go
func TestOpenAIImagesExtractResponse(t *testing.T) {
	e := newOpenAIImagesExtractor()
	body := []byte(`{"created":0,"data":[{"b64_json":"...","url":"..."}],"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300,"input_tokens_details":{"cached_tokens":40,"image_tokens":20,"text_tokens":80},"output_tokens_details":{"image_tokens":180,"text_tokens":20}}}`)
	u, m := e.extractResponse(body)
	if u.PromptTokens != 100 {
		t.Fatalf("PromptTokens=%d, want 100", u.PromptTokens)
	}
	if u.CompletionTokens != 200 {
		t.Fatalf("CompletionTokens=%d, want 200", u.CompletionTokens)
	}
	if u.TotalTokens != 300 {
		t.Fatalf("TotalTokens=%d, want 300", u.TotalTokens)
	}
	if u.CachedTokens != 40 {
		t.Fatalf("CachedTokens=%d, want 40", u.CachedTokens)
	}
	_ = m // images response has no model field
}

func TestOpenAIImagesProcessSSECachedTokens(t *testing.T) {
	e := newOpenAIImagesExtractor()
	e.processSSE([]byte(`{"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300,"input_tokens_details":{"cached_tokens":40}}}`))
	u, _ := e.sseResult()
	if u.PromptTokens != 100 || u.CompletionTokens != 200 || u.TotalTokens != 300 {
		t.Fatalf("usage=%+v", u)
	}
	if u.CachedTokens != 40 {
		t.Fatalf("CachedTokens=%d, want 40", u.CachedTokens)
	}
}
```

- [ ] **Step 2: Run Images extractor tests and verify failure**

Run:

```bash
go test ./internal/gateway/ -run 'TestOpenAIImages(ExtractResponse|ProcessSSECachedTokens)' -count=1
```

Expected: FAIL because `CachedTokens` is `0`.

- [ ] **Step 3: Implement Images cached-token parsing**

In `internal/gateway/usage_openai_images.go`, update both anonymous `Usage` structs to include `InputDetails`, and set `CachedTokens`.

Use this shape in both `processSSE` and `extractResponse`:

```go
Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	InputDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
} `json:"usage"`
```

In `processSSE`, set:

```go
e.acc = minimalUsage{
	PromptTokens:     v.Usage.InputTokens,
	CompletionTokens: v.Usage.OutputTokens,
	TotalTokens:      v.Usage.TotalTokens,
	CachedTokens:     v.Usage.InputDetails.CachedTokens,
}
```

In `extractResponse`, return:

```go
return minimalUsage{
	PromptTokens:     v.Usage.InputTokens,
	CompletionTokens: v.Usage.OutputTokens,
	TotalTokens:      v.Usage.TotalTokens,
	CachedTokens:     v.Usage.InputDetails.CachedTokens,
}, ""
```

- [ ] **Step 4: Run Images extractor tests and verify pass**

Run:

```bash
go test ./internal/gateway/ -run 'TestOpenAIImages(ExtractResponse|ProcessSSECachedTokens)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing generic extractor test**

In `internal/gateway/usage_generic_test.go`, add:

```go
func TestGenericExtractorExtractResponseOpenAICompatibleDetails(t *testing.T) {
	g := newGenericExtractor()
	u, m := g.extractResponse([]byte(`{"model":"x","usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":2}}}`))
	if u.PromptTokens != 5 || u.CompletionTokens != 3 || u.TotalTokens != 8 {
		t.Fatalf("usage=%+v", u)
	}
	if u.CachedTokens != 4 {
		t.Fatalf("CachedTokens=%d, want 4", u.CachedTokens)
	}
	if u.ReasoningTokens != 2 {
		t.Fatalf("ReasoningTokens=%d, want 2", u.ReasoningTokens)
	}
	if m != "x" {
		t.Fatalf("model=%q, want x", m)
	}
}
```

- [ ] **Step 6: Run generic extractor tests and verify failure**

Run:

```bash
go test ./internal/gateway/ -run TestGenericExtractorExtractResponseOpenAICompatibleDetails -count=1
```

Expected: FAIL because `PromptTokens`, `CompletionTokens`, `CachedTokens`, and `ReasoningTokens` are not fully parsed from OpenAI-compatible detail fields.

- [ ] **Step 7: Implement generic OpenAI-compatible detail parsing**

In `internal/gateway/usage_generic.go`, replace the `payload` struct and `u := minimalUsage{...}` block inside `extractResponse` with:

```go
var payload struct {
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		InputTokens      int `json:"input_tokens"`
		OutputTokens     int `json:"output_tokens"`
		PromptDetails    struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		InputDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		CompletionDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
		OutputDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
	Model string `json:"model"`
}
if json.Unmarshal(body, &payload) != nil {
	return minimalUsage{}, ""
}
promptTokens := payload.Usage.PromptTokens
if promptTokens == 0 {
	promptTokens = payload.Usage.InputTokens
}
completionTokens := payload.Usage.CompletionTokens
if completionTokens == 0 {
	completionTokens = payload.Usage.OutputTokens
}
cachedTokens := payload.Usage.PromptDetails.CachedTokens
if cachedTokens == 0 {
	cachedTokens = payload.Usage.InputDetails.CachedTokens
}
reasoningTokens := payload.Usage.CompletionDetails.ReasoningTokens
if reasoningTokens == 0 {
	reasoningTokens = payload.Usage.OutputDetails.ReasoningTokens
}
u := minimalUsage{
	PromptTokens:     promptTokens,
	CompletionTokens: completionTokens,
	TotalTokens:      payload.Usage.TotalTokens,
	ReasoningTokens:  reasoningTokens,
	CachedTokens:     cachedTokens,
}
```

- [ ] **Step 8: Run gateway extractor tests**

Run:

```bash
go test ./internal/gateway/ -run 'Test(OpenAIImages|GenericExtractor)' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit extractor changes**

Run:

```bash
git add internal/gateway/usage_openai_images.go internal/gateway/usage_openai_images_test.go internal/gateway/usage_generic.go internal/gateway/usage_generic_test.go
git commit -m "feat(gateway): capture split token details"
```

Expected: commit succeeds.

## Task 2: Admin Repository Models and SQL

**Files:**
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/repository.go`
- Modify: `internal/admin/repository_test.go`

- [ ] **Step 1: Write failing repository SQL assertions**

In `internal/admin/repository_test.go`, extend `TestRepositoryListTracesBuildsBoundedQuery` after the `FROM traces` assertion:

```go
	for _, column := range []string{"t.usage_prompt_tokens", "t.usage_completion_tokens", "t.usage_cached_tokens", "t.usage_total_tokens"} {
		if !strings.Contains(db.querySQL, column) {
			t.Fatalf("query missing %s: %s", column, db.querySQL)
		}
	}
```

Extend `TestRepositoryListUsageAggregatesCapsLimitAndBindsFilters` after the `FROM usage_aggregates` assertion:

```go
	for _, column := range []string{"prompt_tokens", "completion_tokens", "cached_tokens", "total_tokens"} {
		if !strings.Contains(db.querySQL, column) {
			t.Fatalf("query missing %s: %s", column, db.querySQL)
		}
	}
```

- [ ] **Step 2: Run repository tests and verify failure**

Run:

```bash
go test ./internal/admin/ -run 'TestRepository(ListTracesBuildsBoundedQuery|ListUsageAggregatesCapsLimitAndBindsFilters)' -count=1
```

Expected: FAIL because the queries do not include all split token columns.

- [ ] **Step 3: Add split token fields to admin models**

In `internal/admin/models.go`, replace the token part of `TraceSummary` with:

```go
	ModelRequested        string `json:"model_requested"`
	UsagePromptTokens     int    `json:"usage_prompt_tokens"`
	UsageCompletionTokens int    `json:"usage_completion_tokens"`
	UsageCachedTokens     int    `json:"usage_cached_tokens"`
	UsageTotalTokens      int    `json:"usage_total_tokens"`
	CreatedAt             string `json:"created_at"`
```

In the same file, replace the token part of `UsageBucket` with:

```go
	ErrorCount       int64  `json:"error_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	EstimatedCost    string `json:"estimated_cost"`
```

- [ ] **Step 4: Update `ListTraces` SQL and scan order**

In `internal/admin/repository.go`, update the `ListTraces` SELECT projection to:

```sql
SELECT t.trace_id, t.method, t.path, t.route_pattern, t.protocol_family, t.status_code,
       t.username_snapshot, t.fingerprint_display, t.model_requested,
       t.usage_prompt_tokens, t.usage_completion_tokens, t.usage_cached_tokens, t.usage_total_tokens,
       t.created_at::text,
       EXISTS(SELECT 1 FROM analysis_results WHERE trace_id = t.trace_id AND severity = 'review') AS needs_review
FROM traces t
```

Update the corresponding `rows.Scan` arguments to:

```go
&trace.TraceID, &trace.Method, &trace.Path, &trace.RoutePattern, &trace.ProtocolFamily,
&trace.StatusCode, &trace.Username, &trace.FingerprintDisplay, &trace.ModelRequested,
&trace.UsagePromptTokens, &trace.UsageCompletionTokens, &trace.UsageCachedTokens,
&trace.UsageTotalTokens, &trace.CreatedAt, &trace.NeedsReview,
```

- [ ] **Step 5: Update `ListUsageAggregates` SQL and scan order**

In `internal/admin/repository.go`, update the `ListUsageAggregates` SELECT projection to:

```sql
SELECT bucket_start::text, bucket_size, username, token_name_snapshot, model, route_pattern,
       request_count, success_count, error_count,
       prompt_tokens, completion_tokens, cached_tokens, total_tokens, estimated_cost
FROM usage_aggregates
```

Update the corresponding `rows.Scan` arguments to:

```go
&item.BucketStart,
&item.BucketSize,
&item.Username,
&item.FingerprintDisplay,
&item.Model,
&item.RoutePattern,
&item.RequestCount,
&item.SuccessCount,
&item.ErrorCount,
&item.PromptTokens,
&item.CompletionTokens,
&item.CachedTokens,
&item.TotalTokens,
&item.EstimatedCost,
```

- [ ] **Step 6: Update `GetTraceDetail` SQL and scan order**

In `internal/admin/repository.go`, update the `GetTraceDetail` SELECT projection to:

```sql
SELECT trace_id, method, path, route_pattern, protocol_family, status_code,
       username_snapshot, fingerprint_display, model_requested,
       usage_prompt_tokens, usage_completion_tokens, usage_cached_tokens, usage_total_tokens,
       created_at::text, request_raw_ref, response_raw_ref, request_headers_ref,
       response_headers_ref, identity_resolution_status, analysis_status
FROM traces
```

Update the corresponding `Scan` arguments to:

```go
&detail.TraceID,
&detail.Method,
&detail.Path,
&detail.RoutePattern,
&detail.ProtocolFamily,
&detail.StatusCode,
&detail.Username,
&detail.FingerprintDisplay,
&detail.ModelRequested,
&detail.UsagePromptTokens,
&detail.UsageCompletionTokens,
&detail.UsageCachedTokens,
&detail.UsageTotalTokens,
&detail.CreatedAt,
&detail.RequestRawRef,
&detail.ResponseRawRef,
&detail.RequestHeadersRef,
&detail.ResponseHeadersRef,
&detail.IdentityResolutionStatus,
&detail.AnalysisStatus,
```

- [ ] **Step 7: Update repository detail scan test**

In `TestRepositoryGetTraceDetailScansMessagesAndAnalysisResults`, update the first `scanFuncRow` assignments after model to:

```go
				*(dest[9].(*int)) = 111
				*(dest[10].(*int)) = 222
				*(dest[11].(*int)) = 33
				*(dest[12].(*int)) = 321
				*(dest[13].(*string)) = "2026-04-28 10:00:00+00"
				*(dest[14].(*string)) = "raw/request.json"
				*(dest[15].(*string)) = "raw/response.json"
				*(dest[16].(*string)) = "raw/request-headers.json"
				*(dest[17].(*string)) = "raw/response-headers.json"
				*(dest[18].(*string)) = "resolved"
				*(dest[19].(*string)) = "complete"
```

After the representative fields assertion, add:

```go
	if detail.UsagePromptTokens != 111 || detail.UsageCompletionTokens != 222 || detail.UsageCachedTokens != 33 || detail.UsageTotalTokens != 321 {
		t.Fatalf("usage fields = %#v", detail.TraceSummary)
	}
```

- [ ] **Step 8: Run admin repository tests**

Run:

```bash
go test ./internal/admin/ -run 'TestRepository(ListTracesBuildsBoundedQuery|ListUsageAggregatesCapsLimitAndBindsFilters|GetTraceDetailScansMessagesAndAnalysisResults)' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit admin repository changes**

Run:

```bash
git add internal/admin/models.go internal/admin/repository.go internal/admin/repository_test.go
git commit -m "feat(admin): expose split token fields"
```

Expected: commit succeeds.

## Task 3: Admin Handler JSON Coverage

**Files:**
- Modify: `internal/admin/handlers_test.go`

- [ ] **Step 1: Write failing trace detail JSON assertion**

In `TestTraceDetailIncludesRawRefsForRawEvidenceRoles`, after decoding `body`, add:

```go
			if body.Trace.UsagePromptTokens != 12 || body.Trace.UsageCompletionTokens != 23 ||
				body.Trace.UsageCachedTokens != 7 || body.Trace.UsageTotalTokens != 42 {
				t.Fatalf("usage fields = %#v", body.Trace.TraceSummary)
			}
```

- [ ] **Step 2: Run handler test and verify failure**

Run:

```bash
go test ./internal/admin/ -run TestTraceDetailIncludesRawRefsForRawEvidenceRoles -count=1
```

Expected: FAIL because `traceDetailWithRawRefs` and the memory scanner do not yet supply the new split fields.

- [ ] **Step 3: Update trace detail fixture**

In `traceDetailWithRawRefs`, replace the token field assignment with:

```go
			UsagePromptTokens:     12,
			UsageCompletionTokens: 23,
			UsageCachedTokens:     7,
			UsageTotalTokens:      42,
```

- [ ] **Step 4: Update memory admin trace scanner**

In `memoryAdminRow.Scan`, inside the branch:

```go
if strings.Contains(r.sql, "request_raw_ref") && strings.Contains(r.sql, "FROM traces") {
```

replace the destination assignments after `ModelRequested` with:

```go
		*(dest[9].(*int)) = detail.UsagePromptTokens
		*(dest[10].(*int)) = detail.UsageCompletionTokens
		*(dest[11].(*int)) = detail.UsageCachedTokens
		*(dest[12].(*int)) = detail.UsageTotalTokens
		*(dest[13].(*string)) = detail.CreatedAt
		*(dest[14].(*string)) = detail.RequestRawRef
		*(dest[15].(*string)) = detail.ResponseRawRef
		*(dest[16].(*string)) = detail.RequestHeadersRef
		*(dest[17].(*string)) = detail.ResponseHeadersRef
		*(dest[18].(*string)) = detail.IdentityResolutionStatus
		*(dest[19].(*string)) = detail.AnalysisStatus
```

- [ ] **Step 5: Run handler tests**

Run:

```bash
go test ./internal/admin/ -run 'TestTraceDetail(IncludesRawRefsForRawEvidenceRoles|RedactsRawRefsForAuditor)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit handler test changes**

Run:

```bash
git add internal/admin/handlers_test.go
git commit -m "test(admin): cover split token JSON"
```

Expected: commit succeeds.

## Task 4: Admin UI Split Token Rendering

**Files:**
- Modify: `internal/adminui/app.js`

- [ ] **Step 1: Update usage page rendering**

In `renderUsage`, replace the rows mapping and table header with:

```js
  const rows = arrayValue(body.usage).map((item) => [
    formatTime(item.bucket_start),
    item.username || item.fingerprint_display,
    item.model,
    item.route_pattern,
    formatNumber(item.request_count),
    formatNumber(item.prompt_tokens),
    formatNumber(item.completion_tokens),
    formatNumber(item.cached_tokens),
    formatNumber(item.total_tokens),
    money(item.estimated_cost),
  ]);
  renderShell(page("用量", `<section class="panel">${table(["时间 (UTC+8)", "员工", "Model", "Route", "请求数", "Input", "Output", "Cached", "Total", "费用"], rows)}</section>`));
```

- [ ] **Step 2: Update trace list rendering**

In `renderTraces`, replace the rows mapping and table header with:

```js
  const rows = arrayValue(body.traces).map((trace) => [
    safeHTML(traceButton(trace.trace_id).html + (trace.needs_review ? badge("review").html : "")),
    formatTime(trace.created_at),
    trace.username || trace.fingerprint_display,
    trace.model_requested,
    trace.route_pattern || trace.path,
    trace.status_code,
    formatNumber(trace.usage_prompt_tokens),
    formatNumber(trace.usage_completion_tokens),
    formatNumber(trace.usage_cached_tokens),
    formatNumber(trace.usage_total_tokens),
  ]);
  renderShell(page("Trace", `<section class="panel">${table(["Trace", "时间 (UTC+8)", "员工", "Model", "Route", "Status", "Input", "Output", "Cached", "Total"], rows)}</section>`));
```

- [ ] **Step 3: Update trace detail metadata rendering**

In `renderTraceDetail`, replace:

```js
    ["Token", formatNumber(trace.usage_total_tokens)],
```

with:

```js
    ["Input Token", formatNumber(trace.usage_prompt_tokens)],
    ["Output Token", formatNumber(trace.usage_completion_tokens)],
    ["Cached Token", formatNumber(trace.usage_cached_tokens)],
    ["Total Token", formatNumber(trace.usage_total_tokens)],
```

- [ ] **Step 4: Verify JavaScript syntax**

Run:

```bash
node --check internal/adminui/app.js
```

Expected: PASS with no syntax errors.

- [ ] **Step 5: Commit admin UI changes**

Run:

```bash
git add internal/adminui/app.js
git commit -m "feat(adminui): render split token usage"
```

Expected: commit succeeds.

## Task 5: Worker Assertion, Documentation, and Full Verification

**Files:**
- Modify: `workers/analysis_worker/tests/test_pipeline.py`
- Modify: `ARCHITECTURE.md`

- [ ] **Step 1: Strengthen worker aggregate assertion**

In `workers/analysis_worker/tests/test_pipeline.py`, inside `test_process_job_line_reads_evidence_normalizes_and_persists`, replace:

```python
    assert repo.aggregates[0].total_tokens == 18
```

with:

```python
    assert repo.aggregates[0].prompt_tokens == 11
    assert repo.aggregates[0].completion_tokens == 7
    assert repo.aggregates[0].cached_tokens == 3
    assert repo.aggregates[0].total_tokens == 18
```

- [ ] **Step 2: Run worker pipeline test**

Run:

```bash
cd workers/analysis_worker && uv run pytest -q tests/test_pipeline.py::test_process_job_line_reads_evidence_normalizes_and_persists
```

Expected: PASS.

- [ ] **Step 3: Update architecture token wording**

In `ARCHITECTURE.md`, update the row for `minimal.go` from:

```markdown
| `minimal.go` | 从请求/响应提取模型名称和 token 用量（支持 OpenAI/Anthropic/Gemini 格式） |
```

to:

```markdown
| `minimal.go` | 从请求/响应提取模型名称和 token 用量，记录 input/output/total 以及上游明确报告的 cached/reasoning token（支持 OpenAI/Anthropic/Gemini 格式） |
```

Update the `internal/traces/` section sentence from:

```markdown
`Trace` 结构体包含 50+ 字段：trace ID、方法、路径、状态码、耗时、token 用量、身份快照、模型信息、错误信息、所有证据引用。
```

to:

```markdown
`Trace` 结构体包含 50+ 字段：trace ID、方法、路径、状态码、耗时、input/output/cached/total token 用量、身份快照、模型信息、错误信息、所有证据引用。
```

- [ ] **Step 4: Run focused Go tests**

Run:

```bash
go test ./internal/gateway/ ./internal/admin/ ./internal/jobs/ ./internal/traces/ -count=1
```

Expected: PASS.

- [ ] **Step 5: Run full test suite**

Run:

```bash
make test
```

Expected: PASS.

- [ ] **Step 6: Check worktree status**

Run:

```bash
git status --short
```

Expected: only files changed by this task set are listed before the final commit.

- [ ] **Step 7: Commit worker/docs changes**

Run:

```bash
git add workers/analysis_worker/tests/test_pipeline.py ARCHITECTURE.md
git commit -m "docs: describe split token usage"
```

Expected: commit succeeds.

## Self-Review Notes

Spec coverage:

- Admin trace list, trace detail, and usage view split display are covered by Tasks 2, 3, and 4.
- Cached token source remains response usage only; Task 1 adds parsing only for stable explicit fields.
- No migration, backfill, or new-api log query appears in any task.
- Worker aggregation is not redesigned; Task 5 only strengthens an existing assertion.
- Documentation check is covered by Task 5.

Placeholder scan:

- This plan contains no deferred implementation sections.
- Each code-changing step gives concrete code or exact replacement text.

Type consistency:

- Go admin fields use `UsagePromptTokens`, `UsageCompletionTokens`, `UsageCachedTokens`, and `UsageTotalTokens`.
- JSON fields use `usage_prompt_tokens`, `usage_completion_tokens`, `usage_cached_tokens`, and `usage_total_tokens`.
- Usage aggregate JSON fields use `prompt_tokens`, `completion_tokens`, `cached_tokens`, and `total_tokens`.
