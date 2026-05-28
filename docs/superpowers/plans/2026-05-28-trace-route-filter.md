# Trace Route Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Only proxy and trace requests that match registered model API routes; return 404 for everything else.

**Architecture:** In `gateway/proxy.go ServeHTTP()`, add an early return when `Registry.Match()` fails, sending an OpenAI-style 404 JSON response. Remove two tests that exercised the now-deleted "unknown route" proxy+alert behavior and replace with a test asserting the 404 response. Add route documentation comments and a README table.

**Tech Stack:** Go 1.22+, standard library only

---

### Task 1: Add 404 rejection for unmatched routes

**Files:**
- Modify: `internal/gateway/proxy.go:61-74`

- [ ] **Step 1: Write the failing test**

Add to `internal/gateway/proxy_test.go` after the existing tests:

```go
func TestProxyRejectsUnmatchedRouteWith404(t *testing.T) {
	handler := testHandler("https://upstream.test", &memoryTraceRepo{}, evidence.NewFilesystemStore(t.TempDir()))

	for _, tc := range []struct {
		method string
		path   string
	}{
		{"GET", "/panel"},
		{"GET", "/api/home_page_content"},
		{"POST", "/api/user/login"},
		{"GET", "/favicon.ico"},
		{"GET", "/static/app.js"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer sk-abc123")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			errObj, ok := body["error"].(map[string]interface{})
			if !ok {
				t.Fatalf("response has no error object: %v", body)
			}
			if code, _ := errObj["code"].(float64); code != 404 {
				t.Fatalf("error.code = %v, want 404", code)
			}
			if errType, _ := errObj["type"].(string); errType != "not_found" {
				t.Fatalf("error.type = %q, want not_found", errType)
			}
			msg, _ := errObj["message"].(string)
			if !strings.Contains(msg, tc.method) || !strings.Contains(msg, tc.path) {
				t.Fatalf("error.message = %q, want it to contain method and path", msg)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run TestProxyRejectsUnmatchedRouteWith404 -v`
Expected: FAIL — unmatched routes are still proxied (status 200 or 502 depending on upstream).

- [ ] **Step 3: Write minimal implementation**

In `internal/gateway/proxy.go`, add the `writeRouteNotFound` helper and the early return in `ServeHTTP`:

Add after the import block (e.g. after line 32):

```go
func writeRouteNotFound(w http.ResponseWriter, method, path string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"message": fmt.Sprintf("unknown route: %s %s", method, path),
			"type":    "not_found",
			"code":    404,
		},
	})
}
```

In `ServeHTTP()`, replace lines 65-74:

```go
	// Before:
	entry, ok := h.Registry.Match(req.Method, req.URL.Path)
	unknownRoute := !ok
	if !ok {
		entry = routes.Entry{
			Method:         req.Method,
			PathPattern:    req.URL.Path,
			ProtocolFamily: "unknown",
			CaptureMode:    routes.CaptureRawOnly,
		}
	}

	// After:
	entry, ok := h.Registry.Match(req.Method, req.URL.Path)
	if !ok {
		writeRouteNotFound(w, req.Method, req.URL.Path)
		return
	}
```

Also add `"encoding/json"` to the imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway/ -run TestProxyRejectsUnmatchedRouteWith404 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/proxy.go internal/gateway/proxy_test.go
git commit -m "feat(gateway): reject unmatched routes with 404 instead of proxying"
```

---

### Task 2: Update existing tests broken by the route filter

**Files:**
- Modify: `internal/gateway/proxy_test.go:806-865`

Two existing tests send requests to unregistered paths (`/unmapped/provider/task`) and expect the proxy to forward them and emit coverage alerts. After Task 1, these paths get 404 before reaching the proxy logic. Remove both tests — the new `TestProxyRejectsUnmatchedRouteWith404` covers the correct behavior.

- [ ] **Step 1: Remove the two broken tests**

Delete `TestProxyEmitsCoverageAlertForUnknownRoute` (lines ~806-840) and `TestProxyEmitsCoverageAlertForUnknownRouteWhenUpstreamFails` (lines ~842-865) from `internal/gateway/proxy_test.go`.

- [ ] **Step 2: Run full gateway test suite**

Run: `go test ./internal/gateway/ -v`
Expected: All tests PASS, 0 failures.

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/proxy_test.go
git commit -m "test(gateway): remove tests for deleted unknown-route proxy behavior"
```

---

### Task 3: Add route documentation comments in registry.go

**Files:**
- Modify: `internal/routes/registry.go:28-63`

- [ ] **Step 1: Add comments to each route entry**

Add a comment above each `{Method: ...}` entry in `DefaultRegistry()`. Example for the first few:

```go
func DefaultRegistry() Registry {
	return Registry{entries: []Entry{
		// OpenAI Chat Completions (incl. compatible path /pg/)
		{Method: "POST", PathPattern: "/v1/chat/completions", ProtocolFamily: "openai_chat", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_chat"},
		{Method: "POST", PathPattern: "/pg/chat/completions", ProtocolFamily: "openai_chat", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_chat"},
		// OpenAI Responses API
		{Method: "POST", PathPattern: "/v1/responses", ProtocolFamily: "openai_responses", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_responses"},
		{Method: "POST", PathPattern: "/v1/responses/compact", ProtocolFamily: "openai_responses", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_responses_compact"},
		// Anthropic Claude Messages
		{Method: "POST", PathPattern: "/v1/messages", ProtocolFamily: "claude_messages", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "claude_messages"},
		// OpenAI Legacy Completions
		{Method: "POST", PathPattern: "/v1/completions", ProtocolFamily: "openai_completions", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_completions"},
		// Embeddings
		{Method: "POST", PathPattern: "/v1/embeddings", ProtocolFamily: "embeddings", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "embeddings"},
		// Rerank
		{Method: "POST", PathPattern: "/v1/rerank", ProtocolFamily: "rerank", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "rerank"},
		// OpenAI Image Generations & Edits
		{Method: "POST", PathPattern: "/v1/images/generations", ...},
		{Method: "POST", PathPattern: "/v1/images/edits", ...},
		{Method: "POST", PathPattern: "/v1/edits", ...},
		// OpenAI Audio (Transcription, Translation, TTS)
		{Method: "POST", PathPattern: "/v1/audio/transcriptions", ...},
		{Method: "POST", PathPattern: "/v1/audio/translations", ...},
		{Method: "POST", PathPattern: "/v1/audio/speech", ...},
		// Google Gemini Generate Content
		{Method: "POST", PathPattern: "/v1beta/models/*", ...},
		{Method: "POST", PathPattern: "/v1/models/*", ...},
		// OpenAI Realtime WebSocket
		{Method: "GET", PathPattern: "/v1/realtime", ...},
		// Legacy Engine Embeddings
		{Method: "POST", PathPattern: "/v1/engines/:model/embeddings", ...},
		// Video Generation & Polling (generic, Kling, Midjourney, Suno)
		...
		// Midjourney
		...
		// Suno
		...
	}}
}
```

Add one comment line per logical group (not per entry). Group related routes together.

- [ ] **Step 2: Run tests to verify nothing broke**

Run: `go test ./internal/routes/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/routes/registry.go
git commit -m "docs(routes): add protocol-family comments to registered routes"
```

---

### Task 4: Add supported routes table to README

**Files:**
- Modify: `README.md` — insert new section after "架构概览" (after line ~56, before "快速开始")

- [ ] **Step 1: Add the routes section**

Insert between the "分析 Worker 流程" section end and "快速开始":

```markdown
## 中转路由清单

网关仅代理以下已注册的模型 API 路由，非模型请求（管理后台、静态资源等）返回 `404`。

| 方法 | 路径模式 | 协议族 | 说明 |
|------|---------|--------|------|
| POST | `/v1/chat/completions` | openai_chat | OpenAI Chat Completions |
| POST | `/pg/chat/completions` | openai_chat | OpenAI Chat Completions（兼容路径） |
| POST | `/v1/responses` | openai_responses | OpenAI Responses API |
| POST | `/v1/responses/compact` | openai_responses | OpenAI Responses API (compact) |
| POST | `/v1/messages` | claude_messages | Anthropic Claude Messages |
| POST | `/v1/completions` | openai_completions | OpenAI Legacy Completions |
| POST | `/v1/embeddings` | embeddings | Embeddings |
| POST | `/v1/engines/:model/embeddings` | embeddings | Legacy Engine Embeddings |
| POST | `/v1/rerank` | rerank | Rerank |
| POST | `/v1/images/generations` | openai_images | Image Generations |
| POST | `/v1/images/edits` | openai_images | Image Edits |
| POST | `/v1/edits` | openai_images | Legacy Edits |
| POST | `/v1/audio/transcriptions` | openai_audio | Audio Transcription |
| POST | `/v1/audio/translations` | openai_audio | Audio Translation |
| POST | `/v1/audio/speech` | openai_audio | Text-to-Speech |
| POST | `/v1beta/models/*` | gemini | Google Gemini |
| POST | `/v1/models/*` | gemini | Google Gemini (v1) |
| GET | `/v1/realtime` | realtime | Realtime WebSocket |
| POST | `/v1/video/generations` | video | Video Generation |
| GET | `/v1/video/generations/:task_id` | video | Video Polling |
| GET | `/v1/videos/:task_id` | video | Video Polling (alt) |
| GET | `/v1/videos/:task_id/content` | video | Video Content Download |
| POST | `/v1/videos/:video_id/remix` | video | Video Remix |
| POST | `/v1/videos*` | video | Video (wildcard) |
| POST | `/kling/v1/videos/text2video` | kling_video | Kling Text-to-Video |
| POST | `/kling/v1/videos/image2video` | kling_video | Kling Image-to-Video |
| GET | `/kling/v1/videos/text2video/:task_id` | kling_video | Kling Polling |
| GET | `/kling/v1/videos/image2video/:task_id` | kling_video | Kling Polling |
| POST | `/jimeng/` | jimeng | Jimeng Image |
| POST | `/:mode/mj/*` | midjourney | Midjourney (带 mode 前缀) |
| POST | `/mj/*` | midjourney | Midjourney |
| POST | `/suno/*` | suno | Suno Music |
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add supported proxy routes table to README"
```

---

### Task 5: Full test suite verification

- [ ] **Step 1: Run Go tests**

Run: `go test ./... -count=1`
Expected: All PASS, 0 failures.

- [ ] **Step 2: Run Python worker tests**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: All PASS (no changes to Python code, just verify no cross-contract breakage).

- [ ] **Step 3: Verify scope**

Inspect git diff to confirm changes are scoped to expected files only.
Expected: changes in `proxy.go`, `proxy_test.go`, `registry.go`, `README.md` only.
