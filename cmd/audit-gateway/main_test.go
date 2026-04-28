package main

import (
	"context"
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
		ListenAddr:         "127.0.0.1:8080",
		NewAPIBaseURL:      "https://new-api.example.test/base",
		AuditHMACSecret:    "0123456789abcdef0123456789abcdef",
		EvidenceStorageDir: t.TempDir(),
		EmployeeNoPattern:  regexp.MustCompile(`^E[0-9]+$`),
	}

	handler := buildHandler(cfg, nil, nil, log.New(ioDiscard{}, "", 0))

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
		ListenAddr:         "127.0.0.1:8080",
		NewAPIBaseURL:      "https://new-api.example.test/base",
		AuditHMACSecret:    "0123456789abcdef0123456789abcdef",
		AdminSessionSecret: "admin-session-secret-0123456789abcdef",
		AdminCookieName:    "audit_admin_session",
		EvidenceStorageDir: t.TempDir(),
		EmployeeNoPattern:  regexp.MustCompile(`^E[0-9]+$`),
	}

	handler := buildHTTPHandler(cfg, nil, nil, log.New(ioDiscard{}, "", 0))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusBadGateway {
		t.Fatal("admin route fell through to proxy")
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
