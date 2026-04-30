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

const realtimeAPIKeyProtocolPrefix = "openai-insecure-api-key."

type Result struct {
	CanonicalKey string
	Source       Source
}

func Extract(req *http.Request) (Result, bool) {
	if key, ok := authorizationBearerKey(req.Header.Get("Authorization")); ok {
		return Result{CanonicalKey: key, Source: SourceAuthorization}, true
	}
	if key, ok := canonicalKey(req.Header.Get("x-api-key")); ok {
		return Result{CanonicalKey: key, Source: SourceAnthropic}, true
	}
	if key, ok := canonicalKey(req.URL.Query().Get("key")); ok {
		return Result{CanonicalKey: key, Source: SourceGeminiQuery}, true
	}
	if key, ok := canonicalKey(req.Header.Get("x-goog-api-key")); ok {
		return Result{CanonicalKey: key, Source: SourceGeminiHeader}, true
	}
	if key, ok := canonicalKey(req.Header.Get("mj-api-secret")); ok {
		return Result{CanonicalKey: key, Source: SourceMidjourney}, true
	}
	if value := req.Header.Get("Sec-WebSocket-Protocol"); value != "" {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, realtimeAPIKeyProtocolPrefix) {
				if key, ok := canonicalKey(strings.TrimPrefix(part, realtimeAPIKeyProtocolPrefix)); ok {
					return Result{CanonicalKey: key, Source: SourceRealtime}, true
				}
			}
		}
	}
	return Result{}, false
}

func authorizationBearerKey(value string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(value))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return canonicalKey(parts[1])
}

func Canonicalize(value string) (string, bool) {
	key := canonicalize(value)
	return key, key != ""
}

func canonicalKey(value string) (string, bool) {
	return Canonicalize(value)
}

func canonicalize(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		value = strings.TrimSpace(value[7:])
	}
	value = strings.TrimPrefix(value, "sk-")
	if index := strings.Index(value, "-"); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}
