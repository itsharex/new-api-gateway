package jobs

import (
	"context"
	"encoding/json"
	"errors"
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
	if len(values) != 5 {
		t.Fatalf("values = %#v, want exactly 5 fields", values)
	}

	wantKeys := map[string]struct{}{
		"trace_id":    {},
		"stage":       {},
		"enqueued_at": {},
		"attempt":     {},
		"hints":       {},
	}
	if len(values) != len(wantKeys) {
		t.Fatalf("keys = %#v, want %#v", values, wantKeys)
	}
	for key := range values {
		if _, ok := wantKeys[key]; !ok {
			t.Fatalf("unexpected key %q in values %#v", key, values)
		}
	}

	hintsJSON, ok := values["hints"].(string)
	if !ok {
		t.Fatalf("hints type = %T, want string", values["hints"])
	}

	var hints map[string]string
	if err := json.Unmarshal([]byte(hintsJSON), &hints); err != nil {
		t.Fatalf("unmarshal hints: %v", err)
	}
	if hints["protocol_family"] != "openai_chat" {
		t.Fatalf("hints = %#v", hints)
	}
	if hints["capture_mode"] != "raw_and_normalized" {
		t.Fatalf("hints = %#v", hints)
	}
	if hints["request_size_bucket"] != "<1kb" {
		t.Fatalf("hints = %#v", hints)
	}
	if hints["response_size_bucket"] != "<1kb" {
		t.Fatalf("hints = %#v", hints)
	}
	if hints["has_response_body"] != "true" {
		t.Fatalf("hints = %#v", hints)
	}
}

func TestNewRedisStreamPublisherDefaultsCoreStream(t *testing.T) {
	client := &fakeRedisStreamClient{}
	publisher := NewRedisStreamPublisher(client, "")

	if err := publisher.PublishTraceCaptured(context.Background(), TraceCapturedInput{TraceID: "trace_1"}); err != nil {
		t.Fatalf("PublishTraceCaptured error: %v", err)
	}
	if client.args.Stream != DefaultRedisCoreStream {
		t.Fatalf("stream = %q, want %q", client.args.Stream, DefaultRedisCoreStream)
	}
}

func TestRedisStreamPublisherReturnsNilClientError(t *testing.T) {
	publisher := NewRedisStreamPublisher(nil, DefaultRedisCoreStream)

	err := publisher.PublishTraceCaptured(context.Background(), TraceCapturedInput{TraceID: "trace_1"})
	if !errors.Is(err, ErrRedisStreamClientRequired) {
		t.Fatalf("error = %v, want %v", err, ErrRedisStreamClientRequired)
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
