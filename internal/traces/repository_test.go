package traces

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type memoryRepository struct {
	traces  []Trace
	objects []RawEvidenceObject
}

var _ Repository = (*memoryRepository)(nil)

func (m *memoryRepository) InsertTrace(ctx context.Context, trace Trace) error {
	m.traces = append(m.traces, trace)
	return nil
}

func (m *memoryRepository) InsertRawEvidence(ctx context.Context, object RawEvidenceObject) error {
	m.objects = append(m.objects, object)
	return nil
}

func TestRepositoryContractStoresTraceAndEvidence(t *testing.T) {
	repo := &memoryRepository{}
	trace := Trace{TraceID: "trace_1", Method: "POST", Path: "/v1/chat/completions", CreatedAt: time.Now().UTC()}
	object := RawEvidenceObject{TraceID: "trace_1", ObjectType: "request_body", ObjectRef: "raw/trace_1/request.body"}

	if err := repo.InsertTrace(context.Background(), trace); err != nil {
		t.Fatalf("InsertTrace error: %v", err)
	}
	if err := repo.InsertRawEvidence(context.Background(), object); err != nil {
		t.Fatalf("InsertRawEvidence error: %v", err)
	}
	if len(repo.traces) != 1 || len(repo.objects) != 1 {
		t.Fatalf("unexpected repo state %#v", repo)
	}
}

type recordingExecer struct {
	query string
	args  []any
}

func (r *recordingExecer) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	r.query = query
	r.args = append([]any(nil), args...)
	return pgconn.CommandTag{}, nil
}

func TestPostgresRepositoryZeroValueReturnsErrors(t *testing.T) {
	repo := PostgresRepository{}

	if err := repo.InsertTrace(context.Background(), validTrace()); err == nil || !strings.Contains(err.Error(), "nil database pool") {
		t.Fatalf("InsertTrace error = %v, want nil database pool error", err)
	}
	if err := repo.InsertRawEvidence(context.Background(), validRawEvidenceObject()); err == nil || !strings.Contains(err.Error(), "nil database pool") {
		t.Fatalf("InsertRawEvidence error = %v, want nil database pool error", err)
	}
}

func TestPostgresRepositoryValidatesRequiredTraceTimestamps(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Trace)
		wantErr string
	}{
		{
			name: "request started at",
			mutate: func(trace *Trace) {
				trace.RequestStartedAt = time.Time{}
			},
			wantErr: "request_started_at is required",
		},
		{
			name: "created at",
			mutate: func(trace *Trace) {
				trace.CreatedAt = time.Time{}
			},
			wantErr: "created_at is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := validTrace()
			tt.mutate(&trace)
			repo := PostgresRepository{execer: &recordingExecer{}}

			err := repo.InsertTrace(context.Background(), trace)

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("InsertTrace error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestPostgresRepositoryNormalizesZeroResponseFinishedAtToNull(t *testing.T) {
	execer := &recordingExecer{}
	repo := PostgresRepository{execer: execer}
	trace := validTrace()
	trace.ResponseFinishedAt = time.Time{}

	if err := repo.InsertTrace(context.Background(), trace); err != nil {
		t.Fatalf("InsertTrace error: %v", err)
	}

	if !strings.Contains(execer.query, "INSERT INTO traces") {
		t.Fatalf("query = %q, want traces insert", execer.query)
	}
	assertPlaceholderAlignment(t, execer.query, execer.args)
	if len(execer.args) != 36 {
		t.Fatalf("arg count = %d, want 36", len(execer.args))
	}
	assertArg(t, execer.args, 0, trace.TraceID)
	assertArg(t, execer.args, 1, trace.Method)
	assertArg(t, execer.args, 2, trace.Path)
	assertArg(t, execer.args, 3, trace.RoutePattern)
	assertArg(t, execer.args, 4, trace.ProtocolFamily)
	assertArg(t, execer.args, 6, trace.StatusCode)
	assertArg(t, execer.args, 9, trace.RequestStartedAt)
	if execer.args[10] != nil {
		t.Fatalf("response_finished_at arg = %#v, want nil", execer.args[10])
	}
	assertArg(t, execer.args, 17, trace.RequestHeadersRef)
	assertArg(t, execer.args, 18, trace.ResponseRawRef)
	assertArg(t, execer.args, 19, trace.ResponseHeadersRef)
	assertArg(t, execer.args, 20, trace.TokenFingerprint)
	assertArg(t, execer.args, 22, trace.NewAPITokenIDSnapshot)
	assertArg(t, execer.args, 24, trace.EmployeeNoSnapshot)
	assertArg(t, execer.args, 28, trace.UsagePromptTokens)
	assertArg(t, execer.args, 30, trace.UsageTotalTokens)
	assertArg(t, execer.args, 33, trace.EstimatedCost)
	assertArg(t, execer.args, 34, trace.AnalysisStatus)
	assertArg(t, execer.args, 35, trace.CreatedAt)
}

func TestPostgresRepositoryInsertRawEvidenceBuildsExpectedSQLArgs(t *testing.T) {
	execer := &recordingExecer{}
	repo := PostgresRepository{execer: execer}
	object := validRawEvidenceObject()

	if err := repo.InsertRawEvidence(context.Background(), object); err != nil {
		t.Fatalf("InsertRawEvidence error: %v", err)
	}

	if !strings.Contains(execer.query, "INSERT INTO raw_evidence_objects") {
		t.Fatalf("query = %q, want raw_evidence_objects insert", execer.query)
	}
	assertPlaceholderAlignment(t, execer.query, execer.args)
	if len(execer.args) != 8 {
		t.Fatalf("arg count = %d, want 8", len(execer.args))
	}
	assertArg(t, execer.args, 0, object.TraceID)
	assertArg(t, execer.args, 1, object.ObjectType)
	assertArg(t, execer.args, 2, object.ObjectRef)
	assertArg(t, execer.args, 3, object.StorageBackend)
	assertArg(t, execer.args, 5, object.SizeBytes)
	assertArg(t, execer.args, 6, object.SHA256)
	assertArg(t, execer.args, 7, object.CreatedAt)
}

func TestPostgresRepositoryValidatesRequiredRawEvidenceTimestamp(t *testing.T) {
	repo := PostgresRepository{execer: &recordingExecer{}}
	object := validRawEvidenceObject()
	object.CreatedAt = time.Time{}

	err := repo.InsertRawEvidence(context.Background(), object)

	if err == nil || !strings.Contains(err.Error(), "created_at is required") {
		t.Fatalf("InsertRawEvidence error = %v, want created_at validation error", err)
	}
}

func validTrace() Trace {
	startedAt := time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC)
	finishedAt := startedAt.Add(750 * time.Millisecond)
	return Trace{
		TraceID:                  "trace_1",
		Method:                   "POST",
		Path:                     "/v1/chat/completions",
		RoutePattern:             "/v1/chat/completions",
		ProtocolFamily:           "openai",
		CaptureMode:              "full",
		StatusCode:               200,
		UpstreamStatusCode:       200,
		Stream:                   true,
		RequestStartedAt:         startedAt,
		ResponseFinishedAt:       finishedAt,
		DurationMillis:           750,
		RequestBodySize:          128,
		ResponseBodySize:         256,
		RequestBodySHA256:        "request-sha",
		ResponseBodySHA256:       "response-sha",
		RequestRawRef:            "raw/trace_1/request.body",
		RequestHeadersRef:        "raw/trace_1/request_headers.bin",
		ResponseRawRef:           "raw/trace_1/response.body",
		ResponseHeadersRef:       "raw/trace_1/response_headers.bin",
		TokenFingerprint:         "fp_123",
		FingerprintDisplay:       "sk-...123",
		NewAPITokenIDSnapshot:    42,
		TokenNameSnapshot:        "prod-token",
		EmployeeNoSnapshot:       "E123",
		IdentityResolutionStatus: "resolved",
		IdentityCacheStatus:      "miss",
		ModelRequested:           "gpt-4.1",
		UsagePromptTokens:        10,
		UsageCompletionTokens:    20,
		UsageTotalTokens:         30,
		UsageReasoningTokens:     4,
		UsageCachedTokens:        2,
		EstimatedCost:            "0.0012",
		AnalysisStatus:           "pending",
		CreatedAt:                startedAt.Add(time.Second),
	}
}

func validRawEvidenceObject() RawEvidenceObject {
	return RawEvidenceObject{
		TraceID:        "trace_1",
		ObjectType:     "request_body",
		ObjectRef:      "raw/trace_1/request.body",
		StorageBackend: "filesystem",
		ContentType:    "application/json",
		SizeBytes:      128,
		SHA256:         "request-sha",
		CreatedAt:      time.Date(2026, 4, 27, 10, 30, 1, 0, time.UTC),
	}
}

func assertPlaceholderAlignment(t *testing.T, query string, args []any) {
	t.Helper()

	matches := regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(query, -1)
	if len(matches) != len(args) {
		t.Fatalf("placeholder count = %d, arg count = %d", len(matches), len(args))
	}

	maxPlaceholder := 0
	for _, match := range matches {
		placeholder, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("parse placeholder %q: %v", match[1], err)
		}
		if placeholder > maxPlaceholder {
			maxPlaceholder = placeholder
		}
	}
	if maxPlaceholder != len(args) {
		t.Fatalf("max placeholder = %d, arg count = %d", maxPlaceholder, len(args))
	}
}

func assertArg(t *testing.T, args []any, index int, want any) {
	t.Helper()

	if args[index] != want {
		t.Fatalf("arg %d = %v, want %v", index, args[index], want)
	}
}
