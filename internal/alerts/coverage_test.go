package alerts

import (
	"testing"
	"time"
)

func TestKnownRawFirst(t *testing.T) {
	alert := KnownRawFirst("POST", "/mj/*", "/mj/submit/imagine", "midjourney", "trace-123")

	if alert.AlertCode != "known_route_raw_first" {
		t.Fatalf("AlertCode = %q", alert.AlertCode)
	}
	if alert.Severity != "medium" {
		t.Fatalf("Severity = %q", alert.Severity)
	}
	if alert.Method != "POST" {
		t.Fatalf("Method = %q", alert.Method)
	}
	if alert.RoutePattern != "/mj/*" {
		t.Fatalf("RoutePattern = %q", alert.RoutePattern)
	}
	if alert.RawPath != "/mj/submit/imagine" {
		t.Fatalf("RawPath = %q", alert.RawPath)
	}
	if alert.ProtocolFamily != "midjourney" {
		t.Fatalf("ProtocolFamily = %q", alert.ProtocolFamily)
	}
	if alert.SampleTraceID != "trace-123" {
		t.Fatalf("SampleTraceID = %q", alert.SampleTraceID)
	}
	if alert.FirstSeenAt.IsZero() {
		t.Fatal("FirstSeenAt is zero")
	}
	if alert.LastSeenAt.IsZero() {
		t.Fatal("LastSeenAt is zero")
	}
	if alert.FirstSeenAt.Location() != time.UTC {
		t.Fatalf("FirstSeenAt location = %q", alert.FirstSeenAt.Location())
	}
	if alert.LastSeenAt.Location() != time.UTC {
		t.Fatalf("LastSeenAt location = %q", alert.LastSeenAt.Location())
	}
	if alert.FirstSeenAt != alert.LastSeenAt {
		t.Fatalf("FirstSeenAt = %v, LastSeenAt = %v", alert.FirstSeenAt, alert.LastSeenAt)
	}
}
