package ops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServiceReadinessReportsHealthyDependencies(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	service := Service{
		Now:           nowFunc(now),
		PostgresCheck: func(context.Context) error { return nil },
		RedisCheck:    func(context.Context) error { return nil },
		EvidenceCheck: func(context.Context) error { return nil },
		WorkerHeartbeatCheck: func(context.Context) (WorkerHeartbeatStatus, error) {
			return WorkerHeartbeatStatus{
				LastSeenAt:  now.Add(-time.Minute),
				MaxAge:      5 * time.Minute,
				WorkerCount: 2,
			}, nil
		},
		QueueLagCheck: func(context.Context) (QueueLagStatus, error) {
			return QueueLagStatus{
				QueueName:     "analysis_jobs",
				Depth:         42,
				WarnThreshold: 1000,
			}, nil
		},
		RuntimeMetricsCheck: func(context.Context) (RuntimeMetrics, error) {
			return RuntimeMetrics{IdentityStatuses: map[string]int64{}}, nil
		},
	}

	response := service.Readiness(context.Background())

	if response.Status != "ok" {
		t.Fatalf("response.Status = %q, want ok", response.Status)
	}
	if response.Checks["postgres"].Status != "ok" {
		t.Fatalf("postgres status = %q, want ok", response.Checks["postgres"].Status)
	}
	if response.Checks["worker_heartbeat"].Status != "ok" {
		t.Fatalf("worker_heartbeat status = %q, want ok", response.Checks["worker_heartbeat"].Status)
	}
}

func TestServiceReadinessReportsDegradedQueueLag(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	service := healthyService(now)
	service.QueueLagCheck = func(context.Context) (QueueLagStatus, error) {
		return QueueLagStatus{
			QueueName:     "analysis_jobs",
			Depth:         1201,
			WarnThreshold: 1000,
		}, nil
	}

	response := service.Readiness(context.Background())

	if response.Status != "degraded" {
		t.Fatalf("response.Status = %q, want degraded", response.Status)
	}
	if response.Checks["queue_lag"].Status != "degraded" {
		t.Fatalf("queue_lag status = %q, want degraded", response.Checks["queue_lag"].Status)
	}
}

func TestServiceReadinessIncludesRuntimeMetrics(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	service := healthyService(now)
	service.RuntimeMetricsCheck = func(context.Context) (RuntimeMetrics, error) {
		return RuntimeMetrics{
			RequestCount:        10,
			CaptureFailureCount: 2,
			RawOnlyRouteCount:   3,
			IdentityStatuses:    map[string]int64{"resolved": 8},
			CoverageOpenCount:   4,
			AnomalyOpenCount:    5,
		}, nil
	}

	response := service.Readiness(context.Background())

	if response.Checks["runtime_metrics"].Status != "ok" {
		t.Fatalf("runtime_metrics status = %q, want ok", response.Checks["runtime_metrics"].Status)
	}
	if response.Metrics.RequestCount != 10 {
		t.Fatalf("RequestCount = %d, want 10", response.Metrics.RequestCount)
	}
	if response.Metrics.IdentityStatuses["resolved"] != 8 {
		t.Fatalf("resolved identity status count = %d, want 8", response.Metrics.IdentityStatuses["resolved"])
	}
}

func TestServiceReadinessReportsRuntimeMetricsFailure(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	service := healthyService(now)
	service.RuntimeMetricsCheck = func(context.Context) (RuntimeMetrics, error) {
		return RuntimeMetrics{}, errors.New("missing relation usage_anomalies")
	}

	response := service.Readiness(context.Background())

	if response.Status != "down" {
		t.Fatalf("response.Status = %q, want down", response.Status)
	}
	assertDownCheck(t, response, "runtime_metrics", "runtime metrics check failed")
	if response.Metrics.IdentityStatuses == nil {
		t.Fatal("IdentityStatuses is nil, want empty map")
	}
}

func TestServiceReadinessReportsStaleWorkerHeartbeat(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	service := healthyService(now)
	service.WorkerHeartbeatCheck = func(context.Context) (WorkerHeartbeatStatus, error) {
		return WorkerHeartbeatStatus{
			LastSeenAt:  now.Add(-10 * time.Minute),
			MaxAge:      5 * time.Minute,
			WorkerCount: 1,
		}, nil
	}

	response := service.Readiness(context.Background())

	if response.Status != "degraded" {
		t.Fatalf("response.Status = %q, want degraded", response.Status)
	}
	check := response.Checks["worker_heartbeat"]
	if check.Status != "degraded" {
		t.Fatalf("worker_heartbeat status = %q, want degraded", check.Status)
	}
	if !strings.Contains(check.Message, "stale") {
		t.Fatalf("worker_heartbeat message = %q, want stale", check.Message)
	}
}

func TestHandlerReturnsStatusCodes(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	service := healthyService(now)
	service.PostgresCheck = func(context.Context) error {
		return errors.New("postgres down")
	}
	handler := Handler(service, true)

	healthRecorder := httptest.NewRecorder()
	handler.ServeHTTP(healthRecorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if healthRecorder.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", healthRecorder.Code)
	}

	readyRecorder := httptest.NewRecorder()
	handler.ServeHTTP(readyRecorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readyRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz status = %d, want 503", readyRecorder.Code)
	}

	var response HealthResponse
	if err := json.NewDecoder(readyRecorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode ready response: %v", err)
	}
	if response.Checks["postgres"].Status != "down" {
		t.Fatalf("postgres status = %q, want down", response.Checks["postgres"].Status)
	}
}

func TestHandlerSanitizesReadinessErrors(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	service := healthyService(now)
	service.PostgresCheck = func(context.Context) error {
		return errors.New(`connect postgres://audit:Bearer sk-secret@db.internal:5432/audit failed`)
	}
	service.WorkerHeartbeatCheck = func(context.Context) (WorkerHeartbeatStatus, error) {
		return WorkerHeartbeatStatus{}, errors.New("query failed with token Bearer sk-worker-secret")
	}
	service.QueueLagCheck = func(context.Context) (QueueLagStatus, error) {
		return QueueLagStatus{}, errors.New("/var/private/queue/state contains Bearer sk-queue-secret")
	}
	handler := Handler(service, true)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if body := recorder.Body.String(); strings.Contains(body, "Bearer") || strings.Contains(body, "postgres://") || strings.Contains(body, "/var/private") {
		t.Fatalf("/readyz body leaked raw error details:\n%s", body)
	}

	var response HealthResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode ready response: %v", err)
	}
	assertDownCheck(t, response, "postgres", "postgres check failed")
	assertDownCheck(t, response, "worker_heartbeat", "worker heartbeat check failed")
	assertDownCheck(t, response, "queue_lag", "queue lag check failed")
}

func healthyService(now time.Time) Service {
	return Service{
		Now:           nowFunc(now),
		PostgresCheck: func(context.Context) error { return nil },
		RedisCheck:    func(context.Context) error { return nil },
		EvidenceCheck: func(context.Context) error { return nil },
		WorkerHeartbeatCheck: func(context.Context) (WorkerHeartbeatStatus, error) {
			return WorkerHeartbeatStatus{
				LastSeenAt:  now.Add(-time.Minute),
				MaxAge:      5 * time.Minute,
				WorkerCount: 2,
			}, nil
		},
		QueueLagCheck: func(context.Context) (QueueLagStatus, error) {
			return QueueLagStatus{
				QueueName:     "analysis_jobs",
				Depth:         42,
				WarnThreshold: 1000,
			}, nil
		},
		RuntimeMetricsCheck: func(context.Context) (RuntimeMetrics, error) {
			return RuntimeMetrics{IdentityStatuses: map[string]int64{}}, nil
		},
	}
}

func assertDownCheck(t *testing.T, response HealthResponse, name string, message string) {
	t.Helper()
	check := response.Checks[name]
	if check.Status != "down" {
		t.Fatalf("%s status = %q, want down", name, check.Status)
	}
	if check.Message != message {
		t.Fatalf("%s message = %q, want %q", name, check.Message, message)
	}
}

func nowFunc(now time.Time) func() time.Time {
	return func() time.Time {
		return now
	}
}
