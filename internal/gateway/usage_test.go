package gateway

import (
	"reflect"
	"testing"
)

func TestExtractorForReturnsCorrectType(t *testing.T) {
	cases := map[string]string{
		"openai_chat":      "*gateway.openaiChatExtractor",
		"openai_completions": "*gateway.openaiChatExtractor",
		"openai_responses": "*gateway.openaiResponsesExtractor",
		"openai_images":    "*gateway.openaiImagesExtractor",
		"claude_messages":  "*gateway.claudeExtractor",
		"gemini":           "*gateway.geminiExtractor",
		"unknown":          "*gateway.genericExtractor",
		"":                 "*gateway.genericExtractor",
		"video":            "*gateway.genericExtractor",
		"embeddings":       "*gateway.genericExtractor",
	}
	for family, wantType := range cases {
		ext := extractorFor(family)
		if ext == nil {
			t.Fatalf("extractorFor(%q) returned nil", family)
		}
		gotType := reflect.TypeOf(ext).String()
		if gotType != wantType {
			t.Errorf("extractorFor(%q) = %s, want %s", family, gotType, wantType)
		}
	}
}

func TestExtractorForReturnsFreshInstance(t *testing.T) {
	a := extractorFor("openai_chat")
	b := extractorFor("openai_chat")
	if a == b {
		t.Fatal("extractorFor should return a new instance each call")
	}
}
