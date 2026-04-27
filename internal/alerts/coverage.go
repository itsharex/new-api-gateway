package alerts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type Emitter interface {
	EmitCoverageAlert(ctx context.Context, alert CoverageAlert) error
}

var ErrCoverageExecerRequired = errors.New("coverage alerts postgres execer is nil")

type CoverageAlert struct {
	AlertCode      string
	Severity       string
	Method         string
	RoutePattern   string
	RawPath        string
	ContentType    string
	ProtocolFamily string
	Message        string
	SampleTraceID  string
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
}

func KnownRawFirst(method, routePattern, rawPath, protocolFamily, traceID string) CoverageAlert {
	now := time.Now().UTC()
	return CoverageAlert{
		AlertCode:      "known_route_raw_first",
		Severity:       "medium",
		Method:         method,
		RoutePattern:   routePattern,
		RawPath:        rawPath,
		ProtocolFamily: protocolFamily,
		Message:        "route is captured with raw evidence and minimal metadata; deep normalizer is not enabled",
		SampleTraceID:  traceID,
		FirstSeenAt:    now,
		LastSeenAt:     now,
	}
}

func UnknownRoute(method, rawPath, contentType, traceID string) CoverageAlert {
	now := time.Now().UTC()
	return CoverageAlert{
		AlertCode:      "unknown_route",
		Severity:       "high",
		Method:         method,
		RoutePattern:   rawPath,
		RawPath:        rawPath,
		ContentType:    contentType,
		ProtocolFamily: "unknown",
		Message:        "route is not registered and is captured as raw evidence only",
		SampleTraceID:  traceID,
		FirstSeenAt:    now,
		LastSeenAt:     now,
	}
}

type postgresExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type PostgresRepository struct {
	execer postgresExecer
}

func NewPostgresRepository(execer postgresExecer) PostgresRepository {
	return PostgresRepository{execer: execer}
}

func (r PostgresRepository) EmitCoverageAlert(ctx context.Context, alert CoverageAlert) error {
	if r.execer == nil {
		return ErrCoverageExecerRequired
	}
	now := time.Now().UTC()
	if alert.FirstSeenAt.IsZero() {
		alert.FirstSeenAt = now
	}
	if alert.LastSeenAt.IsZero() {
		alert.LastSeenAt = now
	}
	alertID := coverageAlertID(alert)
	_, err := r.execer.Exec(ctx, `
INSERT INTO coverage_alerts (
  alert_id, alert_code, severity, method, route_pattern, raw_path, content_type,
  protocol_family, first_seen_at, last_seen_at, occurrence_count, sample_trace_ids,
  message, created_at, updated_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,
  $8,$9,$10,1,$11,
  $12,$13,$14
)
ON CONFLICT (alert_id) DO UPDATE SET
  last_seen_at = EXCLUDED.last_seen_at,
  occurrence_count = coverage_alerts.occurrence_count + 1,
  sample_trace_ids = CASE
    WHEN EXCLUDED.sample_trace_ids[1] = ANY(coverage_alerts.sample_trace_ids) THEN coverage_alerts.sample_trace_ids
    ELSE coverage_alerts.sample_trace_ids || EXCLUDED.sample_trace_ids
  END,
  message = EXCLUDED.message,
  updated_at = EXCLUDED.updated_at`,
		alertID, alert.AlertCode, alert.Severity, alert.Method, alert.RoutePattern, alert.RawPath, alert.ContentType,
		alert.ProtocolFamily, alert.FirstSeenAt, alert.LastSeenAt, []string{alert.SampleTraceID},
		alert.Message, now, now,
	)
	return err
}

func coverageAlertID(alert CoverageAlert) string {
	key := strings.Join([]string{
		alert.AlertCode,
		alert.Method,
		alert.RoutePattern,
		alert.RawPath,
		alert.ProtocolFamily,
	}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s:%s", alert.AlertCode, hex.EncodeToString(sum[:8]))
}
