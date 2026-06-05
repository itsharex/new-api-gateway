package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const DefaultRedisListName = "analysis_jobs"

var ErrRedisListClientRequired = errors.New("jobs redis list client is nil")

type TraceCapturedJob struct {
	Type                     string `json:"type"`
	TraceID                  string `json:"trace_id"`
	RoutePattern             string `json:"route_pattern"`
	ProtocolFamily           string `json:"protocol_family"`
	CaptureMode              string `json:"capture_mode"`
	Username                 string `json:"username"`
	TokenFingerprint         string `json:"token_fingerprint"`
	FingerprintDisplay       string `json:"fingerprint_display"`
	NewAPITokenID            int    `json:"new_api_token_id"`
	TokenNameSnapshot        string `json:"token_name_snapshot"`
	IdentityResolutionStatus string `json:"identity_resolution_status"`
	StatusCode               int    `json:"status_code"`
	UpstreamStatusCode       int    `json:"upstream_status_code"`
	Stream                   bool   `json:"stream"`
	RequestStartedAt         string `json:"request_started_at"`
	ClientIPHash             string `json:"client_ip_hash"`
	UserAgentHash            string `json:"user_agent_hash"`
	RequestBodySize          int64  `json:"request_body_size"`
	ResponseBodySize         int64  `json:"response_body_size"`
	RequestRawRef            string `json:"request_raw_ref"`
	RequestHeadersRef        string `json:"request_headers_ref"`
	ResponseRawRef           string `json:"response_raw_ref"`
	ResponseHeadersRef       string `json:"response_headers_ref"`
	RequestContentType       string `json:"request_content_type"`
	ResponseContentType      string `json:"response_content_type"`
	ModelRequested           string `json:"model_requested"`
	UsagePromptTokens        int    `json:"usage_prompt_tokens"`
	UsageCompletionTokens    int    `json:"usage_completion_tokens"`
	UsageTotalTokens         int    `json:"usage_total_tokens"`
	UsageReasoningTokens     int    `json:"usage_reasoning_tokens"`
	UsageCachedTokens        int    `json:"usage_cached_tokens"`
}

type PublishResult struct {
	MessageID  string
	EnqueuedAt time.Time
}

type Publisher interface {
	PublishTraceCaptured(ctx context.Context, input TraceCapturedInput) (PublishResult, error)
}

type TraceCapturedInput struct {
	TraceID                  string
	EnqueuedAt               string
	RoutePattern             string
	ProtocolFamily           string
	CaptureMode              string
	Username                 string
	TokenFingerprint         string
	FingerprintDisplay       string
	NewAPITokenID            int
	TokenNameSnapshot        string
	IdentityResolutionStatus string
	StatusCode               int
	UpstreamStatusCode       int
	Stream                   bool
	RequestStartedAt         string
	ClientIPHash             string
	UserAgentHash            string
	RequestBodySize          int64
	ResponseBodySize         int64
	RequestRawRef            string
	RequestHeadersRef        string
	ResponseRawRef           string
	ResponseHeadersRef       string
	RequestContentType       string
	ResponseContentType      string
	ModelRequested           string
	UsagePromptTokens        int
	UsageCompletionTokens    int
	UsageTotalTokens         int
	UsageReasoningTokens     int
	UsageCachedTokens        int
}

func NewTraceCaptured(input TraceCapturedInput) TraceCapturedJob {
	return TraceCapturedJob{
		Type:                     "trace_captured",
		TraceID:                  input.TraceID,
		RoutePattern:             input.RoutePattern,
		ProtocolFamily:           input.ProtocolFamily,
		CaptureMode:              input.CaptureMode,
		Username:                 input.Username,
		TokenFingerprint:         input.TokenFingerprint,
		FingerprintDisplay:       input.FingerprintDisplay,
		NewAPITokenID:            input.NewAPITokenID,
		TokenNameSnapshot:        input.TokenNameSnapshot,
		IdentityResolutionStatus: input.IdentityResolutionStatus,
		StatusCode:               input.StatusCode,
		UpstreamStatusCode:       input.UpstreamStatusCode,
		Stream:                   input.Stream,
		RequestStartedAt:         input.RequestStartedAt,
		ClientIPHash:             input.ClientIPHash,
		UserAgentHash:            input.UserAgentHash,
		RequestBodySize:          input.RequestBodySize,
		ResponseBodySize:         input.ResponseBodySize,
		RequestRawRef:            input.RequestRawRef,
		RequestHeadersRef:        input.RequestHeadersRef,
		ResponseRawRef:           input.ResponseRawRef,
		ResponseHeadersRef:       input.ResponseHeadersRef,
		RequestContentType:       input.RequestContentType,
		ResponseContentType:      input.ResponseContentType,
		ModelRequested:           input.ModelRequested,
		UsagePromptTokens:        input.UsagePromptTokens,
		UsageCompletionTokens:    input.UsageCompletionTokens,
		UsageTotalTokens:         input.UsageTotalTokens,
		UsageReasoningTokens:     input.UsageReasoningTokens,
		UsageCachedTokens:        input.UsageCachedTokens,
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

func (p RedisListPublisher) PublishTraceCaptured(ctx context.Context, input TraceCapturedInput) (PublishResult, error) {
	if p.client == nil {
		return PublishResult{}, ErrRedisListClientRequired
	}
	job := NewTraceCaptured(input)
	data, err := json.Marshal(job)
	if err != nil {
		return PublishResult{}, err
	}
	if err := p.client.RPush(ctx, p.list, string(data)).Err(); err != nil {
		return PublishResult{}, err
	}
	return PublishResult{}, nil
}
