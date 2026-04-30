package traces

import "time"

type Trace struct {
	TraceID                         string
	ParentTraceID                   string
	RequestIDFromClient             string
	NewAPIRequestID                 string
	Method                          string
	Path                            string
	RoutePattern                    string
	ProtocolFamily                  string
	CaptureMode                     string
	RouteSupportLevel               string
	BodyKind                        string
	StatusCode                      int
	UpstreamStatusCode              int
	Stream                          bool
	RequestStartedAt                time.Time
	ResponseStartedAt               time.Time
	ResponseFinishedAt              time.Time
	DurationMillis                  int64
	ClientIPHash                    string
	UserAgentHash                   string
	RequestBodySize                 int64
	ResponseBodySize                int64
	RequestBodySHA256               string
	ResponseBodySHA256              string
	RequestRawRef                   string
	RequestHeadersRef               string
	ResponseRawRef                  string
	ResponseHeadersRef              string
	TokenFingerprint                string
	FingerprintDisplay              string
	NewAPITokenIDSnapshot           int
	TokenNameSnapshot               string
	EmployeeNoSnapshot              string
	AuditSubjectDisplayNameSnapshot string
	DepartmentSnapshot              string
	IdentityResolutionStatus        string
	IdentityCacheStatus             string
	IdentityResolvedAt              time.Time
	ModelRequested                  string
	ModelUpstream                   string
	UsagePromptTokens               int
	UsageCompletionTokens           int
	UsageTotalTokens                int
	UsageReasoningTokens            int
	UsageCachedTokens               int
	EstimatedCost                   string
	ErrorType                       string
	ErrorMessageRedacted            string
	AnalysisStatus                  string
	CreatedAt                       time.Time
	UpdatedAt                       time.Time
}

type RawEvidenceObject struct {
	TraceID          string
	ObjectType       string
	ObjectRef        string
	StorageBackend   string
	ContentType      string
	ContentEncoding  string
	OriginalFilename string
	SizeBytes        int64
	SHA256           string
	RedactionStatus  string
	EncryptionStatus string
	CreatedAt        time.Time
}
