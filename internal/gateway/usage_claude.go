package gateway

import "encoding/json"

type claudeExtractor struct {
	acc           minimalUsage
	mdl           string
	sseID         string
	sseStopReason string
	sseBlocks     []struct {
		Type string
		Text string
	}
}

func newClaudeExtractor() *claudeExtractor {
	return &claudeExtractor{}
}

func (e *claudeExtractor) processSSE(payload []byte) {
	var v struct {
		Type   string `json:"type"`
		Index  int    `json:"index"`
		Usage  struct {
			OutputTokens      int `json:"output_tokens"`
			CacheReadTokens   int `json:"cache_read_input_tokens"`
			CacheCreateTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Message struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				InputTokens       int `json:"input_tokens"`
				OutputTokens      int `json:"output_tokens"`
				CacheReadTokens   int `json:"cache_read_input_tokens"`
				CacheCreateTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
		Delta struct {
			StopReason string `json:"stop_reason"`
			Type       string `json:"type"`
			Text       string `json:"text"`
		} `json:"delta"`
		ContentBlock struct {
			Type string `json:"type"`
		} `json:"content_block"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	// Existing usage extraction (unchanged behavior)
	if v.Message.Usage.InputTokens > 0 {
		e.acc.PromptTokens = v.Message.Usage.InputTokens
		if e.acc.CachedTokens == 0 {
			e.acc.CachedTokens = v.Message.Usage.CacheReadTokens + v.Message.Usage.CacheCreateTokens
		}
		if v.Message.Model != "" {
			e.mdl = v.Message.Model
		}
	}
	if v.Message.ID != "" {
		e.sseID = v.Message.ID
	}
	if v.Usage.OutputTokens > 0 {
		e.acc.CompletionTokens = v.Usage.OutputTokens
	}
	e.acc.TotalTokens = e.acc.PromptTokens + e.acc.CompletionTokens
	// New: content block accumulation
	if v.Type == "content_block_start" && v.ContentBlock.Type == "text" {
		e.sseBlocks = append(e.sseBlocks, struct {
			Type string
			Text string
		}{Type: "text"})
	}
	if v.Type == "content_block_delta" && v.Delta.Text != "" {
		if len(e.sseBlocks) > 0 {
			e.sseBlocks[len(e.sseBlocks)-1].Text += v.Delta.Text
		}
	}
	if v.Delta.StopReason != "" {
		e.sseStopReason = v.Delta.StopReason
	}
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
	if e.sseID == "" && len(e.sseBlocks) == 0 {
		return nil
	}
	content := make([]map[string]string, len(e.sseBlocks))
	for i, b := range e.sseBlocks {
		content[i] = map[string]string{"type": b.Type, "text": b.Text}
	}
	result := map[string]interface{}{
		"id":          e.sseID,
		"model":       e.mdl,
		"content":     content,
		"stop_reason": e.sseStopReason,
		"usage": map[string]int{
			"input_tokens":  e.acc.PromptTokens,
			"output_tokens": e.acc.CompletionTokens,
		},
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	return b
}

func init() {
	registerExtractor([]string{"claude_messages"}, func() usageExtractor {
		return newClaudeExtractor()
	})
}
