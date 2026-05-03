package gateway

import (
	"encoding/json"
	"testing"
)

func TestClaudeExtractRequest(t *testing.T) {
	e := newClaudeExtractor()
	got := e.extractRequest("/v1/messages", []byte(`{"model":"claude-sonnet-4-20250514","messages":[]}`))
	if got != "claude-sonnet-4-20250514" {
		t.Fatalf("got %q", got)
	}
}

func TestClaudeProcessSSEAccumulates(t *testing.T) {
	e := newClaudeExtractor()
	e.processSSE([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":100,"output_tokens":0}}}`))
	u, _ := e.sseResult()
	if u.PromptTokens != 100 {
		t.Fatalf("after message_start: PromptTokens=%d, want 100", u.PromptTokens)
	}
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

func TestClaudeProcessSSEExtractsModel(t *testing.T) {
	e := newClaudeExtractor()
	e.processSSE([]byte(`{"type":"message_start","message":{"model":"claude-sonnet-4-20250514","usage":{"input_tokens":100,"output_tokens":0}}}`))
	_, m := e.sseResult()
	if m != "claude-sonnet-4-20250514" {
		t.Fatalf("model=%q, want claude-sonnet-4-20250514", m)
	}
}

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
