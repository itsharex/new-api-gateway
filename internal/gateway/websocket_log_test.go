package gateway

import (
	"strings"
	"testing"
)

func TestBoundedWebSocketLogRedactsRealtimeAPIKey(t *testing.T) {
	log := newBoundedWebSocketLog(1024)
	_, _ = log.WriteClient([]byte("Sec-WebSocket-Protocol: openai-insecure-api-key.sk-secret-extra\r\n"))
	_, _ = log.WriteUpstream([]byte("HTTP/1.1 101 Switching Protocols\r\n"))

	text := log.String()
	if strings.Contains(text, "sk-secret-extra") {
		t.Fatalf("websocket log leaked key: %s", text)
	}
	if !strings.Contains(text, "client ") || !strings.Contains(text, "upstream ") {
		t.Fatalf("websocket log missing direction markers: %s", text)
	}
}

func TestBoundedWebSocketLogRedactsAuthorizationBearer(t *testing.T) {
	log := newBoundedWebSocketLog(1024)
	_, _ = log.WriteClient([]byte("Authorization: Bearer sk-secret-extra\r\n{\"ok\":true}"))

	text := log.String()
	if strings.Contains(text, "sk-secret-extra") {
		t.Fatalf("websocket log leaked bearer token: %s", text)
	}
	if !strings.Contains(text, "Authorization: Bearer [REDACTED]") {
		t.Fatalf("websocket log missing bearer redaction: %s", text)
	}
}

func TestBoundedWebSocketLogRedactsAuthorizationBearerAcrossChunks(t *testing.T) {
	log := newBoundedWebSocketLog(1024)
	_, _ = log.WriteClient([]byte("Authorization: Bearer "))
	_, _ = log.WriteClient([]byte("sk-secret-extra\r\n{\"ok\":true}"))

	body := log.Bytes()
	text := string(body)
	if strings.Contains(text, "sk-secret-extra") {
		t.Fatalf("websocket log bytes leaked split bearer token: %s", text)
	}
	if strings.Contains(log.String(), "sk-secret-extra") {
		t.Fatalf("websocket log string leaked split bearer token: %s", log.String())
	}
}

func TestBoundedWebSocketLogRedactsRealtimeAPIKeyAcrossChunks(t *testing.T) {
	log := newBoundedWebSocketLog(1024)
	_, _ = log.WriteClient([]byte("Sec-WebSocket-Protocol: openai-insecure-api-key."))
	_, _ = log.WriteClient([]byte("sk-secret-extra\r\n"))

	text := string(log.Bytes())
	if strings.Contains(text, "sk-secret-extra") {
		t.Fatalf("websocket log bytes leaked split realtime key: %s", text)
	}
	if strings.Contains(log.String(), "sk-secret-extra") {
		t.Fatalf("websocket log string leaked split realtime key: %s", log.String())
	}
}

func TestBoundedWebSocketLogRedactsBareAPIKeyPayload(t *testing.T) {
	log := newBoundedWebSocketLog(1024)
	_, _ = log.WriteClient([]byte(`{"token":"sk-secret-extra"}`))

	body := log.Bytes()
	text := string(body)
	if strings.Contains(text, "sk-secret-extra") {
		t.Fatalf("websocket log bytes leaked bare key: %s", text)
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("websocket log missing fallback redaction: %s", text)
	}
}

func TestBoundedWebSocketLogCapsBytes(t *testing.T) {
	log := newBoundedWebSocketLog(12)
	_, _ = log.WriteClient([]byte("abcdefghijklmnopqrstuvwxyz"))

	if len(log.Bytes()) != 12 {
		t.Fatalf("log length = %d, want 12", len(log.Bytes()))
	}
}
