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
			PromptTokens     *int `json:"prompt_tokens"`
			CompletionTokens *int `json:"completion_tokens"`
			InputTokens      *int `json:"input_tokens"`
			OutputTokens     *int `json:"output_tokens"`
			TotalTokens      int  `json:"total_tokens"`
			PromptDetails    struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputDetails struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens *int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
			OutputDetails struct {
				ReasoningTokens *int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return minimalUsage{}, ""
	}
	var promptTokens int
	if payload.Usage.PromptTokens != nil {
		promptTokens = *payload.Usage.PromptTokens
	} else if payload.Usage.InputTokens != nil {
		promptTokens = *payload.Usage.InputTokens
	}
	var completionTokens int
	if payload.Usage.CompletionTokens != nil {
		completionTokens = *payload.Usage.CompletionTokens
	} else if payload.Usage.OutputTokens != nil {
		completionTokens = *payload.Usage.OutputTokens
	}
	var cachedTokens int
	if payload.Usage.PromptDetails.CachedTokens != nil {
		cachedTokens = *payload.Usage.PromptDetails.CachedTokens
	} else if payload.Usage.InputDetails.CachedTokens != nil {
		cachedTokens = *payload.Usage.InputDetails.CachedTokens
	}
	var reasoningTokens int
	if payload.Usage.CompletionDetails.ReasoningTokens != nil {
		reasoningTokens = *payload.Usage.CompletionDetails.ReasoningTokens
	} else if payload.Usage.OutputDetails.ReasoningTokens != nil {
		reasoningTokens = *payload.Usage.OutputDetails.ReasoningTokens
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
