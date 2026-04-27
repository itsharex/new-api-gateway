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
	Type                  string `json:"type"`
	TraceID               string `json:"trace_id"`
	RoutePattern          string `json:"route_pattern"`
	ProtocolFamily        string `json:"protocol_family"`
	CaptureMode           string `json:"capture_mode"`
	EmployeeNo            string `json:"employee_no"`
	RequestRawRef         string `json:"request_raw_ref"`
	RequestHeadersRef     string `json:"request_headers_ref"`
	ResponseRawRef        string `json:"response_raw_ref"`
	ResponseHeadersRef    string `json:"response_headers_ref"`
	RequestContentType    string `json:"request_content_type"`
	ResponseContentType   string `json:"response_content_type"`
	ModelRequested        string `json:"model_requested"`
	UsagePromptTokens     int    `json:"usage_prompt_tokens"`
	UsageCompletionTokens int    `json:"usage_completion_tokens"`
	UsageTotalTokens      int    `json:"usage_total_tokens"`
	UsageReasoningTokens  int    `json:"usage_reasoning_tokens"`
	UsageCachedTokens     int    `json:"usage_cached_tokens"`
}

type Publisher interface {
	PublishTraceCaptured(ctx context.Context, job TraceCapturedJob) error
}

type TraceCapturedInput struct {
	TraceID               string
	RoutePattern          string
	ProtocolFamily        string
	CaptureMode           string
	EmployeeNo            string
	RequestRawRef         string
	RequestHeadersRef     string
	ResponseRawRef        string
	ResponseHeadersRef    string
	RequestContentType    string
	ResponseContentType   string
	ModelRequested        string
	UsagePromptTokens     int
	UsageCompletionTokens int
	UsageTotalTokens      int
	UsageReasoningTokens  int
	UsageCachedTokens     int
}

func NewTraceCaptured(input TraceCapturedInput) TraceCapturedJob {
	return TraceCapturedJob{
		Type:                  "trace_captured",
		TraceID:               input.TraceID,
		RoutePattern:          input.RoutePattern,
		ProtocolFamily:        input.ProtocolFamily,
		CaptureMode:           input.CaptureMode,
		EmployeeNo:            input.EmployeeNo,
		RequestRawRef:         input.RequestRawRef,
		RequestHeadersRef:     input.RequestHeadersRef,
		ResponseRawRef:        input.ResponseRawRef,
		ResponseHeadersRef:    input.ResponseHeadersRef,
		RequestContentType:    input.RequestContentType,
		ResponseContentType:   input.ResponseContentType,
		ModelRequested:        input.ModelRequested,
		UsagePromptTokens:     input.UsagePromptTokens,
		UsageCompletionTokens: input.UsageCompletionTokens,
		UsageTotalTokens:      input.UsageTotalTokens,
		UsageReasoningTokens:  input.UsageReasoningTokens,
		UsageCachedTokens:     input.UsageCachedTokens,
	}
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
