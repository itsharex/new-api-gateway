package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/your-company/new-api-gateway/internal/config"
)

func TestBuildHandlerWiresGatewayRuntimeDependencies(t *testing.T) {
	cfg := config.Config{
		ListenAddr:              "127.0.0.1:8080",
		NewAPIBaseURL:           "https://new-api.example.test/base",
		AuditHMACSecret:         "0123456789abcdef0123456789abcdef",
		EvidenceStorageBackend:  "filesystem",
		EvidenceStorageDir:      t.TempDir(),
	}

	handler := buildHandler(cfg, nil, nil, nil, log.New(ioDiscard{}, "", 0))

	if handler.UpstreamBaseURL != cfg.NewAPIBaseURL {
		t.Fatalf("UpstreamBaseURL = %q", handler.UpstreamBaseURL)
	}
	if handler.AuditSecret != cfg.AuditHMACSecret {
		t.Fatalf("AuditSecret was not wired from config")
	}
	if handler.EvidenceStore == nil {
		t.Fatal("EvidenceStore is nil")
	}
	if handler.TraceRepo == nil {
		t.Fatal("TraceRepo is nil")
	}
	if handler.IdentityResolver == nil {
		t.Fatal("IdentityResolver is nil")
	}
	if handler.JobPublisher == nil {
		t.Fatal("JobPublisher is nil")
	}
	if handler.CoverageEmitter == nil {
		t.Fatal("CoverageEmitter is nil")
	}
	if handler.AuditError == nil {
		t.Fatal("AuditError is nil")
	}
}

func TestBuildHTTPHandlerRoutesAdminBeforeProxy(t *testing.T) {
	cfg := config.Config{
		ListenAddr:              "127.0.0.1:8080",
		NewAPIBaseURL:           "https://new-api.example.test/base",
		AuditHMACSecret:         "0123456789abcdef0123456789abcdef",
		AdminSessionSecret:      "admin-session-secret-0123456789abcdef",
		AdminCookieName:         "audit_admin_session",
		EvidenceStorageBackend:  "filesystem",
		EvidenceStorageDir:      t.TempDir(),
	}

	handler := buildHTTPHandler(cfg, nil, nil, nil, log.New(ioDiscard{}, "", 0))
	for _, path := range []string{"/admin/api", "/admin/api/login"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusBadGateway {
				t.Fatal("admin route fell through to proxy")
			}
		})
	}
}

func TestBuildHTTPHandlerServesOperationalRoutesBeforeAdminAndProxy(t *testing.T) {
	cfg := config.Config{
		NewAPIBaseURL:            "https://new-api.example.test/base",
		AuditHMACSecret:          "0123456789abcdef0123456789abcdef",
		EvidenceStorageBackend:   "filesystem",
		EvidenceStorageDir:       t.TempDir(),
		OpsCheckTimeout:          50 * time.Millisecond,
		OpsWorkerHeartbeatMaxAge: 5 * time.Minute,
		OpsQueueLagWarnThreshold: 1000,
		OpsMetricsEnabled:        true,
	}
	handler := buildHTTPHandler(cfg, nil, nil, nil, log.New(io.Discard, "", 0))

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusBadGateway {
				t.Fatalf("%s fell through to proxy", path)
			}
			if rec.Code == http.StatusNotFound {
				t.Fatalf("%s was not mounted", path)
			}
		})
	}
}

func TestBuildHTTPHandlerServesAdminUIWithoutInterceptingAPIOrProxy(t *testing.T) {
	handler := buildHTTPHandler(config.Config{EvidenceStorageBackend: "filesystem", EvidenceStorageDir: t.TempDir()}, nil, nil, nil, log.New(io.Discard, "", 0))

	adminReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminRec := httptest.NewRecorder()
	handler.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("/admin status = %d, body = %s", adminRec.Code, adminRec.Body.String())
	}
	if !strings.Contains(adminRec.Body.String(), `id="app"`) {
		t.Fatalf("/admin did not return app shell: %s", adminRec.Body.String())
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/admin/api/me status = %d, want 503 when database unavailable", apiRec.Code)
	}
}

func TestAuditErrorLoggerRedactsBearerTokens(t *testing.T) {
	var logged string
	logger := log.New(logSink{write: func(p []byte) { logged += string(p) }}, "", 0)
	auditError := auditErrorLogger(logger)

	auditError(context.Background(), errString("upstream failed for Bearer sk-secret-plain-text"))

	if logged == "" {
		t.Fatal("expected audit error to be logged")
	}
	if contains(logged, "sk-secret-plain-text") {
		t.Fatalf("audit log leaked plaintext API key: %q", logged)
	}
}

func TestServeUntilContextShutsDownServerWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &http.Server{
		Addr:    "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	}
	shutdownCalled := make(chan struct{})
	server.RegisterOnShutdown(func() {
		close(shutdownCalled)
	})

	done := make(chan error, 1)
	go func() {
		done <- serveUntilContext(ctx, server, 250*time.Millisecond)
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case <-shutdownCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server shutdown was not called after context cancellation")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveUntilContext returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("serveUntilContext did not return after context cancellation")
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}

type logSink struct {
	write func([]byte)
}

func (s logSink) Write(p []byte) (int, error) {
	s.write(p)
	return len(p), nil
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func contains(value, needle string) bool {
	return regexp.MustCompile(regexp.QuoteMeta(needle)).MatchString(value)
}
