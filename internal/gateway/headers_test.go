package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestHeaderEvidenceJSONRedactsSecrets(t *testing.T) {
	header := http.Header{}
	header.Set("Authorization", "Bearer sk-secret-value")
	header.Set("x-api-key", "sk-anthropic-secret")
	header.Set("x-goog-api-key", "sk-gemini-secret")
	header.Set("mj-api-secret", "mj-secret")
	header.Set("Sec-WebSocket-Protocol", "realtime, openai-insecure-api-key.sk-real-secret, openai-beta.realtime-v1")
	header.Set("Content-Type", "application/json")

	data, err := headerEvidenceJSON(header)
	if err != nil {
		t.Fatalf("headerEvidenceJSON error: %v", err)
	}
	text := string(data)
	for _, secret := range []string{"sk-secret-value", "sk-anthropic-secret", "sk-gemini-secret", "mj-secret", "sk-real-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("header evidence leaked %q in %s", secret, text)
		}
	}

	var decoded map[string][]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("header evidence is not JSON: %v", err)
	}
	if decoded["Authorization"][0] != "[REDACTED]" {
		t.Fatalf("Authorization = %q", decoded["Authorization"][0])
	}
	if decoded["Content-Type"][0] != "application/json" {
		t.Fatalf("Content-Type = %q", decoded["Content-Type"][0])
	}
	if !strings.Contains(decoded["Sec-WebSocket-Protocol"][0], "openai-insecure-api-key.[REDACTED]") {
		t.Fatalf("Sec-WebSocket-Protocol = %q", decoded["Sec-WebSocket-Protocol"][0])
	}
}

func TestHeaderEvidenceJSONIsStable(t *testing.T) {
	header := http.Header{}
	header.Add("X-Zeta", "z")
	header.Add("X-Alpha", "a")

	first, err := headerEvidenceJSON(header)
	if err != nil {
		t.Fatalf("first headerEvidenceJSON error: %v", err)
	}
	second, err := headerEvidenceJSON(header)
	if err != nil {
		t.Fatalf("second headerEvidenceJSON error: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("header JSON is not stable:\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestHeaderEvidenceJSONPreservesRawMapKeyValues(t *testing.T) {
	header := http.Header{
		"x-api-key": []string{"sk-direct-secret"},
		"X-Custom":  []string{"visible"},
	}

	data, err := headerEvidenceJSON(header)
	if err != nil {
		t.Fatalf("headerEvidenceJSON error: %v", err)
	}
	if strings.Contains(string(data), "sk-direct-secret") {
		t.Fatalf("header evidence leaked raw map secret in %s", data)
	}

	var decoded map[string][]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("header evidence is not JSON: %v", err)
	}
	if decoded["X-Api-Key"][0] != "[REDACTED]" {
		t.Fatalf("X-Api-Key = %q", decoded["X-Api-Key"][0])
	}
	if decoded["X-Custom"][0] != "visible" {
		t.Fatalf("X-Custom = %q", decoded["X-Custom"][0])
	}
}

func TestHeaderEvidenceJSONMergesCanonicalDuplicateKeys(t *testing.T) {
	header := http.Header{
		"X-Custom": []string{"one"},
		"x-custom": []string{"two"},
	}

	data, err := headerEvidenceJSON(header)
	if err != nil {
		t.Fatalf("headerEvidenceJSON error: %v", err)
	}

	var decoded map[string][]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("header evidence is not JSON: %v", err)
	}
	values := decoded["X-Custom"]
	if len(values) != 2 {
		t.Fatalf("X-Custom length = %d, values = %v", len(values), values)
	}
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	if !seen["one"] || !seen["two"] {
		t.Fatalf("X-Custom values = %v", values)
	}
}
