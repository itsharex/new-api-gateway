package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/identity"
	"github.com/your-company/new-api-gateway/internal/routes"
	"github.com/your-company/new-api-gateway/internal/traces"
)

type memoryTraceRepo struct {
	traces []traces.Trace
}

func (m *memoryTraceRepo) InsertTrace(ctx context.Context, trace traces.Trace) error {
	m.traces = append(m.traces, trace)
	return nil
}
func (m *memoryTraceRepo) InsertRawEvidence(ctx context.Context, object traces.RawEvidenceObject) error {
	return nil
}

type fixedResolver struct{}

func (fixedResolver) Resolve(ctx context.Context, canonicalKey, fingerprintValue, fingerprintDisplay string) (identity.Snapshot, error) {
	return identity.Snapshot{
		TokenFingerprint:    fingerprintValue,
		FingerprintDisplay:  fingerprintDisplay,
		NewAPITokenID:       7,
		TokenNameRaw:        "E12345",
		EmployeeNo:          "E12345",
		ResolutionStatus:    "resolved",
		IdentityCacheStatus: "test",
	}, nil
}

func TestProxyForwardsAndRecordsTrace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"model":"gpt-test","messages":[]}` {
			t.Fatalf("upstream body = %q", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl_test","usage":{"total_tokens":3}}`))
	}))
	defer upstream.Close()

	repo := &memoryTraceRepo{}
	handler := Handler{
		UpstreamBaseURL:  upstream.URL,
		Registry:         routes.DefaultRegistry(),
		EvidenceStore:    evidence.NewFilesystemStore(t.TempDir()),
		TraceRepo:        repo,
		IdentityResolver: fixedResolver{},
		AuditSecret:      "0123456789abcdef0123456789abcdef",
		Now:              func() time.Time { return time.Unix(1000, 0).UTC() },
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[]}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(repo.traces))
	}
	if repo.traces[0].EmployeeNoSnapshot != "E12345" {
		t.Fatalf("EmployeeNoSnapshot = %q", repo.traces[0].EmployeeNoSnapshot)
	}
}
