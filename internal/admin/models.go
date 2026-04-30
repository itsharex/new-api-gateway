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
	EmployeeNo       string
	TokenFingerprint string
	RoutePattern     string
	Model            string
	StatusCode       int
	Limit            int
}

type TraceSummary struct {
	TraceID            string `json:"trace_id"`
	Method             string `json:"method"`
	Path               string `json:"path"`
	RoutePattern       string `json:"route_pattern"`
	ProtocolFamily     string `json:"protocol_family"`
	StatusCode         int    `json:"status_code"`
	EmployeeNo         string `json:"employee_no"`
	FingerprintDisplay string `json:"fingerprint_display"`
	ModelRequested     string `json:"model_requested"`
	UsageTotalTokens   int    `json:"usage_total_tokens"`
	CreatedAt          string `json:"created_at"`
}

type AnomalySummary struct {
	AnomalyID          string `json:"anomaly_id"`
	AnomalyType        string `json:"anomaly_type"`
	Severity           string `json:"severity"`
	Status             string `json:"status"`
	EmployeeNo         string `json:"employee_no"`
	FingerprintDisplay string `json:"fingerprint_display"`
	ObservedValue      string `json:"observed_value"`
	ThresholdValue     string `json:"threshold_value"`
	Reason             string `json:"reason"`
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
	EmployeeNo         string         `json:"employee_no"`
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
	RequestCount24h      int64 `json:"request_count_24h"`
	SuccessCount24h      int64 `json:"success_count_24h"`
	ErrorCount24h        int64 `json:"error_count_24h"`
	TotalTokens24h       int64 `json:"total_tokens_24h"`
	OpenAnomalies        int64 `json:"open_anomalies"`
	OpenCoverageAlerts   int64 `json:"open_coverage_alerts"`
	RawOnlyTraceCount24h int64 `json:"raw_only_trace_count_24h"`
}

type UsageFilter struct {
	EmployeeNo       string
	TokenFingerprint string
	Model            string
	RoutePattern     string
	BucketSize       string
	Limit            int
}

type UsageBucket struct {
	BucketStart        string `json:"bucket_start"`
	BucketSize         string `json:"bucket_size"`
	EmployeeNo         string `json:"employee_no"`
	FingerprintDisplay string `json:"fingerprint_display"`
	Model              string `json:"model"`
	RoutePattern       string `json:"route_pattern"`
	RequestCount       int64  `json:"request_count"`
	SuccessCount       int64  `json:"success_count"`
	ErrorCount         int64  `json:"error_count"`
	TotalTokens        int64  `json:"total_tokens"`
	EstimatedCost      string `json:"estimated_cost"`
}

type TokenIdentityFilter struct {
	EmployeeNo       string
	TokenFingerprint string
	Limit            int
}

type TokenIdentitySummary struct {
	FingerprintDisplay string `json:"fingerprint_display"`
	TokenFingerprint   string `json:"token_fingerprint"`
	NewAPITokenID      int    `json:"new_api_token_id"`
	TokenNameRaw       string `json:"token_name_raw"`
	EmployeeNo         string `json:"employee_no"`
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
	EmployeeNoPattern string `json:"employee_no_pattern"`
	MetricsEnabled    bool   `json:"metrics_enabled"`
	LookupLimit       int    `json:"lookup_limit"`
	RawAccessLimit    int    `json:"raw_access_limit"`
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
