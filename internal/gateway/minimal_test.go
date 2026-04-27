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

func TestExtractOpenAIUsage(t *testing.T) {
	usage := extractResponseUsage([]byte(`{
		"usage": {
			"prompt_tokens": 11,
			"completion_tokens": 7,
			"total_tokens": 18,
			"prompt_tokens_details": {"cached_tokens": 3},
			"completion_tokens_details": {"reasoning_tokens": 2}
		}
	}`))
	if usage.PromptTokens != 11 || usage.CompletionTokens != 7 || usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.CachedTokens != 3 || usage.ReasoningTokens != 2 {
		t.Fatalf("usage details = %+v", usage)
	}
}

func TestExtractAnthropicUsage(t *testing.T) {
	usage := extractResponseUsage([]byte(`{
		"usage": {
			"input_tokens": 5,
			"output_tokens": 9,
			"cache_read_input_tokens": 4,
			"cache_creation_input_tokens": 2
		}
	}`))
	if usage.PromptTokens != 5 || usage.CompletionTokens != 9 || usage.TotalTokens != 14 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.CachedTokens != 6 {
		t.Fatalf("CachedTokens = %d, want 6", usage.CachedTokens)
	}
}

func TestExtractResponseUsageReturnsZeroForInvalidJSON(t *testing.T) {
	usage := extractResponseUsage([]byte(`not-json`))
	if usage != (minimalUsage{}) {
		t.Fatalf("usage = %+v, want zero", usage)
	}
}
