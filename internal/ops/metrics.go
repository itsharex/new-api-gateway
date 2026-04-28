package ops

import (
	"fmt"
	"sort"
	"strconv"
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
		if workers, ok := messageInt(check.Message, "workers"); ok {
			fmt.Fprintf(&builder, "audit_gateway_worker_count %d\n", workers)
		}
		if age, ok := messageDurationSeconds(check.Message, "age"); ok {
			fmt.Fprintf(&builder, "audit_gateway_worker_heartbeat_age_seconds %d\n", age)
		}
	}

	if check, ok := response.Checks["queue_lag"]; ok {
		queue, hasQueue := messageString(check.Message, "queue")
		depth, hasDepth := messageInt(check.Message, "depth")
		if hasQueue && hasDepth {
			fmt.Fprintf(&builder, "audit_gateway_analysis_queue_depth{queue=%q} %d\n", queue, depth)
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

func messageInt(message string, key string) (int64, bool) {
	value, ok := messageString(message, key)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func messageDurationSeconds(message string, key string) (int64, bool) {
	value, ok := messageString(message, key)
	if !ok {
		return 0, false
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, false
	}
	return int64(duration.Seconds()), true
}

func messageString(message string, key string) (string, bool) {
	prefix := key + "="
	for _, field := range strings.Fields(message) {
		if strings.HasPrefix(field, prefix) {
			return strings.TrimPrefix(field, prefix), true
		}
	}
	return "", false
}
