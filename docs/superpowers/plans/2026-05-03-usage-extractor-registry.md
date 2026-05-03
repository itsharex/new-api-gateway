# Usage Extractor Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the monolithic SSE/non-streaming usage parser with a registry of format-specific extractors dispatched by `ProtocolFamily`.

**Architecture:** Each API format (OpenAI Chat, OpenAI Responses, OpenAI Images, Anthropic, Gemini) gets its own extractor struct in its own file, registered in a global map by `ProtocolFamily`. The `sseUsageExtractor` in `stream.go` delegates parsing to the format-specific extractor. Anthropic SSE requires stateful accumulation across events (input_tokens from `message_start`, output_tokens from `message_delta`), so the interface uses `processSSE` (mutate) + `sseResult` (read) instead of a single return-value method.

**Tech Stack:** Go 1.x, standard library `encoding/json`, existing `internal/gateway` package.

**Design doc:** `docs/superpowers/specs/2026-05-03-usage-extractor-registry-design.md`

---

## Interface deviation from spec

The spec defined `extractSSE(payload []byte) (minimalUsage, string)`. This doesn't work for Anthropic SSE, which sends `input_tokens` in `message_start` and `output_tokens` in `message_delta` — a stateless return per event would lose input_tokens when the delta overwrites. The interface is revised to:

```go
type usageExtractor interface {
    processSSE(payload []byte)                   // parse one SSE payload, update internal state
    sseResult() (minimalUsage, string)           // read accumulated SSE state
    extractResponse(body []byte) (minimalUsage, string) // non-streaming response
    extractRequest(path string, body []byte) string     // request model extraction
}
```

The registry stores **factories** (not instances) so each request gets a fresh, zero-state extractor:

```go
type extractorFactory func() usageExtractor
```

---

### Task 1: Foundation — interface, registry, minimalUsage

**Files:**
- Create: `internal/gateway/usage.go`

- [ ] **Step 1: Create `usage.go` with interface + registry + minimalUsage**

Move `minimalUsage` from `minimal.go` and add the extractor infrastructure.

```go
package gateway

import "encoding/json"

type minimalUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ReasoningTokens  int
	CachedTokens     int
}

// usageExtractor parses usage and model from one specific API format.
// Implementations are stateful for SSE: call processSSE per data line,
// then sseResult to read the accumulated state.
type usageExtractor interface {
	processSSE(payload []byte)
	sseResult() (minimalUsage, string)
	extractResponse(body []byte) (minimalUsage, string)
	extractRequest(path string, body []byte) string
}

type extractorFactory func() usageExtractor

var extractorFactories = map[string]extractorFactory{}

func registerExtractor(families []string, factory extractorFactory) {
	for _, f := range families {
		extractorFactories[f] = factory
	}
}

func extractorFor(family string) usageExtractor {
	if factory, ok := extractorFactories[family]; ok {
		return factory()
	}
	if factory, ok := extractorFactories["_generic"]; ok {
		return factory()
	}
	return nil
}

// extractModelFromBody is a shared helper used by most extractors.
func extractModelFromBody(body []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	return v.Model
}
```

- [ ] **Step 2: Run existing tests to verify nothing breaks**

Run: `go build ./internal/gateway/`
Expected: compiles (no consumers of the new types yet)

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/usage.go
git commit -m "feat(gateway): add usageExtractor interface and registry"
```

---

### Task 2: Generic extractor

**Files:**
- Create: `internal/gateway/usage_generic.go`
- Create: `internal/gateway/usage_generic_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/gateway/usage_generic_test.go`:

```go
package gateway

import "testing"

func TestGenericExtractorExtractRequest(t *testing.T) {
	g := newGenericExtractor()
	got := g.extractRequest("/v1/unknown", []byte(`{"model":"test-model"}`))
	if got != "test-model" {
		t.Fatalf("got %q, want %q", got, "test-model")
	}
}

func TestGenericExtractorExtractRequestEmptyBody(t *testing.T) {
	g := newGenericExtractor()
	got := g.extractRequest("/v1/unknown", []byte(`{}`))
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestGenericExtractorProcessSSE(t *testing.T) {
	g := newGenericExtractor()
	g.processSSE([]byte(`{"choices":[]}`))
	u, m := g.sseResult()
	if u != (minimalUsage{}) || m != "" {
		t.Fatalf("generic SSE should be no-op, got usage=%+v model=%q", u, m)
	}
}

func TestGenericExtractorExtractResponseWithUsage(t *testing.T) {
	g := newGenericExtractor()
	u, m := g.extractResponse([]byte(`{"model":"x","usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`))
	if u.TotalTokens != 8 {
		t.Fatalf("TotalTokens=%d, want 8", u.TotalTokens)
	}
	if m != "x" {
		t.Fatalf("model=%q, want %q", m, "x")
	}
}

func TestGenericExtractorExtractResponseEmpty(t *testing.T) {
	g := newGenericExtractor()
	u, m := g.extractResponse([]byte(`{}`))
	if u != (minimalUsage{}) {
		t.Fatalf("got usage=%+v, want zero", u)
	}
	if m != "" {
		t.Fatalf("got model=%q, want empty", m)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/ -run TestGenericExtractor -v`
Expected: FAIL (function not defined)

- [ ] **Step 3: Write implementation**

Create `internal/gateway/usage_generic.go`:

```go
package gateway

import "encoding/json"

type genericExtractor struct {
	acc minimalUsage
	mdl string
}

func newGenericExtractor() *genericExtractor {
	return &genericExtractor{}
}

func (e *genericExtractor) processSSE(_ []byte) {
	// generic: no SSE parsing
}

func (e *genericExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *genericExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var payload struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return minimalUsage{}, ""
	}
	u := minimalUsage{
		PromptTokens:     payload.Usage.PromptTokens,
		CompletionTokens: payload.Usage.CompletionTokens,
		TotalTokens:      payload.Usage.TotalTokens,
	}
	return u, payload.Model
}

func (e *genericExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func init() {
	registerExtractor([]string{"_generic"}, func() usageExtractor {
		return newGenericExtractor()
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gateway/ -run TestGenericExtractor -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/usage_generic.go internal/gateway/usage_generic_test.go
git commit -m "feat(gateway): add generic usage extractor"
```

---

### Task 3: OpenAI Chat extractor

**Files:**
- Create: `internal/gateway/usage_openai_chat.go`
- Create: `internal/gateway/usage_openai_chat_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/gateway/usage_openai_chat_test.go`:

```go
package gateway

import "testing"

func TestOpenAIChatExtractRequest(t *testing.T) {
	e := newOpenAIChatExtractor()
	got := e.extractRequest("/v1/chat/completions", []byte(`{"model":"gpt-4o","messages":[]}`))
	if got != "gpt-4o" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenAIChatProcessSSE(t *testing.T) {
	e := newOpenAIChatExtractor()
	// First chunk: model only
	e.processSSE([]byte(`{"id":"1","model":"gpt-4o"}`))
	u, m := e.sseResult()
	if m != "gpt-4o" {
		t.Fatalf("model=%q, want gpt-4o", m)
	}
	if u.TotalTokens != 0 {
		t.Fatalf("TotalTokens=%d, want 0", u.TotalTokens)
	}
	// Second chunk: usage
	e.processSSE([]byte(`{"id":"2","model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"prompt_tokens_details":{"cached_tokens":5},"completion_tokens_details":{"reasoning_tokens":3}}}`))
	u, m = e.sseResult()
	if u.PromptTokens != 10 || u.CompletionTokens != 20 || u.TotalTokens != 30 {
		t.Fatalf("usage=%+v", u)
	}
	if u.CachedTokens != 5 {
		t.Fatalf("CachedTokens=%d, want 5", u.CachedTokens)
	}
	if u.ReasoningTokens != 3 {
		t.Fatalf("ReasoningTokens=%d, want 3", u.ReasoningTokens)
	}
}

func TestOpenAIChatExtractResponse(t *testing.T) {
	e := newOpenAIChatExtractor()
	body := []byte(`{"model":"gpt-4o","usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}}`)
	u, m := e.extractResponse(body)
	if u.PromptTokens != 11 || u.CompletionTokens != 7 || u.TotalTokens != 18 {
		t.Fatalf("usage=%+v", u)
	}
	if u.CachedTokens != 3 || u.ReasoningTokens != 2 {
		t.Fatalf("details=%+v", u)
	}
	if m != "gpt-4o" {
		t.Fatalf("model=%q", m)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/ -run TestOpenAIChat -v`
Expected: FAIL

- [ ] **Step 3: Write implementation**

Create `internal/gateway/usage_openai_chat.go`:

```go
package gateway

import "encoding/json"

type openaiChatExtractor struct {
	acc minimalUsage
	mdl string
}

func newOpenAIChatExtractor() *openaiChatExtractor {
	return &openaiChatExtractor{}
}

func (e *openaiChatExtractor) processSSE(payload []byte) {
	var v struct {
		Usage struct {
			PromptTokens      int `json:"prompt_tokens"`
			CompletionTokens  int `json:"completion_tokens"`
			TotalTokens       int `json:"total_tokens"`
			PromptDetails     struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.Usage.TotalTokens > 0 {
		e.acc = minimalUsage{
			PromptTokens:     v.Usage.PromptTokens,
			CompletionTokens: v.Usage.CompletionTokens,
			TotalTokens:      v.Usage.TotalTokens,
			ReasoningTokens:  v.Usage.CompletionDetails.ReasoningTokens,
			CachedTokens:     v.Usage.PromptDetails.CachedTokens,
		}
	}
	if v.Model != "" {
		e.mdl = v.Model
	}
}

func (e *openaiChatExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *openaiChatExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		Usage struct {
			PromptTokens      int `json:"prompt_tokens"`
			CompletionTokens  int `json:"completion_tokens"`
			TotalTokens       int `json:"total_tokens"`
			PromptDetails     struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &v) != nil {
		return minimalUsage{}, ""
	}
	return minimalUsage{
		PromptTokens:     v.Usage.PromptTokens,
		CompletionTokens: v.Usage.CompletionTokens,
		TotalTokens:      v.Usage.TotalTokens,
		ReasoningTokens:  v.Usage.CompletionDetails.ReasoningTokens,
		CachedTokens:     v.Usage.PromptDetails.CachedTokens,
	}, v.Model
}

func (e *openaiChatExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func init() {
	registerExtractor([]string{"openai_chat", "openai_completions"}, func() usageExtractor {
		return newOpenAIChatExtractor()
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gateway/ -run TestOpenAIChat -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/usage_openai_chat.go internal/gateway/usage_openai_chat_test.go
git commit -m "feat(gateway): add OpenAI Chat usage extractor"
```

---

### Task 4: OpenAI Responses extractor

**Files:**
- Create: `internal/gateway/usage_openai_responses.go`
- Create: `internal/gateway/usage_openai_responses_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/gateway/usage_openai_responses_test.go`:

```go
package gateway

import "testing"

func TestOpenAIResponsesExtractRequest(t *testing.T) {
	e := newOpenAIResponsesExtractor()
	got := e.extractRequest("/v1/responses", []byte(`{"model":"gpt-5.2","input":"hi"}`))
	if got != "gpt-5.2" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenAIResponsesProcessSSE(t *testing.T) {
	e := newOpenAIResponsesExtractor()
	// response.created — no usage
	e.processSSE([]byte(`{"type":"response.created","response":{"id":"resp_1","object":"response"}}`))
	// response.completed — has usage and model
	e.processSSE([]byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.2","usage":{"input_tokens":21903,"output_tokens":105,"total_tokens":22008,"input_tokens_details":{"cached_tokens":21760},"output_tokens_details":{"reasoning_tokens":74}}}}`))
	u, m := e.sseResult()
	if u.PromptTokens != 21903 {
		t.Fatalf("PromptTokens=%d, want 21903", u.PromptTokens)
	}
	if u.CompletionTokens != 105 {
		t.Fatalf("CompletionTokens=%d, want 105", u.CompletionTokens)
	}
	if u.TotalTokens != 22008 {
		t.Fatalf("TotalTokens=%d, want 22008", u.TotalTokens)
	}
	if u.CachedTokens != 21760 {
		t.Fatalf("CachedTokens=%d, want 21760", u.CachedTokens)
	}
	if u.ReasoningTokens != 74 {
		t.Fatalf("ReasoningTokens=%d, want 74", u.ReasoningTokens)
	}
	if m != "gpt-5.2" {
		t.Fatalf("model=%q, want gpt-5.2", m)
	}
}

func TestOpenAIResponsesExtractResponse(t *testing.T) {
	e := newOpenAIResponsesExtractor()
	body := []byte(`{"id":"resp_1","model":"gpt-5.2","usage":{"input_tokens":100,"output_tokens":50,"total_tokens":150,"input_tokens_details":{"cached_tokens":80},"output_tokens_details":{"reasoning_tokens":10}}}`)
	u, m := e.extractResponse(body)
	if u.PromptTokens != 100 || u.CompletionTokens != 50 || u.TotalTokens != 150 {
		t.Fatalf("usage=%+v", u)
	}
	if u.CachedTokens != 80 {
		t.Fatalf("CachedTokens=%d, want 80", u.CachedTokens)
	}
	if u.ReasoningTokens != 10 {
		t.Fatalf("ReasoningTokens=%d, want 10", u.ReasoningTokens)
	}
	if m != "gpt-5.2" {
		t.Fatalf("model=%q", m)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/ -run TestOpenAIResponses -v`
Expected: FAIL

- [ ] **Step 3: Write implementation**

Create `internal/gateway/usage_openai_responses.go`:

```go
package gateway

import "encoding/json"

type openaiResponsesExtractor struct {
	acc minimalUsage
	mdl string
}

func newOpenAIResponsesExtractor() *openaiResponsesExtractor {
	return &openaiResponsesExtractor{}
}

func (e *openaiResponsesExtractor) processSSE(payload []byte) {
	var v struct {
		Response struct {
			Model string `json:"model"`
			Usage struct {
				InputTokens   int `json:"input_tokens"`
				OutputTokens  int `json:"output_tokens"`
				TotalTokens   int `json:"total_tokens"`
				InputDetails  struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputDetails struct {
					ReasoningTokens int `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	ru := v.Response.Usage
	if ru.TotalTokens > 0 {
		e.acc = minimalUsage{
			PromptTokens:    ru.InputTokens,
			CompletionTokens: ru.OutputTokens,
			TotalTokens:     ru.TotalTokens,
			ReasoningTokens: ru.OutputDetails.ReasoningTokens,
			CachedTokens:    ru.InputDetails.CachedTokens,
		}
	}
	if v.Response.Model != "" {
		e.mdl = v.Response.Model
	}
}

func (e *openaiResponsesExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *openaiResponsesExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens   int `json:"input_tokens"`
			OutputTokens  int `json:"output_tokens"`
			TotalTokens   int `json:"total_tokens"`
			InputDetails  struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &v) != nil {
		return minimalUsage{}, ""
	}
	return minimalUsage{
		PromptTokens:     v.Usage.InputTokens,
		CompletionTokens: v.Usage.OutputTokens,
		TotalTokens:      v.Usage.TotalTokens,
		ReasoningTokens:  v.Usage.OutputDetails.ReasoningTokens,
		CachedTokens:     v.Usage.InputDetails.CachedTokens,
	}, v.Model
}

func (e *openaiResponsesExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func init() {
	registerExtractor([]string{"openai_responses"}, func() usageExtractor {
		return newOpenAIResponsesExtractor()
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gateway/ -run TestOpenAIResponses -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/usage_openai_responses.go internal/gateway/usage_openai_responses_test.go
git commit -m "feat(gateway): add OpenAI Responses usage extractor"
```

---

### Task 5: OpenAI Images extractor

**Files:**
- Create: `internal/gateway/usage_openai_images.go`
- Create: `internal/gateway/usage_openai_images_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/gateway/usage_openai_images_test.go`:

```go
package gateway

import "testing"

func TestOpenAIImagesExtractRequest(t *testing.T) {
	e := newOpenAIImagesExtractor()
	got := e.extractRequest("/v1/images/generations", []byte(`{"model":"gpt-image-2","prompt":"cat"}`))
	if got != "gpt-image-2" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenAIImagesExtractResponse(t *testing.T) {
	e := newOpenAIImagesExtractor()
	body := []byte(`{"created":0,"data":[{"b64_json":"...","url":"..."}],"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300,"input_tokens_details":{"image_tokens":20,"text_tokens":80},"output_tokens_details":{"image_tokens":180,"text_tokens":20}}}`)
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
	_ = m // images response has no model field
}

func TestOpenAIImagesExtractResponseNoUsage(t *testing.T) {
	e := newOpenAIImagesExtractor()
	body := []byte(`{"created":0,"data":[{"url":"..."}]}`)
	u, _ := e.extractResponse(body)
	if u != (minimalUsage{}) {
		t.Fatalf("got usage=%+v, want zero", u)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/ -run TestOpenAIImages -v`
Expected: FAIL

- [ ] **Step 3: Write implementation**

Create `internal/gateway/usage_openai_images.go`:

```go
package gateway

import "encoding/json"

type openaiImagesExtractor struct {
	acc minimalUsage
	mdl string
}

func newOpenAIImagesExtractor() *openaiImagesExtractor {
	return &openaiImagesExtractor{}
}

func (e *openaiImagesExtractor) processSSE(payload []byte) {
	// Images API SSE streams partial image data events, usage may appear in final event
	// with the same structure as non-streaming response
	var v struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.Usage.TotalTokens > 0 {
		e.acc = minimalUsage{
			PromptTokens:     v.Usage.InputTokens,
			CompletionTokens: v.Usage.OutputTokens,
			TotalTokens:      v.Usage.TotalTokens,
		}
	}
}

func (e *openaiImagesExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *openaiImagesExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &v) != nil {
		return minimalUsage{}, ""
	}
	return minimalUsage{
		PromptTokens:     v.Usage.InputTokens,
		CompletionTokens: v.Usage.OutputTokens,
		TotalTokens:      v.Usage.TotalTokens,
	}, ""
}

func (e *openaiImagesExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func init() {
	registerExtractor([]string{"openai_images"}, func() usageExtractor {
		return newOpenAIImagesExtractor()
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gateway/ -run TestOpenAIImages -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/usage_openai_images.go internal/gateway/usage_openai_images_test.go
git commit -m "feat(gateway): add OpenAI Images usage extractor"
```

---

### Task 6: Anthropic Claude extractor

**Files:**
- Create: `internal/gateway/usage_claude.go`
- Create: `internal/gateway/usage_claude_test.go`

This extractor is stateful for SSE — it accumulates `input_tokens` from `message_start` and `output_tokens` from `message_delta`.

- [ ] **Step 1: Write failing test**

Create `internal/gateway/usage_claude_test.go`:

```go
package gateway

import "testing"

func TestClaudeExtractRequest(t *testing.T) {
	e := newClaudeExtractor()
	got := e.extractRequest("/v1/messages", []byte(`{"model":"claude-sonnet-4-20250514","messages":[]}`))
	if got != "claude-sonnet-4-20250514" {
		t.Fatalf("got %q", got)
	}
}

func TestClaudeProcessSSEAccumulates(t *testing.T) {
	e := newClaudeExtractor()
	// message_start: input_tokens
	e.processSSE([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":100,"output_tokens":0}}}`))
	u, _ := e.sseResult()
	if u.PromptTokens != 100 {
		t.Fatalf("after message_start: PromptTokens=%d, want 100", u.PromptTokens)
	}
	// message_delta: output_tokens
	e.processSSE([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}`))
	u, _ = e.sseResult()
	if u.PromptTokens != 100 {
		t.Fatalf("after message_delta: PromptTokens=%d, want 100 (should be preserved)", u.PromptTokens)
	}
	if u.CompletionTokens != 50 {
		t.Fatalf("after message_delta: CompletionTokens=%d, want 50", u.CompletionTokens)
	}
	if u.TotalTokens != 150 {
		t.Fatalf("TotalTokens=%d, want 150", u.TotalTokens)
	}
}

func TestClaudeProcessSSECacheTokens(t *testing.T) {
	e := newClaudeExtractor()
	e.processSSE([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":30,"cache_creation_input_tokens":10}}}`))
	u, _ := e.sseResult()
	if u.CachedTokens != 40 {
		t.Fatalf("CachedTokens=%d, want 40", u.CachedTokens)
	}
}

func TestClaudeExtractResponse(t *testing.T) {
	e := newClaudeExtractor()
	body := []byte(`{"id":"msg_1","model":"claude-sonnet-4-20250514","usage":{"input_tokens":25,"output_tokens":15,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}`)
	u, m := e.extractResponse(body)
	if u.PromptTokens != 25 {
		t.Fatalf("PromptTokens=%d, want 25", u.PromptTokens)
	}
	if u.CompletionTokens != 15 {
		t.Fatalf("CompletionTokens=%d, want 15", u.CompletionTokens)
	}
	if u.TotalTokens != 40 {
		t.Fatalf("TotalTokens=%d, want 40", u.TotalTokens)
	}
	if u.CachedTokens != 15 {
		t.Fatalf("CachedTokens=%d, want 15", u.CachedTokens)
	}
	if m != "claude-sonnet-4-20250514" {
		t.Fatalf("model=%q", m)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/ -run TestClaude -v`
Expected: FAIL

- [ ] **Step 3: Write implementation**

Create `internal/gateway/usage_claude.go`:

```go
package gateway

import "encoding/json"

type claudeExtractor struct {
	acc minimalUsage
	mdl string
}

func newClaudeExtractor() *claudeExtractor {
	return &claudeExtractor{}
}

func (e *claudeExtractor) processSSE(payload []byte) {
	var v struct {
		Type   string `json:"type"`
		Usage  struct {
			OutputTokens       int `json:"output_tokens"`
			CacheReadTokens    int `json:"cache_read_input_tokens"`
			CacheCreateTokens  int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Message struct {
			Usage struct {
				InputTokens       int `json:"input_tokens"`
				OutputTokens      int `json:"output_tokens"`
				CacheReadTokens   int `json:"cache_read_input_tokens"`
				CacheCreateTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	// message_start: accumulate input tokens and cache tokens
	if v.Message.Usage.InputTokens > 0 {
		e.acc.PromptTokens = v.Message.Usage.InputTokens
		if e.acc.CachedTokens == 0 {
			e.acc.CachedTokens = v.Message.Usage.CacheReadTokens + v.Message.Usage.CacheCreateTokens
		}
	}
	// message_delta or top-level usage: accumulate output tokens
	if v.Usage.OutputTokens > 0 {
		e.acc.CompletionTokens = v.Usage.OutputTokens
	}
	e.acc.TotalTokens = e.acc.PromptTokens + e.acc.CompletionTokens
}

func (e *claudeExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *claudeExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens       int `json:"input_tokens"`
			OutputTokens      int `json:"output_tokens"`
			CacheReadTokens   int `json:"cache_read_input_tokens"`
			CacheCreateTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &v) != nil {
		return minimalUsage{}, ""
	}
	u := minimalUsage{
		PromptTokens:     v.Usage.InputTokens,
		CompletionTokens: v.Usage.OutputTokens,
		TotalTokens:      v.Usage.InputTokens + v.Usage.OutputTokens,
		CachedTokens:     v.Usage.CacheReadTokens + v.Usage.CacheCreateTokens,
	}
	return u, v.Model
}

func (e *claudeExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func init() {
	registerExtractor([]string{"claude_messages"}, func() usageExtractor {
		return newClaudeExtractor()
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gateway/ -run TestClaude -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/usage_claude.go internal/gateway/usage_claude_test.go
git commit -m "feat(gateway): add Anthropic Claude usage extractor with SSE accumulation"
```

---

### Task 7: Gemini extractor

**Files:**
- Create: `internal/gateway/usage_gemini.go`
- Create: `internal/gateway/usage_gemini_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/gateway/usage_gemini_test.go`:

```go
package gateway

import "testing"

func TestGeminiExtractRequestFromPath(t *testing.T) {
	e := newGeminiExtractor()
	got := e.extractRequest("/v1/models/gemini-pro:generateContent", nil)
	if got != "gemini-pro" {
		t.Fatalf("got %q, want gemini-pro", got)
	}
}

func TestGeminiExtractRequestFromBody(t *testing.T) {
	e := newGeminiExtractor()
	got := e.extractRequest("/v1/models/gemini-pro:generateContent", []byte(`{"model":"override-model"}`))
	if got != "gemini-pro" {
		t.Fatalf("path should take priority, got %q", got)
	}
}

func TestGeminiExtractRequestFallbackBody(t *testing.T) {
	e := newGeminiExtractor()
	got := e.extractRequest("/v1/models/:generateContent", []byte(`{"model":"fallback-model"}`))
	if got != "fallback-model" {
		t.Fatalf("body fallback: got %q", got)
	}
}

func TestGeminiExtractResponse(t *testing.T) {
	e := newGeminiExtractor()
	body := []byte(`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30}}`)
	u, m := e.extractResponse(body)
	if u.PromptTokens != 10 {
		t.Fatalf("PromptTokens=%d, want 10", u.PromptTokens)
	}
	if u.CompletionTokens != 20 {
		t.Fatalf("CompletionTokens=%d, want 20", u.CompletionTokens)
	}
	if u.TotalTokens != 30 {
		t.Fatalf("TotalTokens=%d, want 30", u.TotalTokens)
	}
	if m != "" {
		t.Fatalf("model=%q, want empty (Gemini response doesn't include model)", m)
	}
}

func TestGeminiProcessSSE(t *testing.T) {
	e := newGeminiExtractor()
	e.processSSE([]byte(`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30}}`))
	u, m := e.sseResult()
	if u.TotalTokens != 30 {
		t.Fatalf("TotalTokens=%d, want 30", u.TotalTokens)
	}
	if m != "" {
		t.Fatalf("model=%q, want empty", m)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/ -run TestGemini -v`
Expected: FAIL

- [ ] **Step 3: Write implementation**

Create `internal/gateway/usage_gemini.go`:

```go
package gateway

import (
	"encoding/json"
	"strings"
)

type geminiExtractor struct {
	acc minimalUsage
	mdl string
}

func newGeminiExtractor() *geminiExtractor {
	return &geminiExtractor{}
}

func (e *geminiExtractor) processSSE(payload []byte) {
	var v struct {
		UsageMetadata struct {
			PromptTokens     int `json:"promptTokenCount"`
			CandidatesTokens int `json:"candidatesTokenCount"`
			TotalTokens      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.UsageMetadata.TotalTokens > 0 {
		e.acc = minimalUsage{
			PromptTokens:     v.UsageMetadata.PromptTokens,
			CompletionTokens: v.UsageMetadata.CandidatesTokens,
			TotalTokens:      v.UsageMetadata.TotalTokens,
		}
	}
}

func (e *geminiExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *geminiExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		UsageMetadata struct {
			PromptTokens     int `json:"promptTokenCount"`
			CandidatesTokens int `json:"candidatesTokenCount"`
			TotalTokens      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(body, &v) != nil {
		return minimalUsage{}, ""
	}
	return minimalUsage{
		PromptTokens:     v.UsageMetadata.PromptTokens,
		CompletionTokens: v.UsageMetadata.CandidatesTokens,
		TotalTokens:      v.UsageMetadata.TotalTokens,
	}, ""
}

// extractRequest extracts model from the URL path (/v1/models/{model}:action)
// with body as fallback.
func (e *geminiExtractor) extractRequest(path string, body []byte) string {
	if model := modelFromGeminiPath(path); model != "" {
		return model
	}
	return extractModelFromBody(body)
}

func modelFromGeminiPath(path string) string {
	// /v1/models/gemini-pro:generateContent → gemini-pro
	// /v1beta/models/gemini-2.0-flash:streamGenerateContent → gemini-2.0-flash
	idx := strings.Index(path, "/models/")
	if idx < 0 {
		return ""
	}
	after := path[idx+len("/models/"):]
	colon := strings.Index(after, ":")
	if colon > 0 {
		return after[:colon]
	}
	return after
}

func init() {
	registerExtractor([]string{"gemini"}, func() usageExtractor {
		return newGeminiExtractor()
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gateway/ -run TestGemini -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/usage_gemini.go internal/gateway/usage_gemini_test.go
git commit -m "feat(gateway): add Gemini usage extractor"
```

---

### Task 8: Registry dispatch tests

**Files:**
- Create: `internal/gateway/usage_test.go`

- [ ] **Step 1: Write registry tests**

Create `internal/gateway/usage_test.go`:

```go
package gateway

import (
	"reflect"
	"testing"
)

func TestExtractorForReturnsCorrectType(t *testing.T) {
	cases := map[string]string{
		"openai_chat":      "*gateway.openaiChatExtractor",
		"openai_completions": "*gateway.openaiChatExtractor",
		"openai_responses": "*gateway.openaiResponsesExtractor",
		"openai_images":    "*gateway.openaiImagesExtractor",
		"claude_messages":  "*gateway.claudeExtractor",
		"gemini":           "*gateway.geminiExtractor",
		"unknown":          "*gateway.genericExtractor",
		"":                 "*gateway.genericExtractor",
		"video":            "*gateway.genericExtractor",
		"embeddings":       "*gateway.genericExtractor",
	}
	for family, wantType := range cases {
		ext := extractorFor(family)
		if ext == nil {
			t.Fatalf("extractorFor(%q) returned nil", family)
		}
		gotType := reflect.TypeOf(ext).String()
		if gotType != wantType {
			t.Errorf("extractorFor(%q) = %s, want %s", family, gotType, wantType)
		}
	}
}

func TestExtractorForReturnsFreshInstance(t *testing.T) {
	a := extractorFor("openai_chat")
	b := extractorFor("openai_chat")
	if a == b {
		t.Fatal("extractorFor should return a new instance each call")
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/gateway/ -run TestExtractorFor -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/usage_test.go
git commit -m "test(gateway): add registry dispatch tests"
```

---

### Task 9: Wire into stream.go, minimal.go, proxy.go

**Files:**
- Modify: `internal/gateway/stream.go`
- Modify: `internal/gateway/minimal.go`
- Modify: `internal/gateway/proxy.go`
- Modify: `internal/gateway/stream_test.go`
- Modify: `internal/gateway/minimal_test.go`

- [ ] **Step 1: Modify `stream.go` — delegate to extractor**

Replace the `sseUsageExtractor` struct and methods. The `Write`, `copyStreamToClientAndCapture`, `teeStream`, `flushWriter`, `isStreamingResponse` functions stay unchanged. Changes:

1. Remove `usage` and `model` fields from struct, add `ext usageExtractor`
2. Change constructor to accept `usageExtractor`
3. Replace `parsePayload` body with delegation to `e.ext.processSSE(payload)`
4. Add `result()` method for proxy.go to read accumulated state

New `sseUsageExtractor` struct and related code:

```go
type sseUsageExtractor struct {
	w      io.Writer
	ext    usageExtractor
	buf    []byte
	prefix []byte
}

func newSSEUsageExtractor(w io.Writer, ext usageExtractor) *sseUsageExtractor {
	return &sseUsageExtractor{
		w:      w,
		ext:    ext,
		prefix: []byte("data: "),
	}
}

func (e *sseUsageExtractor) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if err != nil {
		return n, err
	}
	e.buf = append(e.buf, p...)
	for {
		idx := bytes.IndexByte(e.buf, '\n')
		if idx < 0 {
			break
		}
		line := e.buf[:idx]
		e.buf = e.buf[idx+1:]
		line = bytes.TrimRight(line, "\r")
		if !bytes.HasPrefix(line, e.prefix) {
			continue
		}
		payload := bytes.TrimLeft(line[len(e.prefix):], " ")
		if bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		e.ext.processSSE(payload)
	}
	return n, err
}

func (e *sseUsageExtractor) result() (minimalUsage, string) {
	return e.ext.sseResult()
}
```

- [ ] **Step 2: Modify `minimal.go` — remove old functions**

Delete `extractResponseUsage()` and `extractResponseModel()`. Keep `minimalUsage` definition (it's now also in `usage.go`, so remove it from `minimal.go`). Keep `extractRequestModel()` and `modelFromEngineEmbeddingPath()` — `extractRequestModel` remains for any code paths not yet migrated, and the engine embedding path logic is still needed.

The resulting `minimal.go` should contain only:

```go
package gateway

import "strings"

func extractRequestModel(path string, body []byte) string {
	if model := modelFromEngineEmbeddingPath(path); model != "" {
		return model
	}
	return extractModelFromBody(body)
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
```

- [ ] **Step 3: Modify `proxy.go` — pass ProtocolFamily to extractors**

In `serveStreamingResponse` (~line 732), change:
```go
// before:
usageExtractor := newSSEUsageExtractor(clientWriter)
// after:
usageExtractor := newSSEUsageExtractor(clientWriter, extractorFor(record.entry.ProtocolFamily))
```

In `serveStreamingResponse` (~line 786-788), change:
```go
// before:
record.usage = usageExtractor.usage
if record.modelUpstream == "" {
    record.modelUpstream = usageExtractor.model
}
// after:
u, m := usageExtractor.result()
record.usage = u
if record.modelUpstream == "" {
    record.modelUpstream = m
}
```

For the non-streaming path (~line 281-282), change:
```go
// before:
u := extractResponseUsage(responseBody)
m := extractResponseModel(responseBody)
// after:
ext := extractorFor(record.entry.ProtocolFamily)
u, m := ext.extractResponse(responseBody)
```

For request model extraction, change:
```go
// before:
record.modelRequested = extractRequestModel(req.URL.Path, requestBody)
// after:
ext := extractorFor(record.entry.ProtocolFamily)
record.modelRequested = ext.extractRequest(req.URL.Path, requestBody)
```

Note: look up the exact line numbers in proxy.go before editing. Search for `extractResponseUsage`, `extractResponseModel`, `extractRequestModel`, `newSSEUsageExtractor`, `usageExtractor.usage`, `usageExtractor.model`.

- [ ] **Step 4: Update `stream_test.go` — adapt constructor calls**

All existing SSE tests create `newSSEUsageExtractor(&buf)`. Change each to `newSSEUsageExtractor(&buf, extractorFor("openai_chat"))`. The tests for Anthropic fields should use `extractorFor("claude_messages")`. The Responses API test should use `extractorFor("openai_responses")`.

Replace every `newSSEUsageExtractor(&buf)` call in the test file with the appropriate family. Then remove the old `parsePayload`-centric tests that are now covered by the per-extractor tests (Tests that directly test the JSON parsing logic can be deleted since they're now in the per-extractor test files).

Keep the SSE line-buffering tests (`TestSSEUsageExtractorHandlesSplitChunks`) as they test the `Write()` method's line reassembly. Update them to pass an extractor.

The specific test updates:

`TestSSEUsageExtractorExtractsUsageFromLastChunk`: change constructor, keep assertions
`TestSSEUsageExtractorHandlesSplitChunks`: change constructor, keep assertions
`TestSSEUsageExtractorAnthropicFields`: change constructor to use `extractorFor("claude_messages")`, keep assertions. **Note:** this test will now test accumulation across two events, so the final usage should have PromptTokens=100 and CompletionTokens=15 (previously it only asserted PromptTokens=100). Update the assertion.
`TestSSEUsageExtractorEmptyStream`: change constructor
`TestSSEUsageExtractorFallbackTotalTokens`: delete — this is now covered by `TestClaudeProcessSSEAccumulates` or `TestGenericExtractorProcessSSE`
`TestSSEUsageExtractorResponsesAPI`: change constructor to use `extractorFor("openai_responses")`, keep assertions

- [ ] **Step 5: Delete migrated tests from `minimal_test.go`**

Remove `TestExtractOpenAIUsage`, `TestExtractAnthropicUsage`, `TestExtractResponsesAPIUsage` — these are now covered by the per-extractor tests. Keep `TestExtractRequestModelFromJSONBody` and `TestExtractRequestModelFromEngineEmbeddingPath` since `extractRequestModel` still exists in `minimal.go`.

Remove `TestExtractResponseModelFromJSONBody` and `TestExtractResponseModelReturnsEmptyForInvalidJSON` — `extractResponseModel` no longer exists.

- [ ] **Step 6: Run all gateway tests**

Run: `go test ./internal/gateway/ -v`
Expected: all PASS

- [ ] **Step 7: Run full test suite**

Run: `go test ./...`
Expected: all PASS

- [ ] **Step 8: Commit**

```bash
git add internal/gateway/stream.go internal/gateway/minimal.go internal/gateway/proxy.go internal/gateway/stream_test.go internal/gateway/minimal_test.go
git commit -m "refactor(gateway): wire usage extractor registry into proxy pipeline"
```

---

### Task 10: E2E test — usage assertions + streaming + new formats

**Files:**
- Modify: `e2e/test_gateway_capture.py`

- [ ] **Step 1: Add usage fields to TRACE_FIELDS and assertions**

In `test_gateway_capture.py`, update `TRACE_FIELDS`:

```python
TRACE_FIELDS = """
    trace_id, identity_resolution_status, username_snapshot,
    token_fingerprint, fingerprint_display,
    protocol_family, capture_mode, status_code,
    request_body_size, response_body_size,
    request_raw_ref, response_raw_ref,
    model_requested, model_upstream,
    usage_total_tokens, usage_prompt_tokens, usage_completion_tokens
""".strip().replace("\n", "").replace("  ", " ")
```

Add usage assertions in `assert_traces` after the existing assertions for each trace:

```python
        # Usage assertions
        gt(ctx, "usage_total_tokens", t["usage_total_tokens"], 0)
        gt(ctx, "usage_prompt_tokens", t["usage_prompt_tokens"], 0)
        not_empty(ctx, "model_upstream", t["model_upstream"])
```

- [ ] **Step 2: Add `send_chat_completions_stream()`**

```python
def send_chat_completions_stream() -> list[TraceResult]:
    """Stream request via /v1/chat/completions with stream=true."""
    print("\n=== Phase 2c: /v1/chat/completions (stream) ===")
    results: list[TraceResult] = []
    body = {
        "model": MODEL,
        "messages": [{"role": "user", "content": "hello"}],
        "max_tokens": 10,
        "stream": True,
    }
    url = f"{GATEWAY_URL}/v1/chat/completions"
    try:
        resp = _http.post(url, headers=HEADERS, json=body, stream=True, timeout=60)
    except requests.RequestException as exc:
        check("/v1/chat/completions:stream", False, f"connection error: {exc}")
        return results
    if resp.status_code >= 300:
        check("/v1/chat/completions:stream", False, f"status={resp.status_code}")
        return results
    # Consume the stream
    for _ in resp.iter_lines():
        pass
    trace_id = resp.headers.get("x-audit-trace-id", "")
    print(f"  Stream: status={resp.status_code} trace_id={trace_id}")
    results.append({
        "trace_id": trace_id,
        "endpoint": "/v1/chat/completions",
        "turn": 0,
        "status_code": resp.status_code,
    })
    return results
```

- [ ] **Step 3: Add `send_responses_stream()`**

```python
def send_responses_stream() -> list[TraceResult]:
    """Stream request via /v1/responses with stream=true."""
    print("\n=== Phase 2d: /v1/responses (stream) ===")
    results: list[TraceResult] = []
    body = {
        "model": MODEL,
        "input": "hello",
        "max_output_tokens": 10,
        "stream": True,
    }
    url = f"{GATEWAY_URL}/v1/responses"
    try:
        resp = _http.post(url, headers=HEADERS, json=body, stream=True, timeout=60)
    except requests.RequestException as exc:
        check("/v1/responses:stream", False, f"connection error: {exc}")
        return results
    if resp.status_code >= 300:
        check("/v1/responses:stream", False, f"status={resp.status_code}")
        return results
    for _ in resp.iter_lines():
        pass
    trace_id = resp.headers.get("x-audit-trace-id", "")
    print(f"  Stream: status={resp.status_code} trace_id={trace_id}")
    results.append({
        "trace_id": trace_id,
        "endpoint": "/v1/responses",
        "turn": 0,
        "status_code": resp.status_code,
    })
    return results
```

- [ ] **Step 4: Update PROTOCOL_FAMILY and main()**

Add entries for the stream endpoints:

```python
PROTOCOL_FAMILY = {
    "/v1/chat/completions": "openai_chat",
    "/v1/responses": "openai_responses",
}
```

(This is unchanged since stream endpoints use the same path and same ProtocolFamily.)

Update `main()` to include new scenarios:

```python
def main() -> None:
    preflight()

    all_results: list[TraceResult] = []
    all_results.extend(send_chat_completions())
    all_results.extend(send_responses())
    all_results.extend(send_chat_completions_stream())
    all_results.extend(send_responses_stream())

    # ... rest of main() unchanged
```

- [ ] **Step 5: Run the e2e test manually**

Run: `cd e2e && uv run test_gateway_capture.py`
Expected: All assertions pass, including usage_total_tokens > 0 and model_upstream non-empty for both streaming and non-streaming scenarios.

- [ ] **Step 6: Commit**

```bash
git add e2e/test_gateway_capture.py
git commit -m "test(e2e): add usage assertions and streaming scenarios"
```

---

### Task 11: Final cleanup + verification

**Files:**
- Verify all files

- [ ] **Step 1: Run full test suite**

Run: `go test ./...`
Expected: all PASS

- [ ] **Step 2: Verify no references to deleted functions remain**

Run: `grep -rn 'extractResponseUsage\|extractResponseModel' internal/`
Expected: no results (these functions are deleted)

- [ ] **Step 3: Verify all extractors are registered**

Run: `grep -rn 'registerExtractor' internal/gateway/usage_*.go`
Expected: 6 registrations (generic, openai_chat, openai_responses, openai_images, claude, gemini)

- [ ] **Step 4: Commit if any cleanup was needed**

```bash
git add -A
git commit -m "chore(gateway): cleanup after usage extractor registry refactor"
```
