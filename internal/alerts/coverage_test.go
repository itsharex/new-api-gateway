package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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

func TestPostgresRepositoryUpsertsCoverageAlert(t *testing.T) {
	execer := &fakeCoverageExecer{}
	repo := NewPostgresRepository(execer)
	alert := CoverageAlert{
		AlertCode:      "known_route_raw_first",
		Severity:       "medium",
		Method:         "POST",
		RoutePattern:   "/mj/*",
		RawPath:        "/mj/submit",
		ContentType:    "application/json",
		ProtocolFamily: "midjourney",
		Message:        "raw first",
		SampleTraceID:  "trace_1",
		FirstSeenAt:    time.Unix(1000, 0).UTC(),
		LastSeenAt:     time.Unix(1001, 0).UTC(),
	}

	if err := repo.EmitCoverageAlert(context.Background(), alert); err != nil {
		t.Fatalf("EmitCoverageAlert error: %v", err)
	}
	if !strings.Contains(execer.sql, "INSERT INTO coverage_alerts") {
		t.Fatalf("sql = %s", execer.sql)
	}
	if !strings.Contains(execer.sql, "ON CONFLICT (alert_id)") {
		t.Fatalf("sql missing upsert: %s", execer.sql)
	}
	if len(execer.arguments) == 0 {
		t.Fatal("expected arguments")
	}
	if alertID, ok := execer.arguments[0].(string); !ok || alertID == "" {
		t.Fatalf("alert id argument = %#v", execer.arguments[0])
	}
}

type fakeCoverageExecer struct {
	sql       string
	arguments []any
	err       error
}

func (e *fakeCoverageExecer) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	e.sql = sql
	e.arguments = arguments
	return pgconn.NewCommandTag("INSERT 0 1"), e.err
}
