package alerts

import "time"

type CoverageAlert struct {
	AlertCode      string
	Severity       string
	Method         string
	RoutePattern   string
	RawPath        string
	ContentType    string
	ProtocolFamily string
	Message        string
	SampleTraceID  string
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
}

func KnownRawFirst(method, routePattern, rawPath, protocolFamily, traceID string) CoverageAlert {
	now := time.Now().UTC()
	return CoverageAlert{
		AlertCode:      "known_route_raw_first",
		Severity:       "medium",
		Method:         method,
		RoutePattern:   routePattern,
		RawPath:        rawPath,
		ProtocolFamily: protocolFamily,
		Message:        "route is captured with raw evidence and minimal metadata; deep normalizer is not enabled",
		SampleTraceID:  traceID,
		FirstSeenAt:    now,
		LastSeenAt:     now,
	}
}
