package gateway

import "encoding/json"

type openaiChatExtractor struct {
	acc minimalUsage
	mdl string
}

func newOpenAIChatExtractor() *openaiChatExtractor {
	return &openaiChatExtractor{}
}

func (e *openaiChatExtractor) processSSE(payload []byte) {
	var v struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			PromptDetails    struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.Usage.TotalTokens > 0 {
		e.acc = minimalUsage{
			PromptTokens:     v.Usage.PromptTokens,
			CompletionTokens: v.Usage.CompletionTokens,
			TotalTokens:      v.Usage.TotalTokens,
			ReasoningTokens:  v.Usage.CompletionDetails.ReasoningTokens,
			CachedTokens:     v.Usage.PromptDetails.CachedTokens,
		}
	}
	if v.Model != "" {
		e.mdl = v.Model
	}
}

func (e *openaiChatExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *openaiChatExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			PromptDetails    struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &v) != nil {
		return minimalUsage{}, ""
	}
	return minimalUsage{
		PromptTokens:     v.Usage.PromptTokens,
		CompletionTokens: v.Usage.CompletionTokens,
		TotalTokens:      v.Usage.TotalTokens,
		ReasoningTokens:  v.Usage.CompletionDetails.ReasoningTokens,
		CachedTokens:     v.Usage.PromptDetails.CachedTokens,
	}, v.Model
}

func (e *openaiChatExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func (e *openaiChatExtractor) assembleSSE() []byte {
	return nil
}

func init() {
	registerExtractor([]string{"openai_chat", "openai_completions"}, func() usageExtractor {
		return newOpenAIChatExtractor()
	})
}
