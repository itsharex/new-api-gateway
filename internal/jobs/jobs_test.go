package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestTraceCapturedJobJSON(t *testing.T) {
	job := TraceCapturedJob{TraceID: "trace_1", RoutePattern: "/v1/chat/completions", CaptureMode: "raw_and_normalized"}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if string(data) == "{}" {
		t.Fatal("unexpected empty JSON")
	}
}

func TestRedisListPublisherPushesTraceCapturedEnvelope(t *testing.T) {
	client := &fakeRedisListClient{}
	publisher := NewRedisListPublisher(client, "analysis_jobs")

	job := NewTraceCaptured(TraceCapturedInput{
		TraceID:                  "trace_1",
		RoutePattern:             "/v1/chat/completions",
		ProtocolFamily:           "openai_chat",
		CaptureMode:              "raw_and_normalized",
		EmployeeNo:               "E12345",
		TokenFingerprint:         "tkfp_raw_value",
		FingerprintDisplay:       "tkfp_display",
		NewAPITokenID:            42,
		TokenNameSnapshot:        "E12345",
		IdentityResolutionStatus: "invalid_employee_no",
		StatusCode:               200,
		UpstreamStatusCode:       200,
		Stream:                   false,
		RequestStartedAt:         "2026-04-28T13:45:22Z",
		ClientIPHash:             "iphash",
		UserAgentHash:            "uahash",
		RequestBodySize:          128,
		ResponseBodySize:         256,
		RequestRawRef:            "raw/trace_1/request_body.bin",
		RequestHeadersRef:        "raw/trace_1/request_headers.bin",
		ResponseRawRef:           "raw/trace_1/response_body.bin",
		ResponseHeadersRef:       "raw/trace_1/response_headers.bin",
		RequestContentType:       "application/json",
		ResponseContentType:      "application/json",
		ModelRequested:           "gpt-test",
		UsageTotalTokens:         18,
	})
	err := publisher.PublishTraceCaptured(context.Background(), job)
	if err != nil {
		t.Fatalf("PublishTraceCaptured error: %v", err)
	}
	if client.key != "analysis_jobs" {
		t.Fatalf("key = %q", client.key)
	}
	if len(client.values) != 1 {
		t.Fatalf("values = %d, want 1", len(client.values))
	}
	var decoded TraceCapturedJob
	if err := json.Unmarshal([]byte(client.values[0].(string)), &decoded); err != nil {
		t.Fatalf("job JSON error: %v", err)
	}
	if decoded.Type != "trace_captured" || decoded.TraceID != "trace_1" || decoded.EmployeeNo != "E12345" {
		t.Fatalf("job = %+v", decoded)
	}
	if decoded.TokenFingerprint != "tkfp_raw_value" || decoded.FingerprintDisplay != "tkfp_display" {
		t.Fatalf("fingerprint fields = %+v", decoded)
	}
	if decoded.NewAPITokenID != 42 || decoded.TokenNameSnapshot != "E12345" || decoded.IdentityResolutionStatus != "invalid_employee_no" {
		t.Fatalf("token snapshot fields = %+v", decoded)
	}
	if decoded.StatusCode != 200 || decoded.UpstreamStatusCode != 200 || decoded.Stream {
		t.Fatalf("status fields = %+v", decoded)
	}
	if decoded.RequestStartedAt != "2026-04-28T13:45:22Z" {
		t.Fatalf("RequestStartedAt = %q", decoded.RequestStartedAt)
	}
	if decoded.ClientIPHash != "iphash" || decoded.UserAgentHash != "uahash" {
		t.Fatalf("audit hashes = %+v", decoded)
	}
	if decoded.RequestBodySize != 128 || decoded.ResponseBodySize != 256 {
		t.Fatalf("body sizes = %+v", decoded)
	}
	if decoded.ResponseRawRef != "raw/trace_1/response_body.bin" {
		t.Fatalf("ResponseRawRef = %q", decoded.ResponseRawRef)
	}
	if decoded.ModelRequested != "gpt-test" || decoded.UsageTotalTokens != 18 {
		t.Fatalf("minimal metadata = %+v", decoded)
	}
}

func TestRedisListPublisherReturnsRedisError(t *testing.T) {
	redisErr := errors.New("redis down")
	publisher := NewRedisListPublisher(&fakeRedisListClient{err: redisErr}, "analysis_jobs")

	err := publisher.PublishTraceCaptured(context.Background(), NewTraceCaptured(TraceCapturedInput{
		TraceID:        "trace_1",
		RoutePattern:   "/v1/chat/completions",
		ProtocolFamily: "openai_chat",
		CaptureMode:    "raw_and_normalized",
		EmployeeNo:     "E12345",
	}))
	if !errors.Is(err, redisErr) {
		t.Fatalf("error = %v, want %v", err, redisErr)
	}
}

type fakeRedisListClient struct {
	key    string
	values []any
	err    error
}

func (c *fakeRedisListClient) RPush(ctx context.Context, key string, values ...any) *redis.IntCmd {
	c.key = key
	c.values = append(c.values, values...)
	return redis.NewIntResult(1, c.err)
}
