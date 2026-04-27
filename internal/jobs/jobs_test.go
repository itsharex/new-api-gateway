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

	err := publisher.PublishTraceCaptured(context.Background(), NewTraceCaptured("trace_1", "/v1/chat/completions", "openai_chat", "raw_and_normalized", "E12345"))
	if err != nil {
		t.Fatalf("PublishTraceCaptured error: %v", err)
	}
	if client.key != "analysis_jobs" {
		t.Fatalf("key = %q", client.key)
	}
	if len(client.values) != 1 {
		t.Fatalf("values = %d, want 1", len(client.values))
	}
	var job TraceCapturedJob
	if err := json.Unmarshal([]byte(client.values[0].(string)), &job); err != nil {
		t.Fatalf("job JSON error: %v", err)
	}
	if job.Type != "trace_captured" || job.TraceID != "trace_1" || job.EmployeeNo != "E12345" {
		t.Fatalf("job = %+v", job)
	}
}

func TestRedisListPublisherReturnsRedisError(t *testing.T) {
	redisErr := errors.New("redis down")
	publisher := NewRedisListPublisher(&fakeRedisListClient{err: redisErr}, "analysis_jobs")

	err := publisher.PublishTraceCaptured(context.Background(), NewTraceCaptured("trace_1", "/v1/chat/completions", "openai_chat", "raw_and_normalized", "E12345"))
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
