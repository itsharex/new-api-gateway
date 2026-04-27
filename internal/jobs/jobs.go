package jobs

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/redis/go-redis/v9"
)

const DefaultRedisListName = "analysis_jobs"

var ErrRedisListClientRequired = errors.New("jobs redis list client is nil")

type TraceCapturedJob struct {
	Type           string `json:"type"`
	TraceID        string `json:"trace_id"`
	RoutePattern   string `json:"route_pattern"`
	ProtocolFamily string `json:"protocol_family"`
	CaptureMode    string `json:"capture_mode"`
	EmployeeNo     string `json:"employee_no"`
}

type Publisher interface {
	PublishTraceCaptured(ctx context.Context, job TraceCapturedJob) error
}

func NewTraceCaptured(traceID, routePattern, protocolFamily, captureMode, employeeNo string) TraceCapturedJob {
	return TraceCapturedJob{Type: "trace_captured", TraceID: traceID, RoutePattern: routePattern, ProtocolFamily: protocolFamily, CaptureMode: captureMode, EmployeeNo: employeeNo}
}

type redisListClient interface {
	RPush(ctx context.Context, key string, values ...any) *redis.IntCmd
}

type RedisListPublisher struct {
	client redisListClient
	list   string
}

func NewRedisListPublisher(client redisListClient, list string) RedisListPublisher {
	if list == "" {
		list = DefaultRedisListName
	}
	return RedisListPublisher{client: client, list: list}
}

func (p RedisListPublisher) PublishTraceCaptured(ctx context.Context, job TraceCapturedJob) error {
	if p.client == nil {
		return ErrRedisListClientRequired
	}
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return p.client.RPush(ctx, p.list, string(data)).Err()
}
