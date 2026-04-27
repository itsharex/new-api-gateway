package traces

import (
	"context"
	"testing"
	"time"
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
