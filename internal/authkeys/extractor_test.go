package authkeys

import (
	"net/http"
	"testing"
)

func TestExtractCanonicalKeyFromAuthorization(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-abc123-extra")

	result, ok := Extract(req)
	if !ok {
		t.Fatal("expected key")
	}
	if result.CanonicalKey != "abc123" {
		t.Fatalf("CanonicalKey = %q", result.CanonicalKey)
	}
	if result.Source != SourceAuthorization {
		t.Fatalf("Source = %q", result.Source)
	}
}

func TestExtractCanonicalKeyFromClaudeHeader(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", "sk-claude123")

	result, ok := Extract(req)
	if !ok || result.CanonicalKey != "claude123" {
		t.Fatalf("result = %#v ok=%v", result, ok)
	}
}

func TestExtractCanonicalKeyFromGeminiQuery(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1beta/models/gemini:generateContent?key=sk-gemini123", nil)

	result, ok := Extract(req)
	if !ok || result.CanonicalKey != "gemini123" {
		t.Fatalf("result = %#v ok=%v", result, ok)
	}
}

func TestExtractCanonicalKeyFromRealtimeProtocol(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/v1/realtime", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "realtime, openai-insecure-api-key.sk-real123, openai-beta.realtime-v1")

	result, ok := Extract(req)
	if !ok || result.CanonicalKey != "real123" {
		t.Fatalf("result = %#v ok=%v", result, ok)
	}
}

func TestExtractReturnsFalseWhenMissing(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, ok := Extract(req)
	if ok {
		t.Fatal("expected no key")
	}
}
