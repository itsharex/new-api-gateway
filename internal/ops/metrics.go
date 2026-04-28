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
	fmt.Fprintf(&builder, "audit_gateway_uptime_seconds %.0f\n", now.Sub(startedAt).Seconds())

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

	return builder.String()
}

func gatewayUp(status string) int {
	if status == statusDown {
		return 0
	}
	return 1
}

func checkUp(status string) int {
	if status == statusOK {
		return 1
	}
	return 0
}

func sortedDependencyKeys(checks map[string]CheckStatus) []string {
	keys := make([]string, 0, len(checks))
	for key := range checks {
		switch key {
		case "process", "worker_heartbeat", "queue_lag":
			continue
		default:
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
