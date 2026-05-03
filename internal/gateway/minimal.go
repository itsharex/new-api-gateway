package gateway

import "strings"

type minimalUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ReasoningTokens  int
	CachedTokens     int
}

func extractRequestModel(path string, body []byte) string {
	if model := modelFromEngineEmbeddingPath(path); model != "" {
		return model
	}
	return extractModelFromBody(body)
}

func modelFromEngineEmbeddingPath(path string) string {
	const prefix = "/v1/engines/"
	const suffix = "/embeddings"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	model := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if model == "" || strings.Contains(model, "/") {
		return ""
	}
	return model
}
