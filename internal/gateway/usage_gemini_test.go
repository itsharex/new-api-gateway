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
