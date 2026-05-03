package gateway

import "encoding/json"

type openaiResponsesExtractor struct {
	acc          minimalUsage
	mdl          string
	rawCompleted json.RawMessage
}

func newOpenAIResponsesExtractor() *openaiResponsesExtractor {
	return &openaiResponsesExtractor{}
}

func (e *openaiResponsesExtractor) processSSE(payload []byte) {
	// Capture raw response from response.completed
	var header struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(payload, &header) != nil {
		return
	}
	if header.Type == "response.completed" && len(header.Response) > 0 {
		e.rawCompleted = header.Response
	}

	// Existing usage extraction
	var v struct {
		Response struct {
			Model string `json:"model"`
			Usage struct {
				InputTokens   int `json:"input_tokens"`
				OutputTokens  int `json:"output_tokens"`
				TotalTokens   int `json:"total_tokens"`
				InputDetails  struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputDetails struct {
					ReasoningTokens int `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	ru := v.Response.Usage
	if ru.TotalTokens > 0 {
		e.acc = minimalUsage{
			PromptTokens:    ru.InputTokens,
			CompletionTokens: ru.OutputTokens,
			TotalTokens:     ru.TotalTokens,
			ReasoningTokens: ru.OutputDetails.ReasoningTokens,
			CachedTokens:    ru.InputDetails.CachedTokens,
		}
	}
	if v.Response.Model != "" {
		e.mdl = v.Response.Model
	}
}

func (e *openaiResponsesExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *openaiResponsesExtractor) assembleSSE() []byte {
	if len(e.rawCompleted) == 0 {
		return nil
	}
	return e.rawCompleted
}

func (e *openaiResponsesExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens   int `json:"input_tokens"`
			OutputTokens  int `json:"output_tokens"`
			TotalTokens   int `json:"total_tokens"`
			InputDetails  struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &v) != nil {
		return minimalUsage{}, ""
	}
	return minimalUsage{
		PromptTokens:     v.Usage.InputTokens,
		CompletionTokens: v.Usage.OutputTokens,
		TotalTokens:      v.Usage.TotalTokens,
		ReasoningTokens:  v.Usage.OutputDetails.ReasoningTokens,
		CachedTokens:     v.Usage.InputDetails.CachedTokens,
	}, v.Model
}

func (e *openaiResponsesExtractor) extractRequest(_ string, body []byte) string {
	return extractModelFromBody(body)
}

func init() {
	registerExtractor([]string{"openai_responses"}, func() usageExtractor {
		return newOpenAIResponsesExtractor()
	})
}
