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
	e.processSSE([]byte(`{"id":"1","model":"gpt-4o"}`))
	u, m := e.sseResult()
	if m != "gpt-4o" {
		t.Fatalf("model=%q, want gpt-4o", m)
	}
	if u.TotalTokens != 0 {
		t.Fatalf("TotalTokens=%d, want 0", u.TotalTokens)
	}
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
