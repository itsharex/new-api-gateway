package routes

import "testing"

func TestDefaultRegistryMatchesImageGeneration(t *testing.T) {
	entry, ok := DefaultRegistry().Match("POST", "/v1/images/generations")
	if !ok {
		t.Fatal("expected route match")
	}
	if entry.ProtocolFamily != "openai_images" {
		t.Fatalf("ProtocolFamily = %q", entry.ProtocolFamily)
	}
	if entry.CaptureMode != CaptureRawAndNormalized {
		t.Fatalf("CaptureMode = %q", entry.CaptureMode)
	}
}

func TestDefaultRegistryMatchesMidjourneyRawMinimal(t *testing.T) {
	entry, ok := DefaultRegistry().Match("POST", "/mj/submit/imagine")
	if !ok {
		t.Fatal("expected route match")
	}
	if entry.CaptureMode != CaptureRawAndMinimal {
		t.Fatalf("CaptureMode = %q", entry.CaptureMode)
	}
}

func TestDefaultRegistryUnknownRoute(t *testing.T) {
	_, ok := DefaultRegistry().Match("POST", "/unknown/path")
	if ok {
		t.Fatal("expected no route match")
	}
}

func TestDefaultRegistryWildcardBoundaries(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		wantMatch      bool
		wantPattern    string
		wantProtocol   string
		wantNormalizer string
	}{
		{
			name:         "videos base path matches",
			method:       "POST",
			path:         "/v1/videos",
			wantMatch:    true,
			wantPattern:  "/v1/videos*",
			wantProtocol: "video",
		},
		{
			name:         "videos subtree matches",
			method:       "POST",
			path:         "/v1/videos/jobs/1",
			wantMatch:    true,
			wantPattern:  "/v1/videos*",
			wantProtocol: "video",
		},
		{
			name:      "videos byte prefix does not match",
			method:    "POST",
			path:      "/v1/videosXYZ",
			wantMatch: false,
		},
		{
			name:           "gemini v1beta child path matches",
			method:         "POST",
			path:           "/v1beta/models/gemini-pro:generateContent",
			wantMatch:      true,
			wantPattern:    "/v1beta/models/*",
			wantProtocol:   "gemini",
			wantNormalizer: "gemini_generate_content",
		},
		{
			name:           "gemini v1 child path matches",
			method:         "POST",
			path:           "/v1/models/gemini-pro:generateContent",
			wantMatch:      true,
			wantPattern:    "/v1/models/*",
			wantProtocol:   "gemini",
			wantNormalizer: "gemini_generate_content",
		},
		{
			name:         "gemini slash wildcard requires child path",
			method:       "POST",
			path:         "/v1beta/models",
			wantMatch:    false,
			wantProtocol: "gemini",
		},
		{
			name:         "gemini slash wildcard requires non-empty child path",
			method:       "POST",
			path:         "/v1beta/models/",
			wantMatch:    false,
			wantProtocol: "gemini",
		},
		{
			name:      "method mismatch does not match",
			method:    "GET",
			path:      "/v1/images/generations",
			wantMatch: false,
		},
		{
			name:      "method case is exact",
			method:    "post",
			path:      "/v1/images/generations",
			wantMatch: false,
		},
		{
			name:           "specific route keeps its normalizer",
			method:         "POST",
			path:           "/v1/responses/compact",
			wantMatch:      true,
			wantPattern:    "/v1/responses/compact",
			wantProtocol:   "openai_responses",
			wantNormalizer: "openai_responses_compact",
		},
	}

	registry := DefaultRegistry()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, ok := registry.Match(tt.method, tt.path)
			if ok != tt.wantMatch {
				t.Fatalf("Match(%q, %q) ok = %v, want %v", tt.method, tt.path, ok, tt.wantMatch)
			}
			if !tt.wantMatch {
				return
			}
			if entry.PathPattern != tt.wantPattern {
				t.Fatalf("PathPattern = %q, want %q", entry.PathPattern, tt.wantPattern)
			}
			if entry.ProtocolFamily != tt.wantProtocol {
				t.Fatalf("ProtocolFamily = %q, want %q", entry.ProtocolFamily, tt.wantProtocol)
			}
			if tt.wantNormalizer != "" && entry.Normalizer != tt.wantNormalizer {
				t.Fatalf("Normalizer = %q, want %q", entry.Normalizer, tt.wantNormalizer)
			}
		})
	}
}
