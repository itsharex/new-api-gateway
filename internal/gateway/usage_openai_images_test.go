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
	body := []byte(`{"created":0,"data":[{"b64_json":"...","url":"..."}],"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300,"input_tokens_details":{"image_tokens":20,"text_tokens":80},"output_tokens_details":{"image_tokens":180,"text_tokens":20}}}`)
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
	_ = m // images response has no model field
}

func TestOpenAIImagesExtractResponseNoUsage(t *testing.T) {
	e := newOpenAIImagesExtractor()
	body := []byte(`{"created":0,"data":[{"url":"..."}]}`)
	u, _ := e.extractResponse(body)
	if u != (minimalUsage{}) {
		t.Fatalf("got usage=%+v, want zero", u)
	}
}
