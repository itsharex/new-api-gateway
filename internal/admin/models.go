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
}

type Principal struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        Role   `json:"role"`
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
