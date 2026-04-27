package jobs

import "context"

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
