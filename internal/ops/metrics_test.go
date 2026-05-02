package ops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRenderMetricsIncludesDependencyAndQueueValues(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 2, 0, 0, time.UTC)
	startedAt := now.Add(-120 * time.Second)
	response := HealthResponse{
		Status:    "ok",
		CheckedAt: now,
		Checks: map[string]CheckStatus{
			"postgres": {Status: "ok"},
			"worker_heartbeat": {
				Status:  "ok",
				Message: "workers=2 age=1m0s",
				Metrics: CheckMetrics{
					WorkerCount:           2,
					HasWorkerCount:        true,
					WorkerHeartbeatAge:    time.Minute,
					HasWorkerHeartbeatAge: true,
				},
			},
			"queue_lag": {
				Status:  "ok",
				Message: "queue=analysis_jobs depth=42",
				Metrics: CheckMetrics{
					QueueName:     "analysis_jobs",
					QueueDepth:    42,
					HasQueueDepth: true,
				},
			},
		},
	}

	body := RenderMetrics(response, startedAt, now)

	containsAll(t, body,
		"audit_gateway_up 1",
		"audit_gateway_uptime_seconds 120",
		`audit_gateway_dependency_up{dependency="postgres"} 1`,
		`audit_gateway_dependency_up{dependency="queue_lag"} 1`,
		`audit_gateway_dependency_up{dependency="worker_heartbeat"} 1`,
		"audit_gateway_worker_heartbeat_age_seconds 60",
		"audit_gateway_worker_count 2",
		`audit_gateway_analysis_queue_depth{queue="analysis_jobs"} 42`,
	)
}

func TestRenderMetricsIncludesAuditGatewayCounters(t *testing.T) {
	response := HealthResponse{
		Status:    statusOK,
		CheckedAt: time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC),
		Checks:    map[string]CheckStatus{},
		Metrics: RuntimeMetrics{
			RequestCount:        10,
			CaptureFailureCount: 2,
			RawOnlyRouteCount:   3,
			IdentityStatuses:    map[string]int64{"resolved": 8, "not_found": 2},
			CoverageOpenCount:   4,
			AnomalyOpenCount:    5,
		},
	}

	text := RenderMetrics(response, time.Date(2026, 4, 30, 7, 0, 0, 0, time.UTC), response.CheckedAt)

	for _, want := range []string{
		"audit_gateway_requests_total 10",
		"audit_gateway_capture_failures_total 2",
		`audit_gateway_identity_status_total{status="resolved"} 8`,
		"audit_gateway_raw_only_routes_total 3",
		"audit_gateway_coverage_open 4",
		"audit_gateway_anomaly_open 5",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics missing %q:\n%s", want, text)
		}
	}
}

func TestRenderMetricsIncludesUnknownIdentityStatusWhenNoStatusesLoaded(t *testing.T) {
	response := HealthResponse{
		Status:    statusOK,
		CheckedAt: time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC),
		Checks:    map[string]CheckStatus{},
		Metrics: RuntimeMetrics{
			IdentityStatuses: map[string]int64{},
		},
	}

	text := RenderMetrics(response, time.Date(2026, 4, 30, 7, 0, 0, 0, time.UTC), response.CheckedAt)

	containsAll(t, text, `audit_gateway_identity_status_total{status="unknown"} 0`)
}

func TestRenderMetricsUsesStructuredValuesWhenMessagesAreHumanReadable(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 2, 0, 0, time.UTC)
	response := HealthResponse{
		Status:    "degraded",
		CheckedAt: now,
		Checks: map[string]CheckStatus{
			"worker_heartbeat": {
				Status:  "degraded",
				Message: "no analysis worker heartbeat rows found",
				Metrics: CheckMetrics{
					WorkerCount:    0,
					HasWorkerCount: true,
				},
			},
			"queue_lag": {
				Status:  "degraded",
				Message: "analysis queue is above the configured warning threshold",
				Metrics: CheckMetrics{
					QueueName:     "analysis_jobs",
					QueueDepth:    1201,
					HasQueueDepth: true,
				},
			},
		},
	}

	body := RenderMetrics(response, now.Add(-time.Minute), now)

	containsAll(t, body,
		`audit_gateway_dependency_up{dependency="queue_lag"} 1`,
		`audit_gateway_dependency_up{dependency="worker_heartbeat"} 1`,
		"audit_gateway_worker_count 0",
		`audit_gateway_analysis_queue_depth{queue="analysis_jobs"} 1201`,
	)
	if strings.Contains(body, "audit_gateway_worker_heartbeat_age_seconds") {
		t.Fatalf("body contains heartbeat age without structured heartbeat age:\n%s", body)
	}
}

func TestRenderMetricsClampsInvalidUptime(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 2, 0, 0, time.UTC)
	response := HealthResponse{Status: "ok", CheckedAt: now, Checks: map[string]CheckStatus{}}

	containsAll(t, RenderMetrics(response, time.Time{}, now), "audit_gateway_uptime_seconds 0")
	containsAll(t, RenderMetrics(response, now.Add(time.Minute), now), "audit_gateway_uptime_seconds 0")
}

func TestMetricsEndpointCanBeDisabled(t *testing.T) {
	handler := Handler(Service{Now: time.Now}, false)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("/metrics status = %d, want 404", recorder.Code)
	}
}

func TestMetricsEndpointCanBeEnabled(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 2, 0, 0, time.UTC)
	service := healthyService(now)
	service.StartedAt = now.Add(-120 * time.Second)
	handler := Handler(service, true)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("content type = %q, want text/plain; version=0.0.4; charset=utf-8", contentType)
	}
	containsAll(t, recorder.Body.String(),
		"audit_gateway_up 1",
		"audit_gateway_uptime_seconds 120",
		`audit_gateway_analysis_queue_depth{queue="analysis_jobs"} 42`,
	)
}

func TestServiceReadinessIncludesStructuredMetricsForDegradedChecks(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	service := healthyService(now)
	service.WorkerHeartbeatCheck = func(context.Context) (WorkerHeartbeatStatus, error) {
		return WorkerHeartbeatStatus{WorkerCount: 0, MaxAge: 5 * time.Minute}, nil
	}
	service.QueueLagCheck = func(context.Context) (QueueLagStatus, error) {
		return QueueLagStatus{
			QueueName:     "analysis_jobs",
			Depth:         1201,
			WarnThreshold: 1000,
		}, nil
	}

	response := service.Readiness(context.Background())

	containsAll(t, RenderMetrics(response, now.Add(-time.Minute), now),
		"audit_gateway_worker_count 0",
		`audit_gateway_analysis_queue_depth{queue="analysis_jobs"} 1201`,
	)
}

func containsAll(t *testing.T, body string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(body, value) {
			t.Fatalf("body does not contain %q:\n%s", value, body)
		}
	}
}
