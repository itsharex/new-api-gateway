package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

func sizeBucket(size int64) string {
	switch {
	case size <= 0:
		return "0"
	case size < 1024:
		return "<1kb"
	case size < 64*1024:
		return "1kb-64kb"
	case size < 1024*1024:
		return "64kb-1mb"
	default:
		return ">=1mb"
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
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

func (p RedisStreamPublisher) PublishTraceCaptured(ctx context.Context, input TraceCapturedInput) (PublishResult, error) {
	if p.client == nil {
		return PublishResult{}, ErrRedisStreamClientRequired
	}
	hints, err := json.Marshal(map[string]string{
		"protocol_family":      input.ProtocolFamily,
		"capture_mode":         input.CaptureMode,
		"request_size_bucket":  sizeBucket(input.RequestBodySize),
		"response_size_bucket": sizeBucket(input.ResponseBodySize),
		"has_response_body":    boolString(input.ResponseBodySize > 0),
	})
	if err != nil {
		return PublishResult{}, err
	}
	values := map[string]any{
		"trace_id": input.TraceID,
		"stage":    "core",
		"attempt":  int64(1),
		"hints":    string(hints),
	}
	if input.EnqueuedAt != "" {
		values["enqueued_at"] = input.EnqueuedAt
	}
	messageID, err := p.client.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		Values: values,
	}).Result()
	if err != nil {
		return PublishResult{}, err
	}
	queuedAt, err := streamMessageIDTime(messageID)
	if err != nil {
		return PublishResult{}, err
	}
	return PublishResult{
		MessageID:  messageID,
		EnqueuedAt: queuedAt,
	}, nil
}

func streamMessageIDTime(messageID string) (time.Time, error) {
	timestampText, _, ok := strings.Cut(messageID, "-")
	if !ok || timestampText == "" {
		return time.Time{}, fmt.Errorf("invalid redis stream message id %q", messageID)
	}
	timestampMS, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid redis stream message id %q: %w", messageID, err)
	}
	return time.UnixMilli(timestampMS).UTC(), nil
}
