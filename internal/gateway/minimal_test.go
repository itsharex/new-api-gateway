package gateway

import "testing"

func TestExtractRequestModelFromJSONBody(t *testing.T) {
	got := extractRequestModel("/v1/chat/completions", []byte(`{"model":"gpt-test","messages":[]}`))
	if got != "gpt-test" {
		t.Fatalf("model = %q", got)
	}
}

func TestExtractRequestModelFromEngineEmbeddingPath(t *testing.T) {
	got := extractRequestModel("/v1/engines/text-embedding-3-small/embeddings", []byte(`{"input":"hello"}`))
	if got != "text-embedding-3-small" {
		t.Fatalf("model = %q", got)
	}
}
