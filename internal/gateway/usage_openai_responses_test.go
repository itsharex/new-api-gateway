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
	e.processSSE([]byte(`{"type":"response.created","response":{"id":"resp_1","object":"response"}}`))
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
