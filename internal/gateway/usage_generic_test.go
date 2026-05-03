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
