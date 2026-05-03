package gateway

import "encoding/json"

type openaiImagesExtractor struct {
	acc minimalUsage
	mdl string
}

func newOpenAIImagesExtractor() *openaiImagesExtractor {
	return &openaiImagesExtractor{}
}

func (e *openaiImagesExtractor) processSSE(payload []byte) {
	var v struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.Usage.TotalTokens > 0 {
		e.acc = minimalUsage{
			PromptTokens:     v.Usage.InputTokens,
			CompletionTokens: v.Usage.OutputTokens,
			TotalTokens:      v.Usage.TotalTokens,
		}
	}
}

func (e *openaiImagesExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *openaiImagesExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &v) != nil {
		return minimalUsage{}, ""
	}
	return minimalUsage{
		PromptTokens:     v.Usage.InputTokens,
		CompletionTokens: v.Usage.OutputTokens,
		TotalTokens:      v.Usage.TotalTokens,
	}, ""
}

func (e *openaiImagesExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func (e *openaiImagesExtractor) assembleSSE() []byte { return nil }

func init() {
	registerExtractor([]string{"openai_images"}, func() usageExtractor {
		return newOpenAIImagesExtractor()
	})
}
