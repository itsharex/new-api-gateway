package gateway

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	if job.IdentityResolutionStatus != "resolved" {
		t.Fatalf("job IdentityResolutionStatus = %q", job.IdentityResolutionStatus)
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
	want := []string{"trace", "raw:request_body", "raw:request_headers", "raw:response_body", "raw:response_headers", "publish"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestProxyRecordsHeaderEvidenceAndMinimalMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-abc123" {
			t.Fatalf("upstream Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Request", "upstream-1")
		w.Header().Set("X-Request-Id", "new-api-request-id")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl_test",
			"usage": {
				"prompt_tokens": 11,
				"completion_tokens": 7,
				"total_tokens": 18,
				"prompt_tokens_details": {"cached_tokens": 3},
				"completion_tokens_details": {"reasoning_tokens": 2}
			}
		}`))
	}))
	defer upstream.Close()

	repo := &memoryTraceRepo{}
	publisher := &recordingJobPublisher{}
	handler := testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir()))
	handler.JobPublisher = publisher

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[]}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "client-request-id")
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.1")
	req.Header.Set("User-Agent", "audit-test-agent")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(repo.traces))
	}
	trace := repo.traces[0]
	if trace.RequestHeadersRef == "" || trace.ResponseHeadersRef == "" {
		t.Fatalf("header refs were not recorded: %+v", trace)
	}
	if trace.ModelRequested != "gpt-test" {
		t.Fatalf("ModelRequested = %q", trace.ModelRequested)
	}
	if trace.RouteSupportLevel != "deep_normalized" || trace.BodyKind != "json" {
		t.Fatalf("route/body metadata = %+v", trace)
	}
	if trace.ResponseStartedAt.IsZero() || trace.UpdatedAt.IsZero() {
		t.Fatalf("response/update timestamps missing: %+v", trace)
	}
	if trace.RequestIDFromClient != "client-request-id" || trace.NewAPIRequestID != "new-api-request-id" {
		t.Fatalf("request ids = %+v", trace)
	}
	if trace.ClientIPHash != handler.hashAuditValue("203.0.113.10") || trace.UserAgentHash != handler.hashAuditValue("audit-test-agent") {
		t.Fatalf("audit hashes = %+v", trace)
	}
	if trace.UsagePromptTokens != 11 || trace.UsageCompletionTokens != 7 || trace.UsageTotalTokens != 18 {
		t.Fatalf("usage = %+v", trace)
	}
	if trace.UsageCachedTokens != 3 || trace.UsageReasoningTokens != 2 {
		t.Fatalf("usage details = %+v", trace)
	}

	var objectTypes []string
	for _, object := range repo.rawEvidence {
		objectTypes = append(objectTypes, object.ObjectType)
	}
	got := strings.Join(objectTypes, ",")
	for _, want := range []string{"request_body", "request_headers", "response_body", "response_headers"} {
		if !strings.Contains(got, want) {
			t.Fatalf("raw evidence object types = %s, missing %s", got, want)
		}
	}
	if len(publisher.jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(publisher.jobs))
	}
	job := publisher.jobs[0]
	if job.RequestHeadersRef == "" || job.ResponseHeadersRef == "" || job.ResponseRawRef == "" {
		t.Fatalf("job evidence refs missing: %+v", job)
	}
	if job.ModelRequested != "gpt-test" || job.UsageTotalTokens != 18 {
		t.Fatalf("job metadata = %+v", job)
	}
	if job.TokenFingerprint == "" || job.FingerprintDisplay == "" {
		t.Fatalf("job fingerprint fields missing: %+v", job)
	}
	if job.NewAPITokenID != 7 || job.TokenNameSnapshot != "E12345" {
		t.Fatalf("job token snapshot fields = %+v", job)
	}
	if job.StatusCode != http.StatusOK || job.UpstreamStatusCode != http.StatusOK {
		t.Fatalf("job status fields = %+v", job)
	}
	if job.RequestStartedAt == "" || job.RequestBodySize == 0 || job.ResponseBodySize == 0 {
		t.Fatalf("job timing/body metadata missing: %+v", job)
	}
	if job.ClientIPHash != trace.ClientIPHash || job.UserAgentHash != trace.UserAgentHash {
		t.Fatalf("job audit hashes = %+v", job)
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
	if len(auditErrors) != 4 {
		t.Fatalf("expected 4 audit errors, got %d", len(auditErrors))
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

func TestProxyDoesNotPublishJobOrCoverageWhenResponseEvidenceStoreFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

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
		errs: map[string]error{"response_body": errors.New("response evidence failed")},
	}
	publisher := &recordingJobPublisher{}
	emitter := &recordingCoverageEmitter{}
	handler := testHandler(upstream.URL, &memoryTraceRepo{}, store)
	handler.JobPublisher = publisher
	handler.CoverageEmitter = emitter

	req := httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(publisher.jobs) != 0 {
		t.Fatalf("published jobs = %d, want 0", len(publisher.jobs))
	}
	if len(emitter.alerts) != 0 {
		t.Fatalf("coverage alerts = %d, want 0", len(emitter.alerts))
	}
}

func TestProxyNonStreamingAuditPersistenceSurvivesCanceledRequestContext(t *testing.T) {
	repo := &contextRejectingTraceRepo{}
	store := &contextRejectingEvidenceStore{}
	publisher := &recordingJobPublisher{}
	handler := testHandler("https://upstream.test", repo, store)
	handler.JobPublisher = publisher

	ctx, cancel := context.WithCancel(context.Background())
	handler.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       cancelOnEOFReadCloser{Reader: strings.NewReader(`{"ok":true}`), cancel: cancel},
			Request:    req,
		}, nil
	})}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace despite canceled request context, got %d", len(repo.traces))
	}
	if repo.traces[0].ResponseRawRef == "" {
		t.Fatal("ResponseRawRef is empty")
	}
	if len(publisher.jobs) != 1 {
		t.Fatalf("published jobs = %d, want 1", len(publisher.jobs))
	}
}

func TestProxyTunnelsRealtimeWebSocketUpgrade(t *testing.T) {
	upstreamSawUpgrade := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/realtime" {
			t.Fatalf("upstream path = %q", r.URL.Path)
		}
		if !headerContainsToken(r.Header, "Connection", "Upgrade") {
			t.Fatalf("upstream Connection = %q, want Upgrade token", r.Header.Values("Connection"))
		}
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			t.Fatalf("upstream Upgrade = %q", r.Header.Get("Upgrade"))
		}
		if r.Header.Get("Sec-WebSocket-Key") != "dGhlIHNhbXBsZSBub25jZQ==" {
			t.Fatalf("upstream Sec-WebSocket-Key = %q", r.Header.Get("Sec-WebSocket-Key"))
		}
		upstreamSawUpgrade <- struct{}{}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("upstream response writer does not support hijack")
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n"+
			"Connection: Upgrade\r\n"+
			"Upgrade: websocket\r\n"+
			"Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n"+
			"\r\n")
		line, err := rw.Reader.ReadString('\n')
		if err != nil {
			t.Errorf("upstream read tunneled line: %v", err)
			return
		}
		_, _ = io.WriteString(conn, "echo: "+line)
	}))
	defer upstream.Close()

	repo := &notifyingTraceRepo{traces: make(chan traces.Trace, 1)}
	proxy := httptest.NewServer(testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir())))
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = io.WriteString(conn, "GET /v1/realtime?model=gpt-realtime HTTP/1.1\r\n"+
		"Host: "+proxyURL.Host+"\r\n"+
		"Authorization: Bearer sk-abc123\r\n"+
		"Connection: Upgrade\r\n"+
		"Upgrade: websocket\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n"+
		"Sec-WebSocket-Protocol: realtime, openai-insecure-api-key.sk-abc123, openai-beta.realtime-v1\r\n"+
		"\r\n")
	if err != nil {
		t.Fatal(err)
	}
	clientReader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(clientReader, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	if !headerContainsToken(resp.Header, "Connection", "Upgrade") {
		t.Fatalf("response Connection = %q, want Upgrade token", resp.Header.Values("Connection"))
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		t.Fatalf("response Upgrade = %q", resp.Header.Get("Upgrade"))
	}
	select {
	case <-upstreamSawUpgrade:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("upstream did not receive websocket upgrade")
	}
	if _, err := io.WriteString(conn, "ping\n"); err != nil {
		t.Fatal(err)
	}
	echo, err := clientReader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if echo != "echo: ping\n" {
		t.Fatalf("tunneled echo = %q", echo)
	}
	_ = resp.Body.Close()
	_ = conn.Close()
	select {
	case trace := <-repo.traces:
		if trace.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("trace StatusCode = %d, want 101", trace.StatusCode)
		}
		if !trace.Stream {
			t.Fatal("trace Stream = false, want true")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("websocket trace was not persisted after tunnel closed")
	}
}

func TestProxyWebSocketHandshakeStopsWhenClientCancels(t *testing.T) {
	upstream, accepted := newStallingTCPServer(t)
	defer upstream.Close()

	repo := &notifyingTraceRepo{traces: make(chan traces.Trace, 1)}
	proxy := httptest.NewServer(testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir())))
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(conn, "GET /v1/realtime HTTP/1.1\r\n"+
		"Host: "+proxyURL.Host+"\r\n"+
		"Authorization: Bearer sk-abc123\r\n"+
		"Connection: Upgrade\r\n"+
		"Upgrade: websocket\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n"+
		"\r\n")
	if err != nil {
		t.Fatal(err)
	}
	upstreamConn := <-accepted
	defer upstreamConn.Close()

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case trace := <-repo.traces:
		if trace.StatusCode != http.StatusBadGateway {
			t.Fatalf("trace StatusCode = %d, want 502", trace.StatusCode)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("websocket handshake did not stop after client cancellation")
	}
}

func TestProxyWebSocketHandshakeTimesOutWhenUpstreamStalls(t *testing.T) {
	upstream, accepted := newStallingTCPServer(t)
	defer upstream.Close()

	repo := &notifyingTraceRepo{traces: make(chan traces.Trace, 1)}
	handler := testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir()))
	handler.WebSocketHandshakeTimeout = 10 * time.Millisecond
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = io.WriteString(conn, "GET /v1/realtime HTTP/1.1\r\n"+
		"Host: "+proxyURL.Host+"\r\n"+
		"Authorization: Bearer sk-abc123\r\n"+
		"Connection: Upgrade\r\n"+
		"Upgrade: websocket\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n"+
		"\r\n")
	if err != nil {
		t.Fatal(err)
	}
	upstreamConn := <-accepted
	defer upstreamConn.Close()

	select {
	case trace := <-repo.traces:
		if trace.StatusCode != http.StatusBadGateway {
			t.Fatalf("trace StatusCode = %d, want 502", trace.StatusCode)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("websocket handshake did not honor timeout")
	}
}

func TestProxyWebSocketTunnelStripsHopByHopRequestHeaders(t *testing.T) {
	upstreamHeaders := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHeaders <- r.Header.Clone()
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		w.Header().Set("Sec-WebSocket-Accept", "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=")
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer upstream.Close()

	repo := &notifyingTraceRepo{traces: make(chan traces.Trace, 1)}
	proxy := httptest.NewServer(testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir())))
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = io.WriteString(conn, "GET /v1/realtime HTTP/1.1\r\n"+
		"Host: "+proxyURL.Host+"\r\n"+
		"Authorization: Bearer sk-abc123\r\n"+
		"Connection: keep-alive, X-Request-Hop, Upgrade\r\n"+
		"Keep-Alive: timeout=5\r\n"+
		"Proxy-Connection: keep-alive\r\n"+
		"TE: trailers\r\n"+
		"Trailer: X-Trailer\r\n"+
		"Transfer-Encoding: chunked\r\n"+
		"Upgrade: websocket\r\n"+
		"X-Request-Hop: strip-me\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n"+
		"Sec-WebSocket-Protocol: realtime, openai-insecure-api-key.sk-abc123\r\n"+
		"\r\n"+
		"0\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	_ = conn.Close()

	var headers http.Header
	select {
	case headers = <-upstreamHeaders:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("upstream did not receive websocket request")
	}
	for _, name := range []string{"Keep-Alive", "Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "X-Request-Hop"} {
		if headers.Get(name) != "" {
			t.Fatalf("upstream header %s = %q, want stripped", name, headers.Get(name))
		}
	}
	if got := headers.Values("Connection"); len(got) != 1 || !strings.EqualFold(got[0], "Upgrade") {
		t.Fatalf("upstream Connection = %q, want only Upgrade", got)
	}
	if !strings.EqualFold(headers.Get("Upgrade"), "websocket") {
		t.Fatalf("upstream Upgrade = %q", headers.Get("Upgrade"))
	}
	if headers.Get("Authorization") != "Bearer sk-abc123" {
		t.Fatalf("upstream Authorization = %q", headers.Get("Authorization"))
	}
	if headers.Get("Sec-WebSocket-Key") != "dGhlIHNhbXBsZSBub25jZQ==" {
		t.Fatalf("upstream Sec-WebSocket-Key = %q", headers.Get("Sec-WebSocket-Key"))
	}
	if headers.Get("Sec-WebSocket-Version") != "13" {
		t.Fatalf("upstream Sec-WebSocket-Version = %q", headers.Get("Sec-WebSocket-Version"))
	}
	if headers.Get("Sec-WebSocket-Protocol") != "realtime, openai-insecure-api-key.sk-abc123" {
		t.Fatalf("upstream Sec-WebSocket-Protocol = %q", headers.Get("Sec-WebSocket-Protocol"))
	}
}

func TestProxyForwardsNonSwitchingWebSocketResponseWithoutTunnel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Upstream-Reason", "denied")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "denied\n")
	}))
	defer upstream.Close()

	repo := &notifyingTraceRepo{traces: make(chan traces.Trace, 1)}
	proxy := httptest.NewServer(testHandler(upstream.URL, repo, evidence.NewFilesystemStore(t.TempDir())))
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", proxyURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = io.WriteString(conn, "GET /v1/realtime HTTP/1.1\r\n"+
		"Host: "+proxyURL.Host+"\r\n"+
		"Authorization: Bearer sk-abc123\r\n"+
		"Connection: Upgrade\r\n"+
		"Upgrade: websocket\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n"+
		"\r\n")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if string(body) != "denied\n" {
		t.Fatalf("body = %q, want denied", string(body))
	}
	if resp.Header.Get("X-Upstream-Reason") != "denied" {
		t.Fatalf("X-Upstream-Reason = %q", resp.Header.Get("X-Upstream-Reason"))
	}
	select {
	case trace := <-repo.traces:
		if trace.StatusCode != http.StatusForbidden {
			t.Fatalf("trace StatusCode = %d, want 403", trace.StatusCode)
		}
		if trace.UpstreamStatusCode != http.StatusForbidden {
			t.Fatalf("trace UpstreamStatusCode = %d, want 403", trace.UpstreamStatusCode)
		}
		if !trace.Stream {
			t.Fatal("trace Stream = false, want true")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("websocket non-101 trace was not persisted")
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

func TestProxyCapturesResponseHeadersAndDoesNotPublishJobWhenResponseBodyReadFails(t *testing.T) {
	readErr := errors.New("upstream read failed")
	repo := &memoryTraceRepo{}
	publisher := &recordingJobPublisher{}
	handler := testHandler("https://upstream.test", repo, evidence.NewFilesystemStore(t.TempDir()))
	handler.JobPublisher = publisher
	handler.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":       []string{"application/json"},
				"X-Upstream-Request": []string{"upstream-1"},
			},
			Body:    errReadCloser{err: readErr},
			Request: req,
		}, nil
	})}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[]}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(repo.traces))
	}
	if repo.traces[0].ResponseHeadersRef == "" {
		t.Fatalf("ResponseHeadersRef is empty: %+v", repo.traces[0])
	}
	if repo.traces[0].ResponseRawRef != "" {
		t.Fatalf("ResponseRawRef = %q, want empty", repo.traces[0].ResponseRawRef)
	}
	var foundResponseHeaders bool
	for _, object := range repo.rawEvidence {
		if object.ObjectType == "response_headers" {
			foundResponseHeaders = true
		}
	}
	if !foundResponseHeaders {
		t.Fatalf("raw evidence objects = %+v, missing response_headers", repo.rawEvidence)
	}
	if len(publisher.jobs) != 0 {
		t.Fatalf("published jobs = %d, want 0", len(publisher.jobs))
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
	waitForObservedHeader(t, rec, streamBody, done)
	waitForObservedFlushes(t, rec, 2, streamBody, done)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
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

func TestProxyStreamingAuditTimeoutStartsAfterStreamEnds(t *testing.T) {
	streamBody := newControlledReadCloser()
	repo := &contextRejectingTraceRepo{}
	publisher := &recordingJobPublisher{}
	handler := testHandler("https://upstream.test", repo, &selectiveEvidenceStore{})
	handler.AuditTimeout = time.Millisecond
	handler.JobPublisher = publisher
	handler.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
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
	waitForObservedFlushes(t, rec, 2, streamBody, done)
	time.Sleep(10 * time.Millisecond)
	streamBody.close()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("streaming response did not finish")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace after long stream, got %d", len(repo.traces))
	}
	if repo.traces[0].ResponseRawRef == "" {
		t.Fatal("ResponseRawRef is empty")
	}
	if len(publisher.jobs) != 1 {
		t.Fatalf("published jobs = %d, want 1", len(publisher.jobs))
	}
}

func TestProxyStreamingResponseEvidenceFailureDoesNotMaskClientResponse(t *testing.T) {
	t.Run("store failure suppresses downstream work", func(t *testing.T) {
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
		assertStreamingEvidenceFailureDoesNotMaskClientResponse(t, store, storeErr, "")
	})

	t.Run("capture failure suppresses downstream work", func(t *testing.T) {
		store := &selectiveEvidenceStore{
			objects: map[string]evidence.Object{
				"request_body": {
					ObjectRef:      "request.bin",
					StorageBackend: "test",
					SizeBytes:      2,
					SHA256:         "request-sha",
					CreatedAt:      time.Unix(1000, 0).UTC(),
				},
				"response_body": {
					ObjectRef:      "response.bin",
					StorageBackend: "test",
					SizeBytes:      int64(len("data: one\n\n")),
					SHA256:         "response-sha",
					CreatedAt:      time.Unix(1000, 0).UTC(),
				},
			},
		}
		assertStreamingEvidenceFailureDoesNotMaskClientResponse(t, store, io.ErrClosedPipe, "response.bin")
	})
}

func assertStreamingEvidenceFailureDoesNotMaskClientResponse(t *testing.T, store evidence.Store, wantAuditErr error, wantResponseRawRef string) {
	t.Helper()
	repo := &memoryTraceRepo{}
	publisher := &recordingJobPublisher{}
	emitter := &recordingCoverageEmitter{}
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
	handler.JobPublisher = publisher
	handler.CoverageEmitter = emitter
	handler.AuditError = func(ctx context.Context, err error) {
		auditErrors = append(auditErrors, err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{}`))
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
		t.Fatal("streaming response blocked after evidence failure")
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
	if repo.traces[0].ResponseRawRef != wantResponseRawRef {
		t.Fatalf("ResponseRawRef = %q, want %q", repo.traces[0].ResponseRawRef, wantResponseRawRef)
	}
	if len(auditErrors) == 0 || !errors.Is(auditErrors[0], wantAuditErr) {
		t.Fatalf("audit errors = %v, want %v", auditErrors, wantAuditErr)
	}
	if len(publisher.jobs) != 0 {
		t.Fatalf("published jobs = %d, want 0", len(publisher.jobs))
	}
	if len(emitter.alerts) != 0 {
		t.Fatalf("coverage alerts = %d, want 0", len(emitter.alerts))
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

type contextRejectingEvidenceStore struct{}

func (s *contextRejectingEvidenceStore) Put(ctx context.Context, req evidence.PutRequest) (evidence.Object, error) {
	if err := ctx.Err(); err != nil {
		return evidence.Object{}, err
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

func (s *contextRejectingEvidenceStore) Get(ctx context.Context, objectRef string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

type stallingTCPServer struct {
	URL      string
	listener net.Listener
	done     chan struct{}
}

func newStallingTCPServer(t *testing.T) (*stallingTCPServer, <-chan net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &stallingTCPServer{
		URL:      "http://" + listener.Addr().String(),
		listener: listener,
		done:     make(chan struct{}),
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		defer close(server.done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()
	return server, accepted
}

func (s *stallingTCPServer) Close() {
	_ = s.listener.Close()
	<-s.done
}

type cancelOnEOFReadCloser struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelOnEOFReadCloser) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}

func (r cancelOnEOFReadCloser) Close() error {
	return nil
}

type notifyingTraceRepo struct {
	traces chan traces.Trace
}

func (r *notifyingTraceRepo) InsertTrace(ctx context.Context, trace traces.Trace) error {
	r.traces <- trace
	return nil
}

func (r *notifyingTraceRepo) InsertRawEvidence(ctx context.Context, object traces.RawEvidenceObject) error {
	return nil
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
	flushes       chan struct{}
	headerOnce    sync.Once
}

func newObservedRecorder() *observedRecorder {
	return &observedRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		headerWritten:    make(chan struct{}),
		flushes:          make(chan struct{}, 16),
	}
}

func (r *observedRecorder) WriteHeader(code int) {
	r.ResponseRecorder.WriteHeader(code)
	r.headerOnce.Do(func() { close(r.headerWritten) })
}

func (r *observedRecorder) Write(p []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(p)
	r.headerOnce.Do(func() { close(r.headerWritten) })
	return n, err
}

func (r *observedRecorder) Flush() {
	r.ResponseRecorder.Flush()
	select {
	case r.flushes <- struct{}{}:
	default:
	}
}

func waitForObservedHeader(t *testing.T, rec *observedRecorder, streamBody *controlledReadCloser, done <-chan struct{}) {
	t.Helper()
	select {
	case <-rec.headerWritten:
	case <-time.After(250 * time.Millisecond):
		streamBody.close()
		<-done
		t.Fatal("streaming response was not written before upstream EOF")
	}
}

func waitForObservedFlushes(t *testing.T, rec *observedRecorder, want int, streamBody *controlledReadCloser, done <-chan struct{}) {
	t.Helper()
	for i := 0; i < want; i++ {
		select {
		case <-rec.flushes:
		case <-time.After(250 * time.Millisecond):
			streamBody.close()
			<-done
			t.Fatal("streaming response was not flushed before upstream EOF")
		}
	}
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
