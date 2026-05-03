package gateway

import (
	"encoding/json"
	"testing"
)

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
