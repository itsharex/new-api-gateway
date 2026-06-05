package admin

import "time"

type Role string

const (
	RoleViewer    Role = "viewer"
	RoleAuditor   Role = "auditor"
	RoleRawAccess Role = "raw_access"
	RoleAdmin     Role = "admin"
)

type Permission string

const (
	PermissionViewAggregates       Permission = "view_aggregates"
	PermissionViewNormalizedTraces Permission = "view_normalized_traces"
	PermissionReview               Permission = "review"
	PermissionRawEvidence          Permission = "raw_evidence"
	PermissionAPIKeyLookup         Permission = "api_key_lookup"
	PermissionManageUsers          Permission = "manage_users"
)

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	DisplayName  string    `json:"display_name"`
	Email        string    `json:"email"`
	Role         Role      `json:"role"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Session struct {
	SessionID string
	UserID    int64
	ExpiresAt time.Time
	CSRFToken string
}

type Principal struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        Role   `json:"role"`
	CSRFToken   string `json:"-"`
	SessionID   string `json:"-"`
}

type AuditActionLog struct {
	ActorUserID        int64
	ActorUsername      string
	Action             string
	TargetType         string
	TargetID           string
	TokenFingerprint   string
	FingerprintDisplay string
	TraceID            string
	IPHash             string
	UserAgentHash      string
	MetadataJSON       string
	CreatedAt          time.Time
}

type ReviewDecision struct {
	TargetType       string    `json:"target_type"`
	TargetID         string    `json:"target_id"`
	Decision         string    `json:"decision"`
	ReviewerID       int64     `json:"reviewer_id"`
	ReviewerUsername string    `json:"reviewer_username"`
	Note             string    `json:"note"`
	CreatedAt        time.Time `json:"created_at"`
}

type TraceFilter struct {
	TraceID          string
	Username         string
	TokenFingerprint string
	RoutePattern     string
	Model            string
	StatusCode       int
	Page             int
	Limit            int
}

type TracePagination struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int   `json:"total_pages"`
	HasPrev    bool  `json:"has_prev"`
	HasNext    bool  `json:"has_next"`
}

type TraceListResult struct {
	Traces     []TraceSummary  `json:"traces"`
	Pagination TracePagination `json:"pagination"`
}

type TraceSummary struct {
	TraceID               string `json:"trace_id"`
	Method                string `json:"method"`
	Path                  string `json:"path"`
	RoutePattern          string `json:"route_pattern"`
	ProtocolFamily        string `json:"protocol_family"`
	StatusCode            int    `json:"status_code"`
	Username              string `json:"username"`
	FingerprintDisplay    string `json:"fingerprint_display"`
	ModelRequested        string `json:"model_requested"`
	UsagePromptTokens     int    `json:"usage_prompt_tokens"`
	UsageCompletionTokens int    `json:"usage_completion_tokens"`
	UsageCachedTokens     int    `json:"usage_cached_tokens"`
	UsageTotalTokens      int    `json:"usage_total_tokens"`
	CreatedAt             string `json:"created_at"`
	NeedsReview           bool   `json:"needs_review"`
}

type AnomalySummary struct {
	AnomalyID          string `json:"anomaly_id"`
	AnomalyType        string `json:"anomaly_type"`
	Severity           string `json:"severity"`
	Status             string `json:"status"`
	Username           string `json:"username"`
	FingerprintDisplay string `json:"fingerprint_display"`
	ObservedValue      string `json:"observed_value"`
	ThresholdValue     string `json:"threshold_value"`
	Reason             string `json:"reason"`
	DisplayReason      string `json:"display_reason"`
	CreatedAt          string `json:"created_at"`
}

type CoverageAlertSummary struct {
	AlertID         string `json:"alert_id"`
	AlertCode       string `json:"alert_code"`
	Severity        string `json:"severity"`
	Status          string `json:"status"`
	Method          string `json:"method"`
	RoutePattern    string `json:"route_pattern"`
	RawPath         string `json:"raw_path"`
	ProtocolFamily  string `json:"protocol_family"`
	OccurrenceCount int64  `json:"occurrence_count"`
	Message         string `json:"message"`
	LastSeenAt      string `json:"last_seen_at"`
}

type LookupSummary struct {
	FingerprintDisplay string         `json:"fingerprint_display"`
	TokenFingerprint   string         `json:"token_fingerprint"`
	Username           string         `json:"username"`
	NewAPITokenID      int            `json:"new_api_token_id"`
	TokenName          string         `json:"token_name"`
	TokenStatus        int            `json:"token_status"`
	RecentTraces       []TraceSummary `json:"recent_traces"`
	OpenAnomalyCount   int            `json:"open_anomaly_count"`
}

type EvidenceObjectSummary struct {
	TraceID     string
	ObjectType  string
	ObjectRef   string
	ContentType string
	SizeBytes   int64
	SHA256      string
}

type OverviewSummary struct {
	RequestCount24h      int64           `json:"request_count_24h"`
	SuccessCount24h      int64           `json:"success_count_24h"`
	ErrorCount24h        int64           `json:"error_count_24h"`
	TotalTokens24h       int64           `json:"total_tokens_24h"`
	OpenAnomalies        int64           `json:"open_anomalies"`
	OpenCoverageAlerts   int64           `json:"open_coverage_alerts"`
	RawOnlyTraceCount24h int64           `json:"raw_only_trace_count_24h"`
	TokenUsageDaily      []TokenUsageDay `json:"token_usage_daily"`
}

type AnalysisRuntimeSnapshot struct {
	Available               bool    `json:"available"`
	Error                   string  `json:"error,omitempty"`
	Stage                   string  `json:"stage"`
	QueueDepth              int64   `json:"queue_depth"`
	PendingCount            int64   `json:"pending_count"`
	LeasedCount             int64   `json:"leased_count"`
	ActiveConsumers         int64   `json:"active_consumers"`
	OldestPendingAgeSeconds int64   `json:"oldest_pending_age_seconds"`
	ThroughputPerMinute     int64   `json:"throughput_per_minute"`
	SuccessRate             float64 `json:"success_rate"`
	RetryableFailRate       float64 `json:"retryable_fail_rate"`
	TerminalFailRate        float64 `json:"terminal_fail_rate"`
	LLMJudgeTimeoutRate     float64 `json:"llm_judge_timeout_rate"`
	QueueWaitP50MS          int64   `json:"queue_wait_p50_ms"`
	QueueWaitP95MS          int64   `json:"queue_wait_p95_ms"`
	ProcessingP50MS         int64   `json:"processing_p50_ms"`
	ProcessingP95MS         int64   `json:"processing_p95_ms"`
}

type AnalysisRuntimeHistoryPoint struct {
	Stage                   string `json:"stage"`
	SampledAt               string `json:"sampled_at"`
	QueueDepth              int64  `json:"queue_depth"`
	OldestPendingAgeSeconds int64  `json:"oldest_pending_age_seconds"`
	QueueWaitP95MS          int64  `json:"queue_wait_p95_ms"`
	ProcessingP95MS         int64  `json:"processing_p95_ms"`
}

type AnalysisRuntimeConsumer struct {
	WorkerID      string `json:"worker_id"`
	Stage         string `json:"stage"`
	LeasedCount   int64  `json:"leased_count"`
	LastSeenAt    string `json:"last_seen_at"`
	IdleSeconds   int64  `json:"idle_seconds"`
	LastErrorCode string `json:"last_error_code"`
}

type TokenUsageDay struct {
	Date        string `json:"date"`
	TotalTokens int64  `json:"total_tokens"`
}

type UsageFilter struct {
	Username         string
	TokenFingerprint string
	Model            string
	RoutePattern     string
	BucketSize       string
	Limit            int
}

type UsageBucket struct {
	BucketStart        string `json:"bucket_start"`
	BucketSize         string `json:"bucket_size"`
	Username           string `json:"username"`
	FingerprintDisplay string `json:"fingerprint_display"`
	Model              string `json:"model"`
	RoutePattern       string `json:"route_pattern"`
	RequestCount       int64  `json:"request_count"`
	SuccessCount       int64  `json:"success_count"`
	ErrorCount         int64  `json:"error_count"`
	PromptTokens       int64  `json:"prompt_tokens"`
	CompletionTokens   int64  `json:"completion_tokens"`
	CachedTokens       int64  `json:"cached_tokens"`
	TotalTokens        int64  `json:"total_tokens"`
	EstimatedCost      string `json:"estimated_cost"`
}

type EmployeeUsageFilter struct {
	Username        string
	Range           string
	Model           string
	Start           time.Time
	End             time.Time
	BucketSize      string
	ExpectedBuckets int
}

type UsageEmployeeSearchFilter struct {
	Query string
	Limit int
}

type UsageEmployeeSearchResult struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Department  string `json:"department"`
	LastSeenAt  string `json:"last_seen_at"`
}

type GlobalUsageEmployee struct {
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	Department   string `json:"department"`
	TotalTokens  int64  `json:"total_tokens"`
	RequestCount int64  `json:"request_count"`
	LastSeenAt   string `json:"last_seen_at"`
}

type GlobalUsageSummary struct {
	Window          string                `json:"window"`
	TotalTokens     int64                 `json:"total_tokens"`
	ActiveEmployees int64                 `json:"active_employees"`
	RequestCount    int64                 `json:"request_count"`
	ActiveModels    int64                 `json:"active_models"`
	TopEmployees    []GlobalUsageEmployee `json:"top_employees"`
	TopModels       []UsageModelSummary   `json:"top_models"`
}

type UsageTokenSummary struct {
	RequestCount     int64 `json:"request_count"`
	SuccessCount     int64 `json:"success_count"`
	ErrorCount       int64 `json:"error_count"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CachedTokens     int64 `json:"cached_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type UsageTrendPoint struct {
	BucketStart      string `json:"bucket_start"`
	BucketSize       string `json:"bucket_size"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	ErrorCount       int64  `json:"error_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

type UsageModelSummary struct {
	Model            string `json:"model"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	ErrorCount       int64  `json:"error_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

type EmployeeUsageTrend struct {
	Username            string              `json:"username"`
	Range               string              `json:"range"`
	BucketSize          string              `json:"bucket_size"`
	ExpectedBucketCount int                 `json:"expected_bucket_count"`
	ActiveBucketCount   int                 `json:"active_bucket_count"`
	SelectedModel       string              `json:"selected_model"`
	Models              []string            `json:"models"`
	Summary             UsageTokenSummary   `json:"summary"`
	Points              []UsageTrendPoint   `json:"points"`
	ModelSummary        []UsageModelSummary `json:"model_summary"`
}

type TokenIdentityFilter struct {
	Username         string
	TokenFingerprint string
	Limit            int
}

type TokenIdentitySummary struct {
	FingerprintDisplay string `json:"fingerprint_display"`
	TokenFingerprint   string `json:"token_fingerprint"`
	NewAPITokenID      int    `json:"new_api_token_id"`
	TokenNameRaw       string `json:"token_name_raw"`
	Username           string `json:"username"`
	DisplayName        string `json:"display_name"`
	Department         string `json:"department"`
	TokenStatus        int    `json:"token_status"`
	TokenGroup         string `json:"token_group"`
	LastSeenAt         string `json:"last_seen_at"`
}

type ReviewDecisionFilter struct {
	TargetType string
	TargetID   string
	Limit      int
}

type SystemSettingsSummary struct {
	UsernamePattern string `json:"username_pattern"`
	MetricsEnabled  bool   `json:"metrics_enabled"`
	LookupLimit     int    `json:"lookup_limit"`
	RawAccessLimit  int    `json:"raw_access_limit"`
}

type TraceDetail struct {
	TraceSummary
	RequestRawRef            string                     `json:"request_raw_ref"`
	ResponseRawRef           string                     `json:"response_raw_ref"`
	RequestHeadersRef        string                     `json:"request_headers_ref"`
	ResponseHeadersRef       string                     `json:"response_headers_ref"`
	IdentityResolutionStatus string                     `json:"identity_resolution_status"`
	AnalysisStatus           string                     `json:"analysis_status"`
	NormalizedMessages       []NormalizedMessageSummary `json:"normalized_messages"`
	AnalysisResults          []AnalysisResultSummary    `json:"analysis_results"`
	Anomalies                []AnomalySummary           `json:"anomalies"`
}

type NormalizedMessageSummary struct {
	Direction          string `json:"direction"`
	SequenceIndex      int    `json:"sequence_index"`
	Role               string `json:"role"`
	Modality           string `json:"modality"`
	ContentText        string `json:"content_text"`
	MediaURL           string `json:"media_url"`
	ProtocolItemType   string `json:"protocol_item_type"`
	TokenCountEstimate int    `json:"token_count_estimate"`
}

type AnalysisResultSummary struct {
	AnalyzerName string `json:"analyzer_name"`
	Category     string `json:"category"`
	Label        string `json:"label"`
	Score        string `json:"score"`
	Confidence   string `json:"confidence"`
	Severity     string `json:"severity"`
	ResultJSON   string `json:"result_json"`
	CreatedAt    string `json:"created_at"`
}

type ContextCatalogEntry struct {
	ID                     int64    `json:"id"`
	ContextType            string   `json:"context_type"`
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	Keywords               []string `json:"keywords"`
	Aliases                []string `json:"aliases"`
	Owner                  string   `json:"owner"`
	ExpectedTaskCategories []string `json:"expected_task_categories"`
	ExpectedModels         []string `json:"expected_models"`
	ExpectedUsageLevel     string   `json:"expected_usage_level"`
	Active                 bool     `json:"active"`
	CreatedBy              string   `json:"created_by"`
	UpdatedBy              string   `json:"updated_by"`
	CreatedAt              string   `json:"created_at"`
	UpdatedAt              string   `json:"updated_at"`
}

type AuditActionLogSummary struct {
	ActorUsername      string `json:"actor_username"`
	Action             string `json:"action"`
	TargetType         string `json:"target_type"`
	TargetID           string `json:"target_id"`
	FingerprintDisplay string `json:"fingerprint_display"`
	TraceID            string `json:"trace_id"`
	MetadataJSON       string `json:"metadata_json"`
	CreatedAt          string `json:"created_at"`
}
