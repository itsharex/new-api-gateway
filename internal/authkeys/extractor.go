package authkeys

import (
	"net/http"
	"strings"
)

type Source string

const (
	SourceAuthorization Source = "authorization"
	SourceAnthropic     Source = "x-api-key"
	SourceGeminiQuery   Source = "query:key"
	SourceGeminiHeader  Source = "x-goog-api-key"
	SourceMidjourney    Source = "mj-api-secret"
	SourceRealtime      Source = "sec-websocket-protocol"
)

type Result struct {
	CanonicalKey string
	Source       Source
}

func Extract(req *http.Request) (Result, bool) {
	if value := req.Header.Get("Authorization"); value != "" {
		return Result{CanonicalKey: canonicalize(value), Source: SourceAuthorization}, true
	}
	if value := req.Header.Get("x-api-key"); value != "" {
		return Result{CanonicalKey: canonicalize(value), Source: SourceAnthropic}, true
	}
	if value := req.URL.Query().Get("key"); value != "" {
		return Result{CanonicalKey: canonicalize(value), Source: SourceGeminiQuery}, true
	}
	if value := req.Header.Get("x-goog-api-key"); value != "" {
		return Result{CanonicalKey: canonicalize(value), Source: SourceGeminiHeader}, true
	}
	if value := req.Header.Get("mj-api-secret"); value != "" {
		return Result{CanonicalKey: canonicalize(value), Source: SourceMidjourney}, true
	}
	if value := req.Header.Get("Sec-WebSocket-Protocol"); value != "" {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "openai-insecure-api-key.") {
				return Result{CanonicalKey: canonicalize(strings.TrimPrefix(part, "openai-insecure-api-key.")), Source: SourceRealtime}, true
			}
		}
	}
	return Result{}, false
}

func canonicalize(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		value = strings.TrimSpace(value[7:])
	}
	value = strings.TrimPrefix(value, "sk-")
	parts := strings.Split(value, "-")
	if len(parts) > 0 {
		return parts[0]
	}
	return value
}
