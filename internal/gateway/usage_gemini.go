package gateway

import (
	"encoding/json"
	"strings"
)

type geminiExtractor struct {
	acc minimalUsage
	mdl string
}

func newGeminiExtractor() *geminiExtractor {
	return &geminiExtractor{}
}

func (e *geminiExtractor) processSSE(payload []byte) {
	var v struct {
		UsageMetadata struct {
			PromptTokens     int `json:"promptTokenCount"`
			CandidatesTokens int `json:"candidatesTokenCount"`
			TotalTokens      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return
	}
	if v.UsageMetadata.TotalTokens > 0 {
		e.acc = minimalUsage{
			PromptTokens:     v.UsageMetadata.PromptTokens,
			CompletionTokens: v.UsageMetadata.CandidatesTokens,
			TotalTokens:      v.UsageMetadata.TotalTokens,
		}
	}
}

func (e *geminiExtractor) sseResult() (minimalUsage, string) {
	return e.acc, e.mdl
}

func (e *geminiExtractor) extractResponse(body []byte) (minimalUsage, string) {
	var v struct {
		UsageMetadata struct {
			PromptTokens     int `json:"promptTokenCount"`
			CandidatesTokens int `json:"candidatesTokenCount"`
			TotalTokens      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(body, &v) != nil {
		return minimalUsage{}, ""
	}
	return minimalUsage{
		PromptTokens:     v.UsageMetadata.PromptTokens,
		CompletionTokens: v.UsageMetadata.CandidatesTokens,
		TotalTokens:      v.UsageMetadata.TotalTokens,
	}, ""
}

func (e *geminiExtractor) extractRequest(path string, body []byte) string {
	if model := modelFromGeminiPath(path); model != "" {
		return model
	}
	return extractModelFromBody(body)
}

func (e *geminiExtractor) assembleSSE() []byte { return nil }

func modelFromGeminiPath(path string) string {
	idx := strings.Index(path, "/models/")
	if idx < 0 {
		return ""
	}
	after := path[idx+len("/models/"):]
	colon := strings.Index(after, ":")
	if colon > 0 {
		return after[:colon]
	}
	if colon == 0 {
		return ""
	}
	return after
}

func init() {
	registerExtractor([]string{"gemini"}, func() usageExtractor {
		return newGeminiExtractor()
	})
}
