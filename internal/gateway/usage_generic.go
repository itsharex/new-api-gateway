package gateway

import "encoding/json"

type genericExtractor struct {
	acc minimalUsage
	mdl string
}

func newGenericExtractor() *genericExtractor {
	return &genericExtractor{}
}

func (e *genericExtractor) processSSE(_ []byte) {
	// generic: no SSE parsing
}

func (e *genericExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *genericExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var payload struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			TotalTokens      int `json:"total_tokens"`
			PromptDetails    struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
			OutputDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return minimalUsage{}, ""
	}
	promptTokens := payload.Usage.PromptTokens
	if promptTokens == 0 {
		promptTokens = payload.Usage.InputTokens
	}
	completionTokens := payload.Usage.CompletionTokens
	if completionTokens == 0 {
		completionTokens = payload.Usage.OutputTokens
	}
	cachedTokens := payload.Usage.PromptDetails.CachedTokens
	if cachedTokens == 0 {
		cachedTokens = payload.Usage.InputDetails.CachedTokens
	}
	reasoningTokens := payload.Usage.CompletionDetails.ReasoningTokens
	if reasoningTokens == 0 {
		reasoningTokens = payload.Usage.OutputDetails.ReasoningTokens
	}
	u := minimalUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      payload.Usage.TotalTokens,
		ReasoningTokens:  reasoningTokens,
		CachedTokens:     cachedTokens,
	}
	return u, payload.Model
}

func (e *genericExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func (e *genericExtractor) assembleSSE() []byte { return nil }

func init() {
	registerExtractor([]string{"_generic"}, func() usageExtractor {
		return newGenericExtractor()
	})
}
