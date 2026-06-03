package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const DefaultRedisCoreStream = "analysis.core"

var ErrRedisStreamClientRequired = errors.New("jobs redis stream client is nil")

type redisStreamClient interface {
	XAdd(ctx context.Context, args *redis.XAddArgs) *redis.StringCmd
}

type RedisStreamPublisher struct {
	client redisStreamClient
	stream string
	now    func() time.Time
}

func NewRedisStreamPublisher(client redisStreamClient, stream string) RedisStreamPublisher {
	if stream == "" {
		stream = DefaultRedisCoreStream
	}
	return RedisStreamPublisher{
		client: client,
		stream: stream,
		now:    time.Now,
	}
}

func (p RedisStreamPublisher) PublishTraceCaptured(ctx context.Context, input TraceCapturedInput) error {
	if p.client == nil {
		return ErrRedisStreamClientRequired
	}
	now := p.now
	if now == nil {
		now = time.Now
	}
	values := map[string]any{
		"trace_id":           input.TraceID,
		"stage":              "core",
		"enqueued_at":        now().UTC().Format(time.RFC3339),
		"attempt":            int64(1),
		"route_pattern":      input.RoutePattern,
		"protocol_family":    input.ProtocolFamily,
		"capture_mode":       input.CaptureMode,
		"stream":             input.Stream,
		"request_size":       input.RequestBodySize,
		"response_size":      input.ResponseBodySize,
		"status_code":        input.StatusCode,
		"upstream_status":    input.UpstreamStatusCode,
		"request_started_at": input.RequestStartedAt,
	}
	return p.client.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		Values: values,
	}).Err()
}
