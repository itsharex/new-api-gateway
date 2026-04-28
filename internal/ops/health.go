package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	statusOK       = "ok"
	statusDegraded = "degraded"
	statusDown     = "down"
)

type CheckStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type HealthResponse struct {
	Status    string                 `json:"status"`
	CheckedAt time.Time              `json:"checked_at"`
	Checks    map[string]CheckStatus `json:"checks"`
}

type WorkerHeartbeatStatus struct {
	LastSeenAt  time.Time
	MaxAge      time.Duration
	WorkerCount int64
}

type QueueLagStatus struct {
	QueueName     string
	Depth         int64
	WarnThreshold int64
}

type Service struct {
	StartedAt time.Time
	Now       func() time.Time

	PostgresCheck func(context.Context) error
	RedisCheck    func(context.Context) error
	EvidenceCheck func(context.Context) error

	WorkerHeartbeatCheck func(context.Context) (WorkerHeartbeatStatus, error)
	QueueLagCheck        func(context.Context) (QueueLagStatus, error)
}

func (s Service) Liveness() HealthResponse {
	return HealthResponse{
		Status:    statusOK,
		CheckedAt: s.now().UTC(),
		Checks: map[string]CheckStatus{
			"process": {Status: statusOK},
		},
	}
}

func (s Service) Readiness(ctx context.Context) HealthResponse {
	checks := map[string]CheckStatus{
		"postgres":         s.simpleCheck(ctx, s.PostgresCheck),
		"redis":            s.simpleCheck(ctx, s.RedisCheck),
		"evidence":         s.simpleCheck(ctx, s.EvidenceCheck),
		"worker_heartbeat": s.workerHeartbeatCheck(ctx),
		"queue_lag":        s.queueLagCheck(ctx),
	}

	return HealthResponse{
		Status:    overallStatus(checks),
		CheckedAt: s.now().UTC(),
		Checks:    checks,
	}
}

func Handler(service Service, metricsEnabled bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, service.Liveness())
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		response := service.Readiness(r.Context())
		statusCode := http.StatusOK
		if response.Status == statusDown {
			statusCode = http.StatusServiceUnavailable
		}
		writeJSON(w, statusCode, response)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if !metricsEnabled {
			http.NotFound(w, r)
			return
		}
		now := service.now().UTC()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(RenderMetrics(service.Readiness(r.Context()), service.StartedAt, now)))
	})
	return mux
}

func (s Service) simpleCheck(ctx context.Context, check func(context.Context) error) CheckStatus {
	if check == nil {
		return CheckStatus{Status: statusDegraded, Message: "check is not configured"}
	}
	if err := check(ctx); err != nil {
		return CheckStatus{Status: statusDown, Message: err.Error()}
	}
	return CheckStatus{Status: statusOK}
}

func (s Service) workerHeartbeatCheck(ctx context.Context) CheckStatus {
	if s.WorkerHeartbeatCheck == nil {
		return CheckStatus{Status: statusDegraded, Message: "check is not configured"}
	}
	heartbeat, err := s.WorkerHeartbeatCheck(ctx)
	if err != nil {
		return CheckStatus{Status: statusDown, Message: err.Error()}
	}
	if heartbeat.WorkerCount == 0 {
		return CheckStatus{Status: statusDegraded, Message: "no analysis worker heartbeat rows found"}
	}

	age := s.now().UTC().Sub(heartbeat.LastSeenAt.UTC())
	if age < 0 {
		age = 0
	}
	if heartbeat.MaxAge > 0 && age > heartbeat.MaxAge {
		return CheckStatus{Status: statusDegraded, Message: fmt.Sprintf("analysis worker heartbeat is stale workers=%d age=%s max_age=%s", heartbeat.WorkerCount, age, heartbeat.MaxAge)}
	}
	return CheckStatus{Status: statusOK, Message: fmt.Sprintf("workers=%d age=%s", heartbeat.WorkerCount, age)}
}

func (s Service) queueLagCheck(ctx context.Context) CheckStatus {
	if s.QueueLagCheck == nil {
		return CheckStatus{Status: statusDegraded, Message: "check is not configured"}
	}
	queue, err := s.QueueLagCheck(ctx)
	if err != nil {
		return CheckStatus{Status: statusDown, Message: err.Error()}
	}
	if queue.Depth > queue.WarnThreshold {
		return CheckStatus{Status: statusDegraded, Message: fmt.Sprintf("queue=%s depth=%d threshold=%d", queue.QueueName, queue.Depth, queue.WarnThreshold)}
	}
	return CheckStatus{Status: statusOK, Message: fmt.Sprintf("queue=%s depth=%d", queue.QueueName, queue.Depth)}
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func overallStatus(checks map[string]CheckStatus) string {
	overall := statusOK
	for _, check := range checks {
		switch check.Status {
		case statusDown:
			return statusDown
		case statusDegraded:
			overall = statusDegraded
		}
	}
	return overall
}

func writeJSON(w http.ResponseWriter, statusCode int, response HealthResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(response)
}
