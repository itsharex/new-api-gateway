package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/your-company/new-api-gateway/internal/alerts"
	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/identity"
	"github.com/your-company/new-api-gateway/internal/jobs"
	"github.com/your-company/new-api-gateway/internal/routes"
	"github.com/your-company/new-api-gateway/internal/traces"
)

type memoryTraceRepo struct {
	traces         []traces.Trace
	rawEvidence    []traces.RawEvidenceObject
	insertTraceErr error
	insertRawErr   error
}

func (m *memoryTraceRepo) InsertTrace(ctx context.Context, trace traces.Trace) error {
	m.traces = append(m.traces, trace)
	return m.insertTraceErr
}
func (m *memoryTraceRepo) InsertRawEvidence(ctx context.Context, object traces.RawEvidenceObject) error {
	m.rawEvidence = append(m.rawEvidence, object)
	return m.insertRawErr
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

func TestProxyReportsTraceInsertFailureWithoutMaskingUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	insertErr := errors.New("trace insert failed")
	repo := &memoryTraceRepo{insertTraceErr: insertErr}
	var auditErrors []error
	handler := testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir()))
	handler.AuditError = func(ctx context.Context, err error) {
		auditErrors = append(auditErrors, err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(auditErrors) != 1 {
		t.Fatalf("expected 1 audit error, got %d", len(auditErrors))
	}
	if !errors.Is(auditErrors[0], insertErr) {
		t.Fatalf("audit error = %v, want %v", auditErrors[0], insertErr)
	}
}

func TestProxyPublishesTraceCapturedJobAfterTracePersistence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	publisher := &recordingJobPublisher{}
	handler := testHandler(upstream.URL, &memoryTraceRepo{}, evidence.NewFilesystemStore(t.TempDir()))
	handler.JobPublisher = publisher

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(publisher.jobs) != 1 {
		t.Fatalf("published jobs = %d, want 1", len(publisher.jobs))
	}
	job := publisher.jobs[0]
	if job.Type != "trace_captured" {
		t.Fatalf("job Type = %q", job.Type)
	}
	if job.RoutePattern != "/v1/chat/completions" {
		t.Fatalf("job RoutePattern = %q", job.RoutePattern)
	}
	if job.ProtocolFamily != "openai_chat" {
		t.Fatalf("job ProtocolFamily = %q", job.ProtocolFamily)
	}
	if job.CaptureMode != string(routes.CaptureRawAndNormalized) {
		t.Fatalf("job CaptureMode = %q", job.CaptureMode)
	}
	if job.EmployeeNo != "E12345" {
		t.Fatalf("job EmployeeNo = %q", job.EmployeeNo)
	}
}

func TestProxyPublishesTraceCapturedJobAfterRawEvidencePersistence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	var events []string
	repo := &orderedTraceRepo{events: &events}
	publisher := &orderedJobPublisher{events: &events}
	handler := testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir()))
	handler.JobPublisher = publisher

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	want := []string{"trace", "raw:request_body", "raw:response_body", "publish"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestProxyDoesNotPublishTraceCapturedJobWhenRawEvidencePersistenceFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	insertErr := errors.New("raw evidence insert failed")
	publisher := &recordingJobPublisher{}
	handler := testHandler(upstream.URL, &memoryTraceRepo{insertRawErr: insertErr}, evidence.NewFilesystemStore(t.TempDir()))
	handler.JobPublisher = publisher

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(publisher.jobs) != 0 {
		t.Fatalf("published jobs = %d, want 0", len(publisher.jobs))
	}
}

func TestProxyReportsTraceCapturedPublishFailureWithoutMaskingUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer upstream.Close()

	publishErr := errors.New("publish failed")
	var auditErrors []error
	handler := testHandler(upstream.URL, &memoryTraceRepo{}, evidence.NewFilesystemStore(t.TempDir()))
	handler.JobPublisher = &recordingJobPublisher{err: publishErr}
	handler.AuditError = func(ctx context.Context, err error) {
		auditErrors = append(auditErrors, err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(auditErrors) != 1 || !errors.Is(auditErrors[0], publishErr) {
		t.Fatalf("audit errors = %v, want %v", auditErrors, publishErr)
	}
}

func TestProxyEmitsCoverageAlertForKnownRawFirstRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	emitter := &recordingCoverageEmitter{}
	handler := testHandler(upstream.URL, &memoryTraceRepo{}, evidence.NewFilesystemStore(t.TempDir()))
	handler.CoverageEmitter = emitter

	req := httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(emitter.alerts) != 1 {
		t.Fatalf("coverage alerts = %d, want 1", len(emitter.alerts))
	}
	alert := emitter.alerts[0]
	if alert.AlertCode != "known_route_raw_first" {
		t.Fatalf("AlertCode = %q", alert.AlertCode)
	}
	if alert.RoutePattern != "/mj/*" {
		t.Fatalf("RoutePattern = %q", alert.RoutePattern)
	}
	if alert.RawPath != "/mj/submit/imagine" {
		t.Fatalf("RawPath = %q", alert.RawPath)
	}
}

func TestProxyEmitsCoverageAlertForUnknownRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	emitter := &recordingCoverageEmitter{}
	handler := testHandler(upstream.URL, &memoryTraceRepo{}, evidence.NewFilesystemStore(t.TempDir()))
	handler.CoverageEmitter = emitter

	req := httptest.NewRequest(http.MethodPost, "/unmapped/provider/task", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(emitter.alerts) != 1 {
		t.Fatalf("coverage alerts = %d, want 1", len(emitter.alerts))
	}
	alert := emitter.alerts[0]
	if alert.AlertCode != "unknown_route" {
		t.Fatalf("AlertCode = %q", alert.AlertCode)
	}
	if alert.RawPath != "/unmapped/provider/task" {
		t.Fatalf("RawPath = %q", alert.RawPath)
	}
	if alert.ContentType != "application/json" {
		t.Fatalf("ContentType = %q", alert.ContentType)
	}
}

func TestProxyEmitsCoverageAlertForUnknownRouteWhenUpstreamFails(t *testing.T) {
	emitter := &recordingCoverageEmitter{}
	handler := testHandler("https://upstream.test", &memoryTraceRepo{}, evidence.NewFilesystemStore(t.TempDir()))
	handler.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("upstream unavailable")
	})}
	handler.CoverageEmitter = emitter

	req := httptest.NewRequest(http.MethodPost, "/unmapped/provider/task", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(emitter.alerts) != 1 {
		t.Fatalf("coverage alerts = %d, want 1", len(emitter.alerts))
	}
	if emitter.alerts[0].AlertCode != "unknown_route" {
		t.Fatalf("AlertCode = %q", emitter.alerts[0].AlertCode)
	}
}

func TestProxyDoesNotEmitCoverageAlertWhenTracePersistenceFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	emitter := &recordingCoverageEmitter{}
	handler := testHandler(upstream.URL, &memoryTraceRepo{insertTraceErr: errors.New("trace insert failed")}, evidence.NewFilesystemStore(t.TempDir()))
	handler.CoverageEmitter = emitter

	req := httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(emitter.alerts) != 0 {
		t.Fatalf("coverage alerts = %d, want 0", len(emitter.alerts))
	}
}

func TestProxyReportsRawEvidenceInsertFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	insertErr := errors.New("raw evidence insert failed")
	repo := &memoryTraceRepo{insertRawErr: insertErr}
	var auditErrors []error
	handler := testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir()))
	handler.AuditError = func(ctx context.Context, err error) {
		auditErrors = append(auditErrors, err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(auditErrors) != 2 {
		t.Fatalf("expected 2 audit errors, got %d", len(auditErrors))
	}
	for _, err := range auditErrors {
		if !errors.Is(err, insertErr) {
			t.Fatalf("audit error = %v, want %v", err, insertErr)
		}
	}
}

func TestProxyDoesNotMaskResponseEvidenceFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer upstream.Close()

	storeErr := errors.New("response evidence failed")
	store := &selectiveEvidenceStore{
		objects: map[string]evidence.Object{
			"request_body": {
				ObjectRef:      "request.bin",
				StorageBackend: "test",
				SizeBytes:      2,
				SHA256:         "request-sha",
				CreatedAt:      time.Unix(1000, 0).UTC(),
			},
		},
		errs: map[string]error{"response_body": storeErr},
	}
	repo := &memoryTraceRepo{}
	var auditErrors []error
	handler := testHandler(upstream.URL, repo, store)
	handler.AuditError = func(ctx context.Context, err error) {
		auditErrors = append(auditErrors, err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != `{"accepted":true}` {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(repo.traces))
	}
	if repo.traces[0].ResponseBodySize != int64(len(`{"accepted":true}`)) {
		t.Fatalf("ResponseBodySize = %d", repo.traces[0].ResponseBodySize)
	}
	if repo.traces[0].ResponseRawRef != "" {
		t.Fatalf("ResponseRawRef = %q", repo.traces[0].ResponseRawRef)
	}
	if len(auditErrors) != 1 || !errors.Is(auditErrors[0], storeErr) {
		t.Fatalf("audit errors = %v, want %v", auditErrors, storeErr)
	}
}

func TestProxyRecordsTraceWhenResponseBodyReadFails(t *testing.T) {
	readErr := errors.New("upstream read failed")
	repo := &memoryTraceRepo{}
	var auditErrors []error
	handler := testHandler("https://upstream.test", repo, evidence.NewFilesystemStore(t.TempDir()))
	handler.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       errReadCloser{err: readErr},
			Request:    req,
		}, nil
	})}
	handler.AuditError = func(ctx context.Context, err error) {
		auditErrors = append(auditErrors, err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(repo.traces))
	}
	if repo.traces[0].UpstreamStatusCode != http.StatusOK {
		t.Fatalf("UpstreamStatusCode = %d", repo.traces[0].UpstreamStatusCode)
	}
	if len(auditErrors) != 1 || !errors.Is(auditErrors[0], readErr) {
		t.Fatalf("audit errors = %v, want %v", auditErrors, readErr)
	}
}

func TestProxyStripsHopByHopHeaders(t *testing.T) {
	var upstreamHeaders http.Header
	handler := testHandler("https://upstream.test", &memoryTraceRepo{}, evidence.NewFilesystemStore(t.TempDir()))
	handler.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		upstreamHeaders = req.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Connection":        []string{"X-Response-Hop"},
				"Keep-Alive":        []string{"timeout=5"},
				"Proxy-Connection":  []string{"keep-alive"},
				"TE":                []string{"trailers"},
				"Trailer":           []string{"X-Trailer"},
				"Transfer-Encoding": []string{"chunked"},
				"Upgrade":           []string{"websocket"},
				"X-Response-Hop":    []string{"strip-me"},
				"X-Response-Normal": []string{"keep-me"},
				"Content-Type":      []string{"application/json"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request: req,
		}, nil
	})}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	req.Header.Set("Connection", "X-Request-Hop")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Proxy-Connection", "keep-alive")
	req.Header.Set("TE", "trailers")
	req.Header.Set("Trailer", "X-Trailer")
	req.Header.Set("Transfer-Encoding", "chunked")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("X-Request-Hop", "strip-me")
	req.Header.Set("X-Request-Normal", "keep-me")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade", "X-Request-Hop"} {
		if upstreamHeaders.Get(name) != "" {
			t.Fatalf("upstream header %s = %q, want stripped", name, upstreamHeaders.Get(name))
		}
	}
	if upstreamHeaders.Get("X-Request-Normal") != "keep-me" {
		t.Fatalf("upstream X-Request-Normal = %q", upstreamHeaders.Get("X-Request-Normal"))
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade", "X-Response-Hop"} {
		if rec.Header().Get(name) != "" {
			t.Fatalf("response header %s = %q, want stripped", name, rec.Header().Get(name))
		}
	}
	if rec.Header().Get("X-Response-Normal") != "keep-me" {
		t.Fatalf("response X-Response-Normal = %q", rec.Header().Get("X-Response-Normal"))
	}
}

func TestProxyRejectsAuthenticatedRequestWithShortAuditSecret(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler := testHandler(upstream.URL, &memoryTraceRepo{}, evidence.NewFilesystemStore(t.TempDir()))
	handler.AuditSecret = "too-short"

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if upstreamCalled {
		t.Fatal("upstream was called")
	}
}

func TestProxyAllowsMissingAPIKeyWithShortAuditSecret(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	repo := &memoryTraceRepo{}
	handler := testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir()))
	handler.AuditSecret = "too-short"

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(repo.traces))
	}
	if repo.traces[0].IdentityResolutionStatus != "extract_failed" {
		t.Fatalf("IdentityResolutionStatus = %q", repo.traces[0].IdentityResolutionStatus)
	}
}

func TestProxyRejectsRequestBodyOverLimitBeforeUpstream(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler := testHandler(upstream.URL, &memoryTraceRepo{}, evidence.NewFilesystemStore(t.TempDir()))
	handler.MaxRequestBodyBytes = 4

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("12345"))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if upstreamCalled {
		t.Fatal("upstream was called")
	}
}

func TestProxyStreamsSSEBeforeUpstreamEOFAndRecordsStreamTrace(t *testing.T) {
	streamBody := newControlledReadCloser()
	repo := &memoryTraceRepo{}
	handler := testHandler("https://upstream.test", repo, evidence.NewFilesystemStore(t.TempDir()))
	handler.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       streamBody,
			Request:    req,
		}, nil
	})}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := newObservedRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(rec, req)
	}()

	streamBody.send([]byte("data: one\n\n"))
	select {
	case <-rec.headerWritten:
	case <-time.After(250 * time.Millisecond):
		streamBody.close()
		<-done
		t.Fatal("streaming response was not written before upstream EOF")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !rec.Flushed {
		t.Fatal("expected streaming response to flush")
	}

	streamBody.close()
	<-done

	if rec.Body.String() != "data: one\n\n" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(repo.traces))
	}
	if !repo.traces[0].Stream {
		t.Fatal("trace Stream = false, want true")
	}
	if repo.traces[0].ResponseBodySize != int64(len("data: one\n\n")) {
		t.Fatalf("ResponseBodySize = %d", repo.traces[0].ResponseBodySize)
	}
	if repo.traces[0].ResponseRawRef == "" {
		t.Fatal("ResponseRawRef is empty")
	}
}

func TestProxyStreamingResponseEvidenceFailureDoesNotMaskClientResponse(t *testing.T) {
	storeErr := errors.New("stream evidence failed")
	store := &selectiveEvidenceStore{
		objects: map[string]evidence.Object{
			"request_body": {
				ObjectRef:      "request.bin",
				StorageBackend: "test",
				SizeBytes:      2,
				SHA256:         "request-sha",
				CreatedAt:      time.Unix(1000, 0).UTC(),
			},
		},
		errs: map[string]error{"response_body": storeErr},
	}
	repo := &memoryTraceRepo{}
	var auditErrors []error
	handler := testHandler("https://upstream.test", repo, store)
	handler.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: one\n\n")),
			Request:    req,
		}, nil
	})}
	handler.AuditError = func(ctx context.Context, err error) {
		auditErrors = append(auditErrors, err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("streaming response blocked after evidence store failure")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "data: one\n\n" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(repo.traces))
	}
	if repo.traces[0].ResponseRawRef != "" {
		t.Fatalf("ResponseRawRef = %q", repo.traces[0].ResponseRawRef)
	}
	if len(auditErrors) == 0 || !errors.Is(auditErrors[0], storeErr) {
		t.Fatalf("audit errors = %v, want %v", auditErrors, storeErr)
	}
}

func TestProxyStreamingAuditPersistenceSurvivesCanceledRequestContext(t *testing.T) {
	repo := &contextRejectingTraceRepo{}
	handler := testHandler("https://upstream.test", repo, evidence.NewFilesystemStore(t.TempDir()))

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)).WithContext(ctx)
	cancel()

	rec := httptest.NewRecorder()
	entry, ok := routes.DefaultRegistry().Match(http.MethodPost, "/v1/chat/completions")
	if !ok {
		t.Fatal("default registry did not match chat completions route")
	}
	handler.serveStreamingResponse(rec, req, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: one\n\n")),
		Request:    req,
	}, traceRecord{
		traceID:      "trace_stream_cancel",
		req:          req,
		entry:        entry,
		statusCode:   http.StatusOK,
		upstreamCode: http.StatusOK,
		startedAt:    time.Unix(1000, 0).UTC(),
		requestObject: evidence.Object{
			ObjectRef:      "request.bin",
			StorageBackend: "test",
			SizeBytes:      2,
			SHA256:         "request-sha",
			CreatedAt:      time.Unix(1000, 0).UTC(),
		},
		requestSize: 2,
		stream:      true,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace despite canceled request context, got %d", len(repo.traces))
	}
	if repo.traces[0].ResponseRawRef == "" {
		t.Fatal("ResponseRawRef is empty")
	}
}

func testHandler(upstreamURL string, repo traces.Repository, store evidence.Store) Handler {
	return Handler{
		UpstreamBaseURL:  upstreamURL,
		Registry:         routes.DefaultRegistry(),
		EvidenceStore:    store,
		TraceRepo:        repo,
		IdentityResolver: fixedResolver{},
		AuditSecret:      "0123456789abcdef0123456789abcdef",
		Now:              func() time.Time { return time.Unix(1000, 0).UTC() },
	}
}

type contextRejectingTraceRepo struct {
	traces      []traces.Trace
	rawEvidence []traces.RawEvidenceObject
}

func (r *contextRejectingTraceRepo) InsertTrace(ctx context.Context, trace traces.Trace) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.traces = append(r.traces, trace)
	return nil
}

func (r *contextRejectingTraceRepo) InsertRawEvidence(ctx context.Context, object traces.RawEvidenceObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.rawEvidence = append(r.rawEvidence, object)
	return nil
}

type selectiveEvidenceStore struct {
	objects map[string]evidence.Object
	errs    map[string]error
}

func (s *selectiveEvidenceStore) Put(ctx context.Context, req evidence.PutRequest) (evidence.Object, error) {
	if err := s.errs[req.ObjectType]; err != nil {
		return evidence.Object{}, err
	}
	if object, ok := s.objects[req.ObjectType]; ok {
		return object, nil
	}
	body, _ := io.ReadAll(req.Reader)
	return evidence.Object{
		ObjectRef:      req.ObjectType + ".bin",
		StorageBackend: "test",
		ContentType:    req.ContentType,
		SizeBytes:      int64(len(body)),
		SHA256:         req.ObjectType + "-sha",
		CreatedAt:      time.Unix(1000, 0).UTC(),
	}, nil
}

func (s *selectiveEvidenceStore) Get(ctx context.Context, objectRef string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errReadCloser struct {
	err error
}

func (r errReadCloser) Read(p []byte) (int, error) {
	return 0, r.err
}

func (r errReadCloser) Close() error {
	return nil
}

type recordingJobPublisher struct {
	jobs []jobs.TraceCapturedJob
	err  error
}

func (p *recordingJobPublisher) PublishTraceCaptured(ctx context.Context, job jobs.TraceCapturedJob) error {
	p.jobs = append(p.jobs, job)
	return p.err
}

type orderedTraceRepo struct {
	events *[]string
}

func (r *orderedTraceRepo) InsertTrace(ctx context.Context, trace traces.Trace) error {
	*r.events = append(*r.events, "trace")
	return nil
}

func (r *orderedTraceRepo) InsertRawEvidence(ctx context.Context, object traces.RawEvidenceObject) error {
	*r.events = append(*r.events, "raw:"+object.ObjectType)
	return nil
}

type orderedJobPublisher struct {
	events *[]string
}

func (p *orderedJobPublisher) PublishTraceCaptured(ctx context.Context, job jobs.TraceCapturedJob) error {
	*p.events = append(*p.events, "publish")
	return nil
}

type recordingCoverageEmitter struct {
	alerts []alerts.CoverageAlert
	err    error
}

func (e *recordingCoverageEmitter) EmitCoverageAlert(ctx context.Context, alert alerts.CoverageAlert) error {
	e.alerts = append(e.alerts, alert)
	return e.err
}

type observedRecorder struct {
	*httptest.ResponseRecorder
	headerWritten chan struct{}
	once          sync.Once
}

func newObservedRecorder() *observedRecorder {
	return &observedRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		headerWritten:    make(chan struct{}),
	}
}

func (r *observedRecorder) WriteHeader(code int) {
	r.ResponseRecorder.WriteHeader(code)
	r.once.Do(func() { close(r.headerWritten) })
}

func (r *observedRecorder) Write(p []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(p)
	r.once.Do(func() { close(r.headerWritten) })
	return n, err
}

func (r *observedRecorder) Flush() {
	r.ResponseRecorder.Flush()
}

type controlledReadCloser struct {
	chunks chan []byte
	once   sync.Once
}

func newControlledReadCloser() *controlledReadCloser {
	return &controlledReadCloser{chunks: make(chan []byte, 1)}
}

func (r *controlledReadCloser) Read(p []byte) (int, error) {
	chunk, ok := <-r.chunks
	if !ok {
		return 0, io.EOF
	}
	return copy(p, chunk), nil
}

func (r *controlledReadCloser) Close() error {
	r.close()
	return nil
}

func (r *controlledReadCloser) send(chunk []byte) {
	r.chunks <- chunk
}

func (r *controlledReadCloser) close() {
	r.once.Do(func() { close(r.chunks) })
}
