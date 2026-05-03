package gateway

import (
	"encoding/json"
	"testing"
)

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

func TestOpenAIResponsesAssembleSSE(t *testing.T) {
	e := newOpenAIResponsesExtractor()
	e.processSSE([]byte(`{"type":"response.created","response":{"id":"resp_1","object":"response"}}`))
	e.processSSE([]byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.2","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}],"usage":{"input_tokens":100,"output_tokens":50,"total_tokens":150}}}`))
	assembled := e.assembleSSE()
	if assembled == nil {
		t.Fatal("assembleSSE returned nil")
	}
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
