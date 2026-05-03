package gateway

import "encoding/json"

type openaiChatExtractor struct {
	acc      minimalUsage
	mdl      string
	sseID    string
	sseRole  string
	sseParts []string
	sseTools map[int]*struct {
		ID        string
		Type      string
		Name      string
		Arguments string
	}
	sseFinishReason string
	sseUsage        *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
}

func newOpenAIChatExtractor() *openaiChatExtractor {
	return &openaiChatExtractor{}
}

func (e *openaiChatExtractor) processSSE(payload []byte) {
	var v struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
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
		Choices []struct {
			Index        int    `json:"index"`
			FinishReason string `json:"finish_reason"`
			Delta        struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.ID != "" {
		e.sseID = v.ID
	}
	if v.Model != "" {
		e.mdl = v.Model
	}
	if v.Usage != nil && v.Usage.TotalTokens > 0 {
		e.acc = minimalUsage{
			PromptTokens:     v.Usage.PromptTokens,
			CompletionTokens: v.Usage.CompletionTokens,
			TotalTokens:      v.Usage.TotalTokens,
			ReasoningTokens:  v.Usage.CompletionDetails.ReasoningTokens,
			CachedTokens:     v.Usage.PromptDetails.CachedTokens,
		}
		e.sseUsage = &struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{
			PromptTokens:     v.Usage.PromptTokens,
			CompletionTokens: v.Usage.CompletionTokens,
			TotalTokens:      v.Usage.TotalTokens,
		}
	}
	for _, ch := range v.Choices {
		if ch.Delta.Role != "" && e.sseRole == "" {
			e.sseRole = ch.Delta.Role
		}
		if ch.Delta.Content != "" {
			e.sseParts = append(e.sseParts, ch.Delta.Content)
		}
		if ch.FinishReason != "" {
			e.sseFinishReason = ch.FinishReason
		}
		for _, tc := range ch.Delta.ToolCalls {
			if e.sseTools == nil {
				e.sseTools = make(map[int]*struct {
					ID        string
					Type      string
					Name      string
					Arguments string
				})
			}
			entry := e.sseTools[tc.Index]
			if entry == nil {
				entry = &struct {
					ID        string
					Type      string
					Name      string
					Arguments string
				}{}
				e.sseTools[tc.Index] = entry
			}
			if tc.ID != "" {
				entry.ID = tc.ID
			}
			if tc.Type != "" {
				entry.Type = tc.Type
			}
			if tc.Function.Name != "" {
				entry.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				entry.Arguments += tc.Function.Arguments
			}
		}
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
	if e.sseID == "" && len(e.sseParts) == 0 && e.sseRole == "" && len(e.sseTools) == 0 {
		return nil
	}
	content := ""
	for _, p := range e.sseParts {
		content += p
	}
	message := map[string]interface{}{
		"role":    e.sseRole,
		"content": content,
	}
	if len(e.sseTools) > 0 {
		toolCalls := make([]interface{}, len(e.sseTools))
		for idx, tc := range e.sseTools {
			toolCalls[idx] = map[string]interface{}{
				"id":   tc.ID,
				"type": tc.Type,
				"function": map[string]interface{}{
					"name":      tc.Name,
					"arguments": tc.Arguments,
				},
			}
		}
		message["tool_calls"] = toolCalls
	}
	result := map[string]interface{}{
		"id":    e.sseID,
		"model": e.mdl,
		"choices": []interface{}{
			map[string]interface{}{
				"index":         0,
				"message":       message,
				"finish_reason": e.sseFinishReason,
			},
		},
	}
	if e.sseUsage != nil {
		result["usage"] = e.sseUsage
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	return b
}

func init() {
	registerExtractor([]string{"openai_chat", "openai_completions"}, func() usageExtractor {
		return newOpenAIChatExtractor()
	})
}
