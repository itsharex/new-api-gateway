package routes

import "strings"

type CaptureMode string

const (
	CaptureRawAndNormalized CaptureMode = "raw_and_normalized"
	CaptureRawAndMinimal    CaptureMode = "raw_and_minimal"
	CaptureRawOnly          CaptureMode = "raw_only"
)

type Entry struct {
	Method               string
	PathPattern          string
	ProtocolFamily       string
	BodyKind             string
	CaptureMode          CaptureMode
	Normalizer           string
	MinimalExtractor     string
	UnsupportedAlertCode string
}

type Registry struct {
	entries []Entry
}

func DefaultRegistry() Registry {
	return Registry{entries: []Entry{
		{Method: "POST", PathPattern: "/v1/chat/completions", ProtocolFamily: "openai_chat", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_chat"},
		{Method: "POST", PathPattern: "/pg/chat/completions", ProtocolFamily: "openai_chat", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_chat"},
		{Method: "POST", PathPattern: "/v1/responses", ProtocolFamily: "openai_responses", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_responses"},
		{Method: "POST", PathPattern: "/v1/responses/compact", ProtocolFamily: "openai_responses", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_responses_compact"},
		{Method: "POST", PathPattern: "/v1/messages", ProtocolFamily: "claude_messages", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "claude_messages"},
		{Method: "POST", PathPattern: "/v1/completions", ProtocolFamily: "openai_completions", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_completions"},
		{Method: "POST", PathPattern: "/v1/embeddings", ProtocolFamily: "embeddings", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "embeddings"},
		{Method: "POST", PathPattern: "/v1/rerank", ProtocolFamily: "rerank", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "rerank"},
		{Method: "POST", PathPattern: "/v1/images/generations", ProtocolFamily: "openai_images", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_image_generation"},
		{Method: "POST", PathPattern: "/v1/images/edits", ProtocolFamily: "openai_images", BodyKind: "multipart_or_json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_image_edit"},
		{Method: "POST", PathPattern: "/v1/edits", ProtocolFamily: "openai_images", BodyKind: "multipart_or_json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_edit"},
		{Method: "POST", PathPattern: "/v1/audio/transcriptions", ProtocolFamily: "openai_audio", BodyKind: "multipart", CaptureMode: CaptureRawAndNormalized, Normalizer: "audio_transcription"},
		{Method: "POST", PathPattern: "/v1/audio/translations", ProtocolFamily: "openai_audio", BodyKind: "multipart", CaptureMode: CaptureRawAndNormalized, Normalizer: "audio_translation"},
		{Method: "POST", PathPattern: "/v1/audio/speech", ProtocolFamily: "openai_audio", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "audio_speech"},
		{Method: "POST", PathPattern: "/v1beta/models/*", ProtocolFamily: "gemini", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "gemini_generate_content"},
		{Method: "POST", PathPattern: "/v1/models/*", ProtocolFamily: "gemini", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "gemini_generate_content"},
		{Method: "GET", PathPattern: "/v1/realtime", ProtocolFamily: "realtime", BodyKind: "websocket", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "realtime_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/v1/engines/:model/embeddings", ProtocolFamily: "embeddings", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "embeddings"},
		{Method: "POST", PathPattern: "/v1/video/generations", ProtocolFamily: "video", BodyKind: "json_or_multipart", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "GET", PathPattern: "/v1/video/generations/:task_id", ProtocolFamily: "video", BodyKind: "none", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "GET", PathPattern: "/v1/videos/:task_id", ProtocolFamily: "video", BodyKind: "none", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "GET", PathPattern: "/v1/videos/:task_id/content", ProtocolFamily: "video", BodyKind: "none", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/v1/videos/:video_id/remix", ProtocolFamily: "video", BodyKind: "json_or_multipart", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/kling/v1/videos/text2video", ProtocolFamily: "kling_video", BodyKind: "json", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/kling/v1/videos/image2video", ProtocolFamily: "kling_video", BodyKind: "json_or_multipart", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "GET", PathPattern: "/kling/v1/videos/text2video/:task_id", ProtocolFamily: "kling_video", BodyKind: "none", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "GET", PathPattern: "/kling/v1/videos/image2video/:task_id", ProtocolFamily: "kling_video", BodyKind: "none", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/jimeng/", ProtocolFamily: "jimeng", BodyKind: "json", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/:mode/mj/*", ProtocolFamily: "midjourney", BodyKind: "json", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/mj/*", ProtocolFamily: "midjourney", BodyKind: "json", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/suno/*", ProtocolFamily: "suno", BodyKind: "json", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/v1/videos*", ProtocolFamily: "video", BodyKind: "json_or_multipart", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
	}}
}

func (r Registry) Match(method, path string) (Entry, bool) {
	for _, entry := range r.entries {
		if entry.Method != method {
			continue
		}
		if matchPath(entry.PathPattern, path) {
			return entry, true
		}
	}
	return Entry{}, false
}

func matchPath(pattern, path string) bool {
	if pattern == path {
		return true
	}
	if strings.Contains(pattern, ":") {
		return matchSegmentPath(pattern, path)
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if strings.HasSuffix(prefix, "/") {
			return matchSegmentPath(pattern, path)
		}
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	return false
}

func matchSegmentPath(pattern, path string) bool {
	patternSegments := splitPath(pattern)
	pathSegments := splitPath(path)
	if len(patternSegments) == 0 || len(pathSegments) == 0 {
		return false
	}
	if patternSegments[len(patternSegments)-1] == "*" {
		if len(pathSegments) <= len(patternSegments)-1 {
			return false
		}
		for _, pathSegment := range pathSegments[len(patternSegments)-1:] {
			if pathSegment == "" {
				return false
			}
		}
		pathSegments = pathSegments[:len(patternSegments)-1]
		patternSegments = patternSegments[:len(patternSegments)-1]
	} else if len(patternSegments) != len(pathSegments) {
		return false
	}
	if strings.HasSuffix(pattern, "/") != strings.HasSuffix(path, "/") {
		return false
	}
	if len(patternSegments) != len(pathSegments) {
		return false
	}
	for i := range patternSegments {
		patternSegment := patternSegments[i]
		pathSegment := pathSegments[i]
		if pathSegment == "" {
			return false
		}
		if strings.HasPrefix(patternSegment, ":") {
			continue
		}
		if patternSegment != pathSegment {
			return false
		}
	}
	return true
}

func splitPath(path string) []string {
	withoutLeadingSlash := strings.TrimPrefix(path, "/")
	if withoutLeadingSlash == "" {
		return nil
	}
	return strings.Split(withoutLeadingSlash, "/")
}
