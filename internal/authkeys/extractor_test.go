package authkeys

import (
	"net/http"
	"testing"
)

func TestExtractCanonicalKeyFromSupportedSources(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*http.Request)
		url      string
		wantKey  string
		wantFrom Source
	}{
		{
			name: "authorization bearer",
			setup: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer sk-abc123-extra")
			},
			wantKey:  "abc123-extra",
			wantFrom: SourceAuthorization,
		},
		{
			name: "anthropic x-api-key",
			setup: func(req *http.Request) {
				req.Header.Set("x-api-key", "sk-claude123-extra")
			},
			wantKey:  "claude123-extra",
			wantFrom: SourceAnthropic,
		},
		{
			name:     "gemini query key",
			url:      "/v1beta/models/gemini:generateContent?key=sk-gemini123-extra",
			wantKey:  "gemini123-extra",
			wantFrom: SourceGeminiQuery,
		},
		{
			name: "gemini x-goog-api-key",
			setup: func(req *http.Request) {
				req.Header.Set("x-goog-api-key", "sk-google123-extra")
			},
			wantKey:  "google123-extra",
			wantFrom: SourceGeminiHeader,
		},
		{
			name: "midjourney mj-api-secret",
			setup: func(req *http.Request) {
				req.Header.Set("mj-api-secret", "sk-mj123-extra")
			},
			wantKey:  "mj123-extra",
			wantFrom: SourceMidjourney,
		},
		{
			name: "realtime websocket protocol",
			setup: func(req *http.Request) {
				req.Header.Set("Sec-WebSocket-Protocol", "realtime, openai-insecure-api-key.sk-real123-extra, openai-beta.realtime-v1")
			},
			wantKey:  "real123-extra",
			wantFrom: SourceRealtime,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.url
			if url == "" {
				url = "/v1/test"
			}
			req, _ := http.NewRequest(http.MethodPost, url, nil)
			if tt.setup != nil {
				tt.setup(req)
			}

			result, ok := Extract(req)
			if !ok {
				t.Fatal("expected key")
			}
			if result.CanonicalKey != tt.wantKey {
				t.Fatalf("CanonicalKey = %q, want %q", result.CanonicalKey, tt.wantKey)
			}
			if result.Source != tt.wantFrom {
				t.Fatalf("Source = %q, want %q", result.Source, tt.wantFrom)
			}
		})
	}
}

func TestExtractAuthorizationBearerSchemeIsCaseInsensitive(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "bEaReR sk-mixed-case")

	result, ok := Extract(req)
	if !ok {
		t.Fatal("expected key")
	}
	if result.CanonicalKey != "mixed-case" {
		t.Fatalf("CanonicalKey = %q", result.CanonicalKey)
	}
	if result.Source != SourceAuthorization {
		t.Fatalf("Source = %q", result.Source)
	}
}

func TestExtractPreservesDistinctHyphenatedKeys(t *testing.T) {
	firstReq, _ := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	firstReq.Header.Set("x-api-key", "sk-team-alpha-prod")
	secondReq, _ := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	secondReq.Header.Set("x-api-key", "sk-team-alpha-dev")

	first, ok := Extract(firstReq)
	if !ok {
		t.Fatal("expected first key")
	}
	second, ok := Extract(secondReq)
	if !ok {
		t.Fatal("expected second key")
	}
	if first.CanonicalKey != "team-alpha-prod" {
		t.Fatalf("first CanonicalKey = %q", first.CanonicalKey)
	}
	if second.CanonicalKey != "team-alpha-dev" {
		t.Fatalf("second CanonicalKey = %q", second.CanonicalKey)
	}
	if first.CanonicalKey == second.CanonicalKey {
		t.Fatalf("distinct hyphenated keys collided: %q", first.CanonicalKey)
	}
}

func TestExtractSkipsInvalidOrEmptySourcesAndFallsBack(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*http.Request)
		url      string
		wantKey  string
		wantFrom Source
		wantOK   bool
	}{
		{
			name: "invalid authorization scheme falls back to x-api-key",
			setup: func(req *http.Request) {
				req.Header.Set("Authorization", "Basic sk-invalid")
				req.Header.Set("x-api-key", "sk-fallback-anthropic")
			},
			wantKey:  "fallback-anthropic",
			wantFrom: SourceAnthropic,
			wantOK:   true,
		},
		{
			name: "empty authorization bearer falls back to x-api-key",
			setup: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer sk-")
				req.Header.Set("x-api-key", "sk-fallback-empty-auth")
			},
			wantKey:  "fallback-empty-auth",
			wantFrom: SourceAnthropic,
			wantOK:   true,
		},
		{
			name: "whitespace authorization falls back to x-api-key",
			setup: func(req *http.Request) {
				req.Header.Set("Authorization", "   ")
				req.Header.Set("x-api-key", "sk-fallback-blank-auth")
			},
			wantKey:  "fallback-blank-auth",
			wantFrom: SourceAnthropic,
			wantOK:   true,
		},
		{
			name: "invalid authorization without fallback returns false",
			setup: func(req *http.Request) {
				req.Header.Set("Authorization", "Basic sk-invalid")
			},
			wantOK: false,
		},
		{
			name: "empty x-api-key falls back to gemini query",
			setup: func(req *http.Request) {
				req.Header.Set("x-api-key", "   ")
			},
			url:      "/v1beta/models/gemini:generateContent?key=sk-fallback-query",
			wantKey:  "fallback-query",
			wantFrom: SourceGeminiQuery,
			wantOK:   true,
		},
		{
			name: "empty gemini query falls back to x-goog-api-key",
			setup: func(req *http.Request) {
				req.Header.Set("x-goog-api-key", "sk-fallback-google")
			},
			url:      "/v1beta/models/gemini:generateContent?key=sk-",
			wantKey:  "fallback-google",
			wantFrom: SourceGeminiHeader,
			wantOK:   true,
		},
		{
			name: "empty x-goog-api-key falls back to mj-api-secret",
			setup: func(req *http.Request) {
				req.Header.Set("x-goog-api-key", "sk-")
				req.Header.Set("mj-api-secret", "sk-fallback-mj")
			},
			wantKey:  "fallback-mj",
			wantFrom: SourceMidjourney,
			wantOK:   true,
		},
		{
			name: "empty mj-api-secret falls back to realtime",
			setup: func(req *http.Request) {
				req.Header.Set("mj-api-secret", " \t ")
				req.Header.Set("Sec-WebSocket-Protocol", "realtime, openai-insecure-api-key.sk-fallback-real")
			},
			wantKey:  "fallback-real",
			wantFrom: SourceRealtime,
			wantOK:   true,
		},
		{
			name: "empty realtime protocol key returns false",
			setup: func(req *http.Request) {
				req.Header.Set("Sec-WebSocket-Protocol", "realtime, openai-insecure-api-key.sk-, openai-beta.realtime-v1")
			},
			wantOK: false,
		},
		{
			name: "empty realtime protocol key skips to later valid protocol key",
			setup: func(req *http.Request) {
				req.Header.Set("Sec-WebSocket-Protocol", "openai-insecure-api-key.sk-, openai-insecure-api-key.sk-real-later")
			},
			wantKey:  "real-later",
			wantFrom: SourceRealtime,
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.url
			if url == "" {
				url = "/v1/test"
			}
			req, _ := http.NewRequest(http.MethodPost, url, nil)
			if tt.setup != nil {
				tt.setup(req)
			}

			result, ok := Extract(req)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v; result = %#v", ok, tt.wantOK, result)
			}
			if !tt.wantOK {
				return
			}
			if result.CanonicalKey != tt.wantKey {
				t.Fatalf("CanonicalKey = %q, want %q", result.CanonicalKey, tt.wantKey)
			}
			if result.Source != tt.wantFrom {
				t.Fatalf("Source = %q, want %q", result.Source, tt.wantFrom)
			}
		})
	}
}

func TestExtractSourcePrecedence(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*http.Request)
		url      string
		wantKey  string
		wantFrom Source
	}{
		{
			name: "authorization wins over later sources",
			setup: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer sk-auth-key")
				req.Header.Set("x-api-key", "sk-anthropic-key")
				req.Header.Set("x-goog-api-key", "sk-google-key")
			},
			url:      "/v1beta/models/gemini:generateContent?key=sk-query-key",
			wantKey:  "auth-key",
			wantFrom: SourceAuthorization,
		},
		{
			name: "x-api-key wins over gemini query",
			setup: func(req *http.Request) {
				req.Header.Set("x-api-key", "sk-anthropic-key")
			},
			url:      "/v1beta/models/gemini:generateContent?key=sk-query-key",
			wantKey:  "anthropic-key",
			wantFrom: SourceAnthropic,
		},
		{
			name: "gemini query wins over gemini header",
			setup: func(req *http.Request) {
				req.Header.Set("x-goog-api-key", "sk-google-key")
			},
			url:      "/v1beta/models/gemini:generateContent?key=sk-query-key",
			wantKey:  "query-key",
			wantFrom: SourceGeminiQuery,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, tt.url, nil)
			tt.setup(req)

			result, ok := Extract(req)
			if !ok {
				t.Fatal("expected key")
			}
			if result.CanonicalKey != tt.wantKey {
				t.Fatalf("CanonicalKey = %q, want %q", result.CanonicalKey, tt.wantKey)
			}
			if result.Source != tt.wantFrom {
				t.Fatalf("Source = %q, want %q", result.Source, tt.wantFrom)
			}
		})
	}
}

func TestExtractReturnsFalseWhenMissing(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, ok := Extract(req)
	if ok {
		t.Fatal("expected no key")
	}
}
