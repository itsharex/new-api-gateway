package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrAdminDBRequired = errors.New("admin repository database is nil")

type DB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Repository struct {
	db DB
}

func NewRepository(db DB) Repository {
	return Repository{db: db}
}

func (r Repository) FindActiveUserByUsername(ctx context.Context, username string) (User, error) {
	if r.db == nil {
		return User{}, ErrAdminDBRequired
	}
	var user User
	err := r.db.QueryRow(ctx, `
SELECT id, username, password_hash, display_name, email, role, status, created_at, updated_at
FROM audit_users
WHERE username = $1 AND status = 'active'
LIMIT 1`, username).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName, &user.Email,
		&user.Role, &user.Status, &user.CreatedAt, &user.UpdatedAt,
	)
	return user, err
}

func (r Repository) CreateSession(ctx context.Context, session Session) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	_, err := r.db.Exec(ctx, `
INSERT INTO audit_sessions (session_id, user_id, expires_at)
VALUES ($1,$2,$3)`, session.SessionID, session.UserID, session.ExpiresAt)
	return err
}

func (r Repository) PrincipalBySession(ctx context.Context, sessionID string, now time.Time) (Principal, error) {
	if r.db == nil {
		return Principal{}, ErrAdminDBRequired
	}
	var principal Principal
	err := r.db.QueryRow(ctx, `
SELECT u.id, u.username, u.display_name, u.role
FROM audit_sessions s
JOIN audit_users u ON u.id = s.user_id
WHERE s.session_id = $1
  AND s.revoked_at IS NULL
  AND s.expires_at > $2
  AND u.status = 'active'
LIMIT 1`, sessionID, now).Scan(
		&principal.UserID, &principal.Username, &principal.DisplayName, &principal.Role,
	)
	if err != nil {
		return Principal{}, err
	}
	_, _ = r.db.Exec(ctx, `UPDATE audit_sessions SET last_seen_at = $2 WHERE session_id = $1`, sessionID, now)
	return principal, nil
}

func (r Repository) RevokeSession(ctx context.Context, sessionID string, now time.Time) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	_, err := r.db.Exec(ctx, `UPDATE audit_sessions SET revoked_at = $2 WHERE session_id = $1`, sessionID, now)
	return err
}

func (r Repository) InsertAuditActionLog(ctx context.Context, log AuditActionLog) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(log.MetadataJSON) == "" {
		log.MetadataJSON = `{}`
	}
	_, err := r.db.Exec(ctx, `
INSERT INTO audit_action_logs (
  actor_user_id, actor_username, action, target_type, target_id,
  token_fingerprint, fingerprint_display, trace_id, ip_hash, user_agent_hash,
  metadata_json, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12)`,
		log.ActorUserID, log.ActorUsername, log.Action, log.TargetType, log.TargetID,
		log.TokenFingerprint, log.FingerprintDisplay, log.TraceID, log.IPHash, log.UserAgentHash,
		log.MetadataJSON, log.CreatedAt,
	)
	return err
}

func (r Repository) InsertReviewDecision(ctx context.Context, decision ReviewDecision) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	_, err := r.db.Exec(ctx, `
INSERT INTO review_decisions (
  target_type, target_id, decision, reviewer_id, reviewer_username, note, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		decision.TargetType, decision.TargetID, decision.Decision, decision.ReviewerID,
		decision.ReviewerUsername, decision.Note, decision.CreatedAt,
	)
	return err
}

func (r Repository) ListTraces(ctx context.Context, filter TraceFilter) ([]TraceSummary, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	where := []string{"1=1"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if filter.TraceID != "" {
		add("trace_id = $%d", filter.TraceID)
	}
	if filter.EmployeeNo != "" {
		add("employee_no_snapshot = $%d", filter.EmployeeNo)
	}
	if filter.TokenFingerprint != "" {
		add("token_fingerprint = $%d", filter.TokenFingerprint)
	}
	if filter.RoutePattern != "" {
		add("route_pattern = $%d", filter.RoutePattern)
	}
	if filter.Model != "" {
		add("model_requested = $%d", filter.Model)
	}
	if filter.StatusCode != 0 {
		add("status_code = $%d", filter.StatusCode)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`
SELECT trace_id, method, path, route_pattern, protocol_family, status_code,
       employee_no_snapshot, fingerprint_display, model_requested, usage_total_tokens,
       created_at::text
FROM traces
WHERE %s
ORDER BY created_at DESC
LIMIT $%d`, strings.Join(where, " AND "), len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var traces []TraceSummary
	for rows.Next() {
		var trace TraceSummary
		if err := rows.Scan(
			&trace.TraceID, &trace.Method, &trace.Path, &trace.RoutePattern, &trace.ProtocolFamily,
			&trace.StatusCode, &trace.EmployeeNo, &trace.FingerprintDisplay, &trace.ModelRequested,
			&trace.UsageTotalTokens, &trace.CreatedAt,
		); err != nil {
			return nil, err
		}
		traces = append(traces, trace)
	}
	return traces, rows.Err()
}

func (r Repository) ListAnomalies(ctx context.Context, limit int) ([]AnomalySummary, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.db.Query(ctx, `
SELECT anomaly_id, anomaly_type, severity, status, employee_no, fingerprint_display,
       observed_value::text, threshold_value::text, reason, created_at::text
FROM usage_anomalies
ORDER BY created_at DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []AnomalySummary
	for rows.Next() {
		var item AnomalySummary
		if err := rows.Scan(
			&item.AnomalyID, &item.AnomalyType, &item.Severity, &item.Status,
			&item.EmployeeNo, &item.FingerprintDisplay, &item.ObservedValue,
			&item.ThresholdValue, &item.Reason, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) ListCoverageAlerts(ctx context.Context, limit int) ([]CoverageAlertSummary, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.db.Query(ctx, `
SELECT alert_id, alert_code, severity, status, method, route_pattern, raw_path,
       protocol_family, occurrence_count, message, last_seen_at::text
FROM coverage_alerts
ORDER BY last_seen_at DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CoverageAlertSummary
	for rows.Next() {
		var item CoverageAlertSummary
		if err := rows.Scan(
			&item.AlertID, &item.AlertCode, &item.Severity, &item.Status,
			&item.Method, &item.RoutePattern, &item.RawPath, &item.ProtocolFamily,
			&item.OccurrenceCount, &item.Message, &item.LastSeenAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) LookupTokenSummary(ctx context.Context, tokenFingerprint, fingerprintDisplay string) (LookupSummary, error) {
	if r.db == nil {
		return LookupSummary{}, ErrAdminDBRequired
	}
	summary := LookupSummary{TokenFingerprint: tokenFingerprint, FingerprintDisplay: fingerprintDisplay}
	_ = r.db.QueryRow(ctx, `
SELECT employee_no, new_api_token_id, token_name_raw, token_status
FROM token_identity_cache
WHERE token_fingerprint = $1
LIMIT 1`, tokenFingerprint).Scan(&summary.EmployeeNo, &summary.NewAPITokenID, &summary.TokenName, &summary.TokenStatus)
	traces, err := r.ListTraces(ctx, TraceFilter{TokenFingerprint: tokenFingerprint, Limit: 20})
	if err != nil {
		return LookupSummary{}, err
	}
	summary.RecentTraces = traces
	_ = r.db.QueryRow(ctx, `
SELECT count(*)
FROM usage_anomalies
WHERE token_fingerprint = $1 AND status = 'open'`, tokenFingerprint).Scan(&summary.OpenAnomalyCount)
	return summary, nil
}

func (r Repository) FindRawEvidenceObject(ctx context.Context, traceID, objectType string) (EvidenceObjectSummary, error) {
	if r.db == nil {
		return EvidenceObjectSummary{}, ErrAdminDBRequired
	}
	var object EvidenceObjectSummary
	err := r.db.QueryRow(ctx, `
SELECT trace_id, object_type, object_ref, content_type, size_bytes, sha256
FROM raw_evidence_objects
WHERE trace_id = $1 AND object_type = $2
ORDER BY created_at DESC
LIMIT 1`, traceID, objectType).Scan(
		&object.TraceID, &object.ObjectType, &object.ObjectRef,
		&object.ContentType, &object.SizeBytes, &object.SHA256,
	)
	return object, err
}
