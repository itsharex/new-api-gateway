package gateway

import "encoding/json"

type claudeExtractor struct {
	acc minimalUsage
	mdl string
}

func newClaudeExtractor() *claudeExtractor {
	return &claudeExtractor{}
}

func (e *claudeExtractor) processSSE(payload []byte) {
	var v struct {
		Type   string `json:"type"`
		Usage  struct {
			OutputTokens      int `json:"output_tokens"`
			CacheReadTokens   int `json:"cache_read_input_tokens"`
			CacheCreateTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Message struct {
			Model string `json:"model"`
			Usage struct {
				InputTokens       int `json:"input_tokens"`
				OutputTokens      int `json:"output_tokens"`
				CacheReadTokens   int `json:"cache_read_input_tokens"`
				CacheCreateTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.Message.Usage.InputTokens > 0 {
		e.acc.PromptTokens = v.Message.Usage.InputTokens
		if e.acc.CachedTokens == 0 {
			e.acc.CachedTokens = v.Message.Usage.CacheReadTokens + v.Message.Usage.CacheCreateTokens
		}
		if v.Message.Model != "" {
			e.mdl = v.Message.Model
		}
	}
	if v.Usage.OutputTokens > 0 {
		e.acc.CompletionTokens = v.Usage.OutputTokens
	}
	e.acc.TotalTokens = e.acc.PromptTokens + e.acc.CompletionTokens
}

func (e *claudeExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *claudeExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens       int `json:"input_tokens"`
			OutputTokens      int `json:"output_tokens"`
			CacheReadTokens   int `json:"cache_read_input_tokens"`
			CacheCreateTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &v) != nil {
		return minimalUsage{}, ""
	}
	u := minimalUsage{
		PromptTokens:     v.Usage.InputTokens,
		CompletionTokens: v.Usage.OutputTokens,
		TotalTokens:      v.Usage.InputTokens + v.Usage.OutputTokens,
		CachedTokens:     v.Usage.CacheReadTokens + v.Usage.CacheCreateTokens,
	}
	return u, v.Model
}

func (e *claudeExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func (e *claudeExtractor) assembleSSE() []byte {
	return nil
}

func init() {
	registerExtractor([]string{"claude_messages"}, func() usageExtractor {
		return newClaudeExtractor()
	})
}
