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

func TestDefaultRegistryMatchesApprovedMVPRoutes(t *testing.T) {
	tests := []struct {
		method       string
		path         string
		wantPattern  string
		wantProtocol string
		wantCapture  CaptureMode
	}{
		{"POST", "/v1/engines/text-embedding-3-small/embeddings", "/v1/engines/:model/embeddings", "embeddings", CaptureRawAndNormalized},
		{"POST", "/v1/video/generations", "/v1/video/generations", "video", CaptureRawAndMinimal},
		{"GET", "/v1/video/generations/task_123", "/v1/video/generations/:task_id", "video", CaptureRawAndMinimal},
		{"GET", "/v1/videos/task_123", "/v1/videos/:task_id", "video", CaptureRawAndMinimal},
		{"GET", "/v1/videos/task_123/content", "/v1/videos/:task_id/content", "video", CaptureRawAndMinimal},
		{"POST", "/v1/videos/video_123/remix", "/v1/videos/:video_id/remix", "video", CaptureRawAndMinimal},
		{"POST", "/kling/v1/videos/text2video", "/kling/v1/videos/text2video", "kling_video", CaptureRawAndMinimal},
		{"POST", "/kling/v1/videos/image2video", "/kling/v1/videos/image2video", "kling_video", CaptureRawAndMinimal},
		{"GET", "/kling/v1/videos/text2video/task_123", "/kling/v1/videos/text2video/:task_id", "kling_video", CaptureRawAndMinimal},
		{"GET", "/kling/v1/videos/image2video/task_123", "/kling/v1/videos/image2video/:task_id", "kling_video", CaptureRawAndMinimal},
		{"POST", "/jimeng/", "/jimeng/", "jimeng", CaptureRawAndMinimal},
		{"POST", "/relay/mj/submit/imagine", "/:mode/mj/*", "midjourney", CaptureRawAndMinimal},
		{"POST", "/suno/submit/music", "/suno/*", "suno", CaptureRawAndMinimal},
	}

	registry := DefaultRegistry()
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			entry, ok := registry.Match(tt.method, tt.path)
			if !ok {
				t.Fatalf("expected match for %s %s", tt.method, tt.path)
			}
			if entry.PathPattern != tt.wantPattern {
				t.Fatalf("PathPattern = %q, want %q", entry.PathPattern, tt.wantPattern)
			}
			if entry.ProtocolFamily != tt.wantProtocol {
				t.Fatalf("ProtocolFamily = %q, want %q", entry.ProtocolFamily, tt.wantProtocol)
			}
			if entry.CaptureMode != tt.wantCapture {
				t.Fatalf("CaptureMode = %q, want %q", entry.CaptureMode, tt.wantCapture)
			}
		})
	}
}

func TestRouteSegmentParametersRequireNonEmptySegment(t *testing.T) {
	registry := DefaultRegistry()
	if _, ok := registry.Match("GET", "/v1/videos//content"); ok {
		t.Fatal("expected no match for empty task id")
	}
	if _, ok := registry.Match("POST", "/relay/mj/"); ok {
		t.Fatal("expected no match for empty mj child path")
	}
}
