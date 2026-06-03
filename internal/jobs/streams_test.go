package jobs

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestRedisStreamPublisherPublishesCoreEnvelope(t *testing.T) {
	client := &fakeRedisStreamClient{}
	publisher := NewRedisStreamPublisher(client, DefaultRedisCoreStream)

	err := publisher.PublishTraceCaptured(context.Background(), TraceCapturedInput{
		TraceID:          "trace_1",
		ProtocolFamily:   "openai_chat",
		CaptureMode:      "raw_and_normalized",
		RequestBodySize:  128,
		ResponseBodySize: 256,
	})
	if err != nil {
		t.Fatalf("PublishTraceCaptured error: %v", err)
	}
	if client.args.Stream != "analysis.core" {
		t.Fatalf("stream = %q", client.args.Stream)
	}
	values, ok := client.args.Values.(map[string]any)
	if !ok {
		t.Fatalf("values type = %T, want map[string]any", client.args.Values)
	}
	if values["trace_id"] != "trace_1" {
		t.Fatalf("trace_id = %v", values["trace_id"])
	}
	if values["stage"] != "core" {
		t.Fatalf("stage = %v", values["stage"])
	}
	if values["enqueued_at"] == "" {
		t.Fatalf("enqueued_at = %v", values["enqueued_at"])
	}
	if values["attempt"] != int64(1) {
		t.Fatalf("attempt = %v", values["attempt"])
	}
}

type fakeRedisStreamClient struct {
	args redis.XAddArgs
	err  error
}

func (c *fakeRedisStreamClient) XAdd(ctx context.Context, args *redis.XAddArgs) *redis.StringCmd {
	if args != nil {
		c.args = *args
	}
	return redis.NewStringResult("1-0", c.err)
}
