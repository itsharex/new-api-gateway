package gateway

import (
	"bytes"
	"context"
	"errors"
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
