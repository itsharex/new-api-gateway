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
