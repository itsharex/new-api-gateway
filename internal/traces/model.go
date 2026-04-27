package traces

import "time"

type Trace struct {
	TraceID                  string
	Method                   string
	Path                     string
	RoutePattern             string
	ProtocolFamily           string
	CaptureMode              string
	StatusCode               int
	UpstreamStatusCode       int
	Stream                   bool
	RequestStartedAt         time.Time
	ResponseFinishedAt       time.Time
	DurationMillis           int64
	RequestBodySize          int64
	ResponseBodySize         int64
	RequestBodySHA256        string
	ResponseBodySHA256       string
	RequestRawRef            string
	ResponseRawRef           string
	TokenFingerprint         string
	FingerprintDisplay       string
	NewAPITokenIDSnapshot    int
	TokenNameSnapshot        string
	EmployeeNoSnapshot       string
	IdentityResolutionStatus string
	IdentityCacheStatus      string
	ModelRequested           string
	AnalysisStatus           string
	CreatedAt                time.Time
}

type RawEvidenceObject struct {
	TraceID        string
	ObjectType     string
	ObjectRef      string
	StorageBackend string
	ContentType    string
	SizeBytes      int64
	SHA256         string
	CreatedAt      time.Time
}
