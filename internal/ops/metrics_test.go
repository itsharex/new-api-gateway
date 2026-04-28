package ops

import (
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
			"postgres":         {Status: "ok"},
			"worker_heartbeat": {Status: "ok", Message: "workers=2 age=1m0s"},
			"queue_lag":        {Status: "ok", Message: "queue=analysis_jobs depth=42"},
		},
	}

	body := RenderMetrics(response, startedAt, now)

	containsAll(t, body,
		"audit_gateway_up 1",
		"audit_gateway_uptime_seconds 120",
		`audit_gateway_dependency_up{dependency="postgres"} 1`,
		"audit_gateway_worker_heartbeat_age_seconds 60",
		"audit_gateway_worker_count 2",
		`audit_gateway_analysis_queue_depth{queue="analysis_jobs"} 42`,
	)
}

func TestMetricsEndpointCanBeDisabled(t *testing.T) {
	handler := Handler(Service{Now: time.Now}, false)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("/metrics status = %d, want 404", recorder.Code)
	}
}

func containsAll(t *testing.T, body string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(body, value) {
			t.Fatalf("body does not contain %q:\n%s", value, body)
		}
	}
}
