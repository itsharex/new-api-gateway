package gateway

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

var sensitiveHeaderNames = map[string]struct{}{
	"authorization":        {},
	"x-api-key":            {},
	"x-goog-api-key":       {},
	"mj-api-secret":        {},
	"proxy-authorization":  {},
	"openai-organization":  {},
	"anthropic-api-key":    {},
	"anthropic-auth-token": {},
}

func headerEvidenceJSON(header http.Header) ([]byte, error) {
	snapshot := make(map[string][]string, len(header))
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		values := header[key]
		copied := make([]string, 0, len(values))
		for _, value := range values {
			copied = append(copied, sanitizeHeaderValue(key, value))
		}
		snapshot[headerEvidenceKey(key)] = copied
	}
	return json.Marshal(snapshot)
}

func headerEvidenceKey(key string) string {
	canonical := http.CanonicalHeaderKey(key)
	if strings.EqualFold(canonical, "Sec-Websocket-Protocol") {
		return "Sec-WebSocket-Protocol"
	}
	return canonical
}

func sanitizeHeaderValue(key, value string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if _, ok := sensitiveHeaderNames[normalized]; ok {
		return "[REDACTED]"
	}
	if normalized == "sec-websocket-protocol" {
		parts := strings.Split(value, ",")
		for i, part := range parts {
			trimmed := strings.TrimSpace(part)
			if strings.HasPrefix(trimmed, "openai-insecure-api-key.") {
				parts[i] = " openai-insecure-api-key.[REDACTED]"
				if i == 0 {
					parts[i] = "openai-insecure-api-key.[REDACTED]"
				}
			} else {
				parts[i] = part
			}
		}
		return strings.Join(parts, ",")
	}
	return value
}
