package gateway

import (
	"encoding/json"
	"strings"
)

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
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Model
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

func extractResponseUsage(body []byte) minimalUsage {
	var payload struct {
		Usage struct {
			PromptTokens      int `json:"prompt_tokens"`
			CompletionTokens  int `json:"completion_tokens"`
			TotalTokens       int `json:"total_tokens"`
			InputTokens       int `json:"input_tokens"`
			OutputTokens      int `json:"output_tokens"`
			CacheReadTokens   int `json:"cache_read_input_tokens"`
			CacheCreateTokens int `json:"cache_creation_input_tokens"`
			PromptDetails     struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return minimalUsage{}
	}

	usage := minimalUsage{
		PromptTokens:     payload.Usage.PromptTokens,
		CompletionTokens: payload.Usage.CompletionTokens,
		TotalTokens:      payload.Usage.TotalTokens,
		ReasoningTokens:  payload.Usage.CompletionDetails.ReasoningTokens,
		CachedTokens:     payload.Usage.PromptDetails.CachedTokens,
	}
	if usage.PromptTokens == 0 && payload.Usage.InputTokens > 0 {
		usage.PromptTokens = payload.Usage.InputTokens
	}
	if usage.CompletionTokens == 0 && payload.Usage.OutputTokens > 0 {
		usage.CompletionTokens = payload.Usage.OutputTokens
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if usage.CachedTokens == 0 {
		usage.CachedTokens = payload.Usage.CacheReadTokens + payload.Usage.CacheCreateTokens
	}
	return usage
}
