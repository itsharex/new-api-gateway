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
	body := []byte(`{"created":0,"data":[{"b64_json":"...","url":"..."}],"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300,"input_tokens_details":{"cached_tokens":40,"image_tokens":20,"text_tokens":80},"output_tokens_details":{"image_tokens":180,"text_tokens":20}}}`)
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
	if u.CachedTokens != 40 {
		t.Fatalf("CachedTokens=%d, want 40", u.CachedTokens)
	}
	_ = m // images response has no model field
}

func TestOpenAIImagesProcessSSECachedTokens(t *testing.T) {
	e := newOpenAIImagesExtractor()
	e.processSSE([]byte(`{"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300,"input_tokens_details":{"cached_tokens":40}}}`))
	u, _ := e.sseResult()
	if u.PromptTokens != 100 || u.CompletionTokens != 200 || u.TotalTokens != 300 {
		t.Fatalf("usage=%+v", u)
	}
	if u.CachedTokens != 40 {
		t.Fatalf("CachedTokens=%d, want 40", u.CachedTokens)
	}
}

func TestOpenAIImagesExtractResponseNoUsage(t *testing.T) {
	e := newOpenAIImagesExtractor()
	body := []byte(`{"created":0,"data":[{"url":"..."}]}`)
	u, _ := e.extractResponse(body)
	if u != (minimalUsage{}) {
		t.Fatalf("got usage=%+v, want zero", u)
	}
}
