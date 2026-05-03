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
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return minimalUsage{}, ""
	}
	u := minimalUsage{
		PromptTokens:     payload.Usage.PromptTokens,
		CompletionTokens: payload.Usage.CompletionTokens,
		TotalTokens:      payload.Usage.TotalTokens,
	}
	return u, payload.Model
}

func (e *genericExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func init() {
	registerExtractor([]string{"_generic"}, func() usageExtractor {
		return newGenericExtractor()
	})
}
