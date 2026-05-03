# SSE Response Assembly Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Assemble structured JSON from SSE streaming responses in the Go gateway, replacing raw SSE evidence with reconstructed non-streaming-equivalent JSON.

**Architecture:** Extend the existing `usageExtractor` interface with an `assembleSSE()` method. Each format-specific extractor accumulates SSE delta events during streaming and returns a fully assembled JSON response body when the stream ends. The proxy layer uses the assembled JSON instead of raw SSE text when storing evidence.

**Tech Stack:** Go, `encoding/json`, `bytes`, `io`

---

### Task 1: Add `assembleSSE()` to the interface and stubs

**Files:**
- Modify: `internal/gateway/usage.go`
- Modify: `internal/gateway/usage_generic.go`
- Modify: `internal/gateway/usage_openai_images.go`
- Modify: `internal/gateway/usage_gemini.go`

- [ ] **Step 1: Add `assembleSSE()` to the interface in `usage.go`**

Add the method to the `usageExtractor` interface (after `extractRequest`):

```go
type usageExtractor interface {
	processSSE(payload []byte)
	sseResult() (minimalUsage, string)
	extractResponse(body []byte) (minimalUsage, string)
	extractRequest(path string, body []byte) string
	assembleSSE() []byte // returns assembled JSON or nil
}
```

- [ ] **Step 2: Add `assembleSSE()` stub to `usage_generic.go`**

Add to `genericExtractor`:

```go
func (e *genericExtractor) assembleSSE() []byte { return nil }
```

- [ ] **Step 3: Add `assembleSSE()` stub to `usage_openai_images.go`**

```go
func (e *openaiImagesExtractor) assembleSSE() []byte { return nil }
```

- [ ] **Step 4: Add `assembleSSE()` stub to `usage_gemini.go`**

```go
func (e *geminiExtractor) assembleSSE() []byte { return nil }
```

- [ ] **Step 5: Run tests to verify compilation**

Run: `go test ./internal/gateway/...`
Expected: Compilation fails for `openaiChatExtractor`, `openaiResponsesExtractor`, `claudeExtractor` — that's expected. Stubs compile fine.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/usage.go internal/gateway/usage_generic.go internal/gateway/usage_openai_images.go internal/gateway/usage_gemini.go
git commit -m "feat(gateway): add assembleSSE to usageExtractor interface with nil stubs"
```

---

### Task 2: OpenAI Responses — save `response.completed` and implement `assembleSSE()`

**Files:**
- Modify: `internal/gateway/usage_openai_responses.go`
- Modify: `internal/gateway/usage_openai_responses_test.go`

The Responses API's `response.completed` event contains the full response object. Save it using `json.RawMessage` so no fields are lost, then `assembleSSE()` returns it.

- [ ] **Step 1: Write the failing test**

Add to `usage_openai_responses_test.go`:

```go
func TestOpenAIResponsesAssembleSSE(t *testing.T) {
	e := newOpenAIResponsesExtractor()
	e.processSSE([]byte(`{"type":"response.created","response":{"id":"resp_1","object":"response"}}`))
	e.processSSE([]byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.2","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}],"usage":{"input_tokens":100,"output_tokens":50,"total_tokens":150}}}`))
	assembled := e.assembleSSE()
	if assembled == nil {
		t.Fatal("assembleSSE returned nil")
	}
	// Verify it's valid JSON with expected fields
	var v struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(assembled, &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if v.ID != "resp_1" {
		t.Fatalf("id=%q, want resp_1", v.ID)
	}
	if v.Model != "gpt-5.2" {
		t.Fatalf("model=%q, want gpt-5.2", v.Model)
	}
	if v.Status != "completed" {
		t.Fatalf("status=%q, want completed", v.Status)
	}
	if len(v.Output) != 1 || len(v.Output[0].Content) != 1 {
		t.Fatalf("output=%+v", v.Output)
	}
	if v.Output[0].Content[0].Text != "hello world" {
		t.Fatalf("text=%q", v.Output[0].Content[0].Text)
	}
	if v.Usage.TotalTokens != 150 {
		t.Fatalf("total_tokens=%d, want 150", v.Usage.TotalTokens)
	}
}

func TestOpenAIResponsesAssembleSSEEmptyStream(t *testing.T) {
	e := newOpenAIResponsesExtractor()
	assembled := e.assembleSSE()
	if assembled != nil {
		t.Fatalf("expected nil for empty stream, got %q", string(assembled))
	}
}
```

Add `"encoding/json"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run TestOpenAIResponsesAssemble -v`
Expected: FAIL (method not implemented)

- [ ] **Step 3: Implement `assembleSSE()` for openaiResponsesExtractor**

Add a `rawCompleted json.RawMessage` field to the struct, populate it in `processSSE`, return it in `assembleSSE()`.

In `usage_openai_responses.go`, add field to struct:

```go
type openaiResponsesExtractor struct {
	acc          minimalUsage
	mdl          string
	rawCompleted json.RawMessage
}
```

In `processSSE`, after the existing `if json.Unmarshal(payload, &v) != nil { return }`, add logic to capture `response.completed`:

```go
func (e *openaiResponsesExtractor) processSSE(payload []byte) {
	var v struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.Type == "response.completed" && len(v.Response) > 0 {
		e.rawCompleted = v.Response
	}
	// ... existing usage extraction logic unchanged ...
}
```

Wait — the existing `processSSE` already unmarshals the payload into a struct with `Response.Usage` etc. We need to refactor it to also capture the raw response. The cleanest way: unmarshal `type` first, then if it's `response.completed`, also save the raw `response` field.

Refactor `processSSE` to:

```go
func (e *openaiResponsesExtractor) processSSE(payload []byte) {
	var header struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(payload, &header) != nil {
		return
	}
	if header.Type == "response.completed" && len(header.Response) > 0 {
		e.rawCompleted = header.Response
	}
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
```

Add `assembleSSE()` method:

```go
func (e *openaiResponsesExtractor) assembleSSE() []byte {
	if len(e.rawCompleted) == 0 {
		return nil
	}
	return e.rawCompleted
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway/ -run TestOpenAIResponsesAssemble -v`
Expected: PASS

- [ ] **Step 5: Run all existing tests**

Run: `go test ./internal/gateway/ -run TestOpenAIResponses -v`
Expected: ALL PASS (existing tests unaffected)

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/usage_openai_responses.go internal/gateway/usage_openai_responses_test.go
git commit -m "feat(gateway): implement assembleSSE for OpenAI Responses extractor"
```

---

### Task 3: OpenAI Chat — accumulate deltas and implement `assembleSSE()`

**Files:**
- Modify: `internal/gateway/usage_openai_chat.go`
- Modify: `internal/gateway/usage_openai_chat_test.go`

Chat Completions has no final event with complete data. We must accumulate:
- `choices[].delta.role` (first chunk only)
- `choices[].delta.content` (concatenated)
- `choices[].delta.tool_calls[].function.name` and `function.arguments` (accumulated by index)
- `id`, `model` from any chunk
- `usage` from last chunk with usage
- `choices[].finish_reason` from last chunk

- [ ] **Step 1: Write the failing test**

Add to `usage_openai_chat_test.go`:

```go
func TestOpenAIChatAssembleSSE(t *testing.T) {
	e := newOpenAIChatExtractor()
	e.processSSE([]byte(`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`))
	e.processSSE([]byte(`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`))
	e.processSSE([]byte(`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`))
	e.processSSE([]byte(`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	assembled := e.assembleSSE()
	if assembled == nil {
		t.Fatal("assembleSSE returned nil")
	}
	var v struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int    `json:"index"`
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(assembled, &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if v.ID != "chatcmpl-1" {
		t.Fatalf("id=%q", v.ID)
	}
	if v.Model != "gpt-4o" {
		t.Fatalf("model=%q", v.Model)
	}
	if len(v.Choices) != 1 {
		t.Fatalf("choices=%+v", v.Choices)
	}
	if v.Choices[0].Message.Role != "assistant" {
		t.Fatalf("role=%q", v.Choices[0].Message.Role)
	}
	if v.Choices[0].Message.Content != "Hello world" {
		t.Fatalf("content=%q", v.Choices[0].Message.Content)
	}
	if v.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason=%q", v.Choices[0].FinishReason)
	}
	if v.Usage.TotalTokens != 15 {
		t.Fatalf("total_tokens=%d", v.Usage.TotalTokens)
	}
}

func TestOpenAIChatAssembleSSEWithToolCalls(t *testing.T) {
	e := newOpenAIChatExtractor()
	e.processSSE([]byte(`{"id":"chatcmpl-2","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`))
	e.processSSE([]byte(`{"id":"chatcmpl-2","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loca"}}]},"finish_reason":null}]}`))
	e.processSSE([]byte(`{"id":"chatcmpl-2","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"tion\":\"SF\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}}`))
	assembled := e.assembleSSE()
	if assembled == nil {
		t.Fatal("assembleSSE returned nil")
	}
	var v struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(assembled, &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(v.Choices) != 1 {
		t.Fatalf("choices len=%d", len(v.Choices))
	}
	if v.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason=%q", v.Choices[0].FinishReason)
	}
	tc := v.Choices[0].Message.ToolCalls
	if len(tc) != 1 {
		t.Fatalf("tool_calls len=%d", len(tc))
	}
	if tc[0].Function.Name != "get_weather" {
		t.Fatalf("name=%q", tc[0].Function.Name)
	}
	if tc[0].Function.Arguments != `{"location":"SF"}` {
		t.Fatalf("arguments=%q", tc[0].Function.Arguments)
	}
}

func TestOpenAIChatAssembleSSEEmptyStream(t *testing.T) {
	e := newOpenAIChatExtractor()
	if assembled := e.assembleSSE(); assembled != nil {
		t.Fatalf("expected nil, got %q", string(assembled))
	}
}
```

Add `"encoding/json"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run TestOpenAIChatAssemble -v`
Expected: FAIL

- [ ] **Step 3: Implement accumulation and `assembleSSE()` for openaiChatExtractor**

Replace `usage_openai_chat.go` with the expanded version. Add accumulation fields to the struct:

```go
package gateway

import "encoding/json"

type openaiChatExtractor struct {
	acc      minimalUsage
	mdl      string
	sseID    string
	sseRole  string
	sseParts []string
	sseTools map[int]*struct {
		ID        string
		Type      string
		Name      string
		Arguments string
	}
	sseFinishReason string
	sseUsage        *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
}

func newOpenAIChatExtractor() *openaiChatExtractor {
	return &openAIChatExtractor{}
}

func (e *openaiChatExtractor) processSSE(payload []byte) {
	var v struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Usage   *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			PromptDetails    struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Choices []struct {
			Index        int    `json:"index"`
			FinishReason string `json:"finish_reason"`
			Delta        struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.ID != "" {
		e.sseID = v.ID
	}
	if v.Model != "" {
		e.mdl = v.Model
	}
	if v.Usage != nil && v.Usage.TotalTokens > 0 {
		e.acc = minimalUsage{
			PromptTokens:     v.Usage.PromptTokens,
			CompletionTokens: v.Usage.CompletionTokens,
			TotalTokens:      v.Usage.TotalTokens,
			ReasoningTokens:  v.Usage.CompletionDetails.ReasoningTokens,
			CachedTokens:     v.Usage.PromptDetails.CachedTokens,
		}
		e.sseUsage = &struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{
			PromptTokens:     v.Usage.PromptTokens,
			CompletionTokens: v.Usage.CompletionTokens,
			TotalTokens:      v.Usage.TotalTokens,
		}
	}
	for _, ch := range v.Choices {
		if ch.Delta.Role != "" && e.sseRole == "" {
			e.sseRole = ch.Delta.Role
		}
		if ch.Delta.Content != "" {
			e.sseParts = append(e.sseParts, ch.Delta.Content)
		}
		if ch.FinishReason != "" {
			e.sseFinishReason = ch.FinishReason
		}
		for _, tc := range ch.Delta.ToolCalls {
			if e.sseTools == nil {
				e.sseTools = make(map[int]*struct {
					ID        string
					Type      string
					Name      string
					Arguments string
				})
			}
			entry := e.sseTools[tc.Index]
			if entry == nil {
				entry = &struct {
					ID        string
					Type      string
					Name      string
					Arguments string
				}{}
				e.sseTools[tc.Index] = entry
			}
			if tc.ID != "" {
				entry.ID = tc.ID
			}
			if tc.Type != "" {
				entry.Type = tc.Type
			}
			if tc.Function.Name != "" {
				entry.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				entry.Arguments += tc.Function.Arguments
			}
		}
	}
}

func (e *openaiChatExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *openaiChatExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			PromptDetails    struct {
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

func (e *openaiChatExtractor) assembleSSE() []byte {
	if e.sseID == "" && len(e.sseParts) == 0 && e.sseRole == "" && len(e.sseTools) == 0 {
		return nil
	}
	content := ""
	for _, p := range e.sseParts {
		content += p
	}
	message := map[string]interface{}{
		"role":    e.sseRole,
		"content": content,
	}
	if len(e.sseTools) > 0 {
		toolCalls := make([]interface{}, len(e.sseTools))
		for idx, tc := range e.sseTools {
			toolCalls[idx] = map[string]interface{}{
				"id":   tc.ID,
				"type": tc.Type,
				"function": map[string]interface{}{
					"name":      tc.Name,
					"arguments": tc.Arguments,
				},
			}
		}
		message["tool_calls"] = toolCalls
	}
	result := map[string]interface{}{
		"id":    e.sseID,
		"model": e.mdl,
		"choices": []interface{}{
			map[string]interface{}{
				"index":         0,
				"message":       message,
				"finish_reason": e.sseFinishReason,
			},
		},
	}
	if e.sseUsage != nil {
		result["usage"] = e.sseUsage
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	return b
}

func init() {
	registerExtractor([]string{"openai_chat", "openai_completions"}, func() usageExtractor {
		return newOpenAIChatExtractor()
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway/ -run TestOpenAIChatAssemble -v`
Expected: PASS

- [ ] **Step 5: Run all existing tests**

Run: `go test ./internal/gateway/ -run TestOpenAIChat -v`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/usage_openai_chat.go internal/gateway/usage_openai_chat_test.go
git commit -m "feat(gateway): implement assembleSSE for OpenAI Chat extractor with delta accumulation"
```

---

### Task 4: Claude Messages — accumulate deltas and implement `assembleSSE()`

**Files:**
- Modify: `internal/gateway/usage_claude.go`
- Modify: `internal/gateway/usage_claude_test.go`

Claude SSE events:
- `message_start` → id, model, input usage
- `content_block_start` → content block type and index
- `content_block_delta` → text deltas
- `message_delta` → stop_reason, output usage

- [ ] **Step 1: Write the failing test**

Add to `usage_claude_test.go`:

```go
func TestClaudeAssembleSSE(t *testing.T) {
	e := newClaudeExtractor()
	e.processSSE([]byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-20250514","usage":{"input_tokens":50,"output_tokens":0}}}`))
	e.processSSE([]byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`))
	e.processSSE([]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`))
	e.processSSE([]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`))
	e.processSSE([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`))
	assembled := e.assembleSSE()
	if assembled == nil {
		t.Fatal("assembleSSE returned nil")
	}
	var v struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(assembled, &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if v.ID != "msg_1" {
		t.Fatalf("id=%q, want msg_1", v.ID)
	}
	if v.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("model=%q", v.Model)
	}
	if v.StopReason != "end_turn" {
		t.Fatalf("stop_reason=%q", v.StopReason)
	}
	if len(v.Content) != 1 {
		t.Fatalf("content len=%d", len(v.Content))
	}
	if v.Content[0].Text != "Hello world" {
		t.Fatalf("text=%q", v.Content[0].Text)
	}
	if v.Usage.InputTokens != 50 {
		t.Fatalf("input_tokens=%d", v.Usage.InputTokens)
	}
	if v.Usage.OutputTokens != 10 {
		t.Fatalf("output_tokens=%d", v.Usage.OutputTokens)
	}
}

func TestClaudeAssembleSSEEmptyStream(t *testing.T) {
	e := newClaudeExtractor()
	if assembled := e.assembleSSE(); assembled != nil {
		t.Fatalf("expected nil, got %q", string(assembled))
	}
}
```

Add `"encoding/json"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run TestClaudeAssemble -v`
Expected: FAIL

- [ ] **Step 3: Implement accumulation and `assembleSSE()` for claudeExtractor**

Rewrite `usage_claude.go`:

```go
package gateway

import "encoding/json"

type claudeExtractor struct {
	acc          minimalUsage
	mdl          string
	sseID        string
	sseStopReason string
	sseBlocks    []struct {
		Type string
		Text string
	}
}

func newClaudeExtractor() *claudeExtractor {
	return &claudeExtractor{}
}

func (e *claudeExtractor) processSSE(payload []byte) {
	var v struct {
		Type   string `json:"type"`
		Index  int    `json:"index"`
		Usage  struct {
			OutputTokens      int `json:"output_tokens"`
			CacheReadTokens   int `json:"cache_read_input_tokens"`
			CacheCreateTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Message struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				InputTokens       int `json:"input_tokens"`
				OutputTokens      int `json:"output_tokens"`
				CacheReadTokens   int `json:"cache_read_input_tokens"`
				CacheCreateTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		ContentBlock struct {
			Type string `json:"type"`
		} `json:"content_block"`
		DeltaText struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.Message.Usage.InputTokens > 0 {
		e.acc.PromptTokens = v.Message.Usage.InputTokens
		if e.acc.CachedTokens == 0 {
			e.acc.CachedTokens = v.Message.Usage.CacheReadTokens + v.Message.Usage.CacheCreateTokens
		}
		if v.Message.Model != "" {
			e.mdl = v.Message.Model
		}
		if v.Message.ID != "" {
			e.sseID = v.Message.ID
		}
	}
	if v.Usage.OutputTokens > 0 {
		e.acc.CompletionTokens = v.Usage.OutputTokens
	}
	e.acc.TotalTokens = e.acc.PromptTokens + e.acc.CompletionTokens
	if v.Type == "content_block_start" && v.ContentBlock.Type == "text" {
		e.sseBlocks = append(e.sseBlocks, struct {
			Type string
			Text string
		}{Type: "text"})
	}
	if v.Type == "content_block_delta" && v.DeltaText.Text != "" {
		if len(e.sseBlocks) > 0 {
			e.sseBlocks[len(e.sseBlocks)-1].Text += v.DeltaText.Text
		}
	}
	if v.Delta.StopReason != "" {
		e.sseStopReason = v.Delta.StopReason
	}
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

func (e *claudeExtractor) assembleSSE() []byte {
	if e.sseID == "" && len(e.sseBlocks) == 0 {
		return nil
	}
	content := make([]map[string]string, len(e.sseBlocks))
	for i, b := range e.sseBlocks {
		content[i] = map[string]string{"type": b.Type, "text": b.Text}
	}
	result := map[string]interface{}{
		"id":    e.sseID,
		"model": e.mdl,
		"content": content,
		"stop_reason": e.sseStopReason,
		"usage": map[string]int{
			"input_tokens":  e.acc.PromptTokens,
			"output_tokens": e.acc.CompletionTokens,
		},
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	return b
}

func init() {
	registerExtractor([]string{"claude_messages"}, func() usageExtractor {
		return newClaudeExtractor()
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway/ -run TestClaudeAssemble -v`
Expected: PASS

- [ ] **Step 5: Run all existing Claude tests**

Run: `go test ./internal/gateway/ -run TestClaude -v`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/usage_claude.go internal/gateway/usage_claude_test.go
git commit -m "feat(gateway): implement assembleSSE for Claude extractor with delta accumulation"
```

---

### Task 5: Wire `assembleSSE()` into `stream.go` and `proxy.go`

**Files:**
- Modify: `internal/gateway/stream.go`
- Modify: `internal/gateway/proxy.go`

Add `assembledResult()` method to `sseUsageExtractor` and change the evidence storage to use assembled JSON when available.

- [ ] **Step 1: Add `assembledResult()` method to `sseUsageExtractor` in `stream.go`**

Add after the `result()` method:

```go
func (e *sseUsageExtractor) assembledResult() []byte {
	return e.ext.assembleSSE()
}
```

- [ ] **Step 2: Modify `serveStreamingResponse` in `proxy.go` to use assembled evidence**

Replace the evidence storage section (lines ~738-772 in the current file). The key change: use a `bytes.Buffer` instead of `io.Pipe` for capture, then write assembled (or raw) to evidence store after stream ends.

Replace the section from `var responseObject evidence.Object` through the closing of `storeDone` with:

```go
	var responseObject evidence.Object
	var responseErr error
	var captureErr error
	var storeErr error
	clientWriter := flushWriter{writer: w}
	if flusher, ok := w.(http.Flusher); ok {
		clientWriter.flusher = flusher
	}
	usageExtractor := newSSEUsageExtractor(clientWriter, extractorFor(record.entry.ProtocolFamily))

	var captureBuf bytes.Buffer
	if h.EvidenceStore == nil {
		written, responseErr, captureErr = copyStreamToClientAndCapture(upstreamResp.Body, usageExtractor, nil)
	} else {
		written, responseErr, captureErr = copyStreamToClientAndCapture(upstreamResp.Body, usageExtractor, &captureBuf)
		assembled := usageExtractor.assembledResult()
		evidenceReader := io.Reader(&captureBuf)
		contentType := upstreamResp.Header.Get("Content-Type")
		if assembled != nil {
			evidenceReader = bytes.NewReader(assembled)
			contentType = "application/json"
		}
		storeCtx, cancelStore := context.WithCancel(context.WithoutCancel(req.Context()))
		defer cancelStore()
		object, err := h.EvidenceStore.Put(storeCtx, evidence.PutRequest{
			TraceID:     record.traceID,
			ObjectType:  "response_body",
			ContentType: contentType,
			Reader:      evidenceReader,
		})
		if err != nil {
			storeErr = err
		} else {
			responseObject = object
		}
	}
```

Ensure `"bytes"` is imported in `proxy.go` (it likely already is).

- [ ] **Step 3: Run tests to verify compilation and existing tests pass**

Run: `go test ./internal/gateway/... -v`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/stream.go internal/gateway/proxy.go
git commit -m "feat(gateway): wire assembleSSE into streaming pipeline for evidence storage"
```

---

### Task 6: Add integration test for assembled SSE in `stream_test.go`

**Files:**
- Modify: `internal/gateway/stream_test.go`

- [ ] **Step 1: Write integration test for Responses API assembly**

Add to `stream_test.go`:

```go
func TestSSEUsageExtractorResponsesAPIAssembled(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf, extractorFor("openai_responses"))
	sse := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.2\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\"}]}],\"usage\":{\"input_tokens\":100,\"output_tokens\":10,\"total_tokens\":110}}}\n\n"
	if _, err := ex.Write([]byte(sse)); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	assembled := ex.assembledResult()
	if assembled == nil {
		t.Fatal("assembledResult returned nil")
	}
	var v struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(assembled, &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if v.ID != "resp_1" {
		t.Fatalf("id=%q", v.ID)
	}
	if v.Usage.TotalTokens != 110 {
		t.Fatalf("total_tokens=%d", v.Usage.TotalTokens)
	}
	if v.Output[0].Content[0].Text != "hi" {
		t.Fatalf("text=%q", v.Output[0].Content[0].Text)
	}
	// Verify passthrough unchanged
	if buf.String() != sse {
		t.Fatalf("passthrough mismatch")
	}
}
```

Add `"encoding/json"` to the imports in `stream_test.go`.

- [ ] **Step 2: Write integration test for Chat Completions assembly**

```go
func TestSSEUsageExtractorChatCompletionsAssembled(t *testing.T) {
	var buf bytes.Buffer
	ex := newSSEUsageExtractor(&buf, extractorFor("openai_chat"))
	sse := "data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1,\"total_tokens\":6}}\n\ndata: [DONE]\n\n"
	if _, err := ex.Write([]byte(sse)); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	assembled := ex.assembledResult()
	if assembled == nil {
		t.Fatal("assembledResult returned nil")
	}
	var v struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(assembled, &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if v.ID != "chatcmpl-1" {
		t.Fatalf("id=%q", v.ID)
	}
	if v.Choices[0].Message.Content != "hi" {
		t.Fatalf("content=%q", v.Choices[0].Message.Content)
	}
	if v.Usage.TotalTokens != 6 {
		t.Fatalf("total_tokens=%d", v.Usage.TotalTokens)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/gateway/ -run TestSSEUsageExtractor -v`
Expected: ALL PASS (including new and existing tests)

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/stream_test.go
git commit -m "test(gateway): add integration tests for SSE response assembly"
```

---

### Task 7: Update e2e test to verify streaming evidence is JSON

**Files:**
- Modify: `e2e/test_gateway_capture.py`

- [ ] **Step 1: Add JSON validation helper and assertion for streaming traces**

No changes needed. The existing e2e assertions already check `usage_total_tokens > 0` and `model_upstream` non-empty for streaming traces. If the assembly works, these will pass. If it doesn't (and the normalizer can't parse raw SSE for that format), they'll fail.

- [ ] **Step 2: Commit (if any changes were made)**

Only commit if actual changes were made. If no changes needed, skip.

- [ ] **Step 3: Run all tests**

Run: `go test ./... && cd workers/analysis_worker && uv run pytest -q`
Expected: ALL PASS

---

### Task 8: Final verification

- [ ] **Step 1: Run full Go test suite**

Run: `go test ./...`
Expected: ALL PASS

- [ ] **Step 2: Run Python worker tests**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: ALL PASS

- [ ] **Step 3: Commit any remaining changes**

```bash
git add -A
git commit -m "chore: final cleanup for SSE response assembly feature"
```
