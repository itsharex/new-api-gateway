package ops

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func RenderMetrics(response HealthResponse, startedAt time.Time, now time.Time) string {
	var builder strings.Builder

	fmt.Fprintf(&builder, "audit_gateway_up %d\n", gatewayUp(response.Status))
	fmt.Fprintf(&builder, "audit_gateway_uptime_seconds %.0f\n", uptimeSeconds(startedAt, now))

	for _, key := range sortedDependencyKeys(response.Checks) {
		fmt.Fprintf(&builder, "audit_gateway_dependency_up{dependency=%q} %d\n", key, checkUp(response.Checks[key].Status))
	}

	if check, ok := response.Checks["worker_heartbeat"]; ok {
		if check.Metrics.HasWorkerCount {
			fmt.Fprintf(&builder, "audit_gateway_worker_count %d\n", check.Metrics.WorkerCount)
		}
		if check.Metrics.HasWorkerHeartbeatAge {
			fmt.Fprintf(&builder, "audit_gateway_worker_heartbeat_age_seconds %.0f\n", check.Metrics.WorkerHeartbeatAge.Seconds())
		}
	}

	if check, ok := response.Checks["queue_lag"]; ok {
		if check.Metrics.HasQueueDepth {
			fmt.Fprintf(&builder, "audit_gateway_analysis_queue_depth{queue=%q} %d\n", check.Metrics.QueueName, check.Metrics.QueueDepth)
		}
	}

	fmt.Fprintf(&builder, "audit_gateway_requests_total %d\n", response.Metrics.RequestCount)
	fmt.Fprintf(&builder, "audit_gateway_capture_failures_total %d\n", response.Metrics.CaptureFailureCount)
	fmt.Fprintf(&builder, "audit_gateway_raw_only_routes_total %d\n", response.Metrics.RawOnlyRouteCount)
	fmt.Fprintf(&builder, "audit_gateway_coverage_open %d\n", response.Metrics.CoverageOpenCount)
	fmt.Fprintf(&builder, "audit_gateway_anomaly_open %d\n", response.Metrics.AnomalyOpenCount)
	identityStatuses := response.Metrics.IdentityStatuses
	if len(identityStatuses) == 0 {
		identityStatuses = map[string]int64{"unknown": 0}
	}
	for _, status := range sortedMetricKeys(identityStatuses) {
		fmt.Fprintf(&builder, "audit_gateway_identity_status_total{status=%q} %d\n", status, identityStatuses[status])
	}

	return builder.String()
}

func uptimeSeconds(startedAt time.Time, now time.Time) float64 {
	if startedAt.IsZero() || startedAt.After(now) {
		return 0
	}
	return now.Sub(startedAt).Seconds()
}

func gatewayUp(status string) int {
	if status == statusDown {
		return 0
	}
	return 1
}

func checkUp(status string) int {
	if status == statusDown {
		return 0
	}
	return 1
}

func sortedDependencyKeys(checks map[string]CheckStatus) []string {
	keys := make([]string, 0, len(checks))
	for key := range checks {
		if key == "process" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMetricKeys(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
