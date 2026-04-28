package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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
	err := r.db.QueryRow(ctx, `
SELECT employee_no, new_api_token_id, token_name_raw, token_status
FROM token_identity_cache
WHERE token_fingerprint = $1
LIMIT 1`, tokenFingerprint).Scan(&summary.EmployeeNo, &summary.NewAPITokenID, &summary.TokenName, &summary.TokenStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LookupSummary{}, err
	}
	traces, err := r.ListTraces(ctx, TraceFilter{TokenFingerprint: tokenFingerprint, Limit: 20})
	if err != nil {
		return LookupSummary{}, err
	}
	summary.RecentTraces = traces
	if err := r.db.QueryRow(ctx, `
SELECT count(*)
FROM usage_anomalies
WHERE token_fingerprint = $1 AND status = 'open'`, tokenFingerprint).Scan(&summary.OpenAnomalyCount); err != nil {
		return LookupSummary{}, err
	}
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

func (r Repository) OverviewSummary(ctx context.Context, now time.Time) (OverviewSummary, error) {
	if r.db == nil {
		return OverviewSummary{}, ErrAdminDBRequired
	}
	since := now.Add(-24 * time.Hour)
	var summary OverviewSummary
	err := r.db.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE created_at >= $1),
  count(*) FILTER (WHERE created_at >= $1 AND status_code >= 200 AND status_code < 400),
  count(*) FILTER (WHERE created_at >= $1 AND status_code >= 400),
  coalesce(sum(usage_total_tokens) FILTER (WHERE created_at >= $1), 0),
  (SELECT count(*) FROM usage_anomalies WHERE status = 'open'),
  (SELECT count(*) FROM coverage_alerts WHERE status = 'open'),
  count(*) FILTER (WHERE created_at >= $1 AND capture_mode = 'raw_only')
FROM traces`, since).Scan(
		&summary.RequestCount24h,
		&summary.SuccessCount24h,
		&summary.ErrorCount24h,
		&summary.TotalTokens24h,
		&summary.OpenAnomalies,
		&summary.OpenCoverageAlerts,
		&summary.RawOnlyTraceCount24h,
	)
	return summary, err
}

func (r Repository) ListUsageAggregates(ctx context.Context, filter UsageFilter) ([]UsageBucket, error) {
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
	if filter.EmployeeNo != "" {
		add("employee_no = $%d", filter.EmployeeNo)
	}
	if filter.TokenFingerprint != "" {
		add("token_fingerprint = $%d", filter.TokenFingerprint)
	}
	if filter.Model != "" {
		add("model = $%d", filter.Model)
	}
	if filter.RoutePattern != "" {
		add("route_pattern = $%d", filter.RoutePattern)
	}
	if filter.BucketSize != "" {
		add("bucket_size = $%d", filter.BucketSize)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`
SELECT bucket_start::text, bucket_size, employee_no, token_name_snapshot, model, route_pattern,
       request_count, success_count, error_count, total_tokens, estimated_cost
FROM usage_aggregates
WHERE %s
ORDER BY bucket_start DESC
LIMIT $%d`, strings.Join(where, " AND "), len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []UsageBucket{}
	for rows.Next() {
		var item UsageBucket
		if err := rows.Scan(
			&item.BucketStart,
			&item.BucketSize,
			&item.EmployeeNo,
			&item.FingerprintDisplay,
			&item.Model,
			&item.RoutePattern,
			&item.RequestCount,
			&item.SuccessCount,
			&item.ErrorCount,
			&item.TotalTokens,
			&item.EstimatedCost,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) InsertContextCatalogEntry(ctx context.Context, entry ContextCatalogEntry) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	_, err := r.db.Exec(ctx, `
INSERT INTO context_catalog (
  context_type, name, description, keywords, aliases, owner,
  expected_task_categories, expected_models, expected_usage_level, active,
  created_by, updated_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (context_type, name) DO UPDATE SET
  description = EXCLUDED.description,
  keywords = EXCLUDED.keywords,
  aliases = EXCLUDED.aliases,
  owner = EXCLUDED.owner,
  expected_task_categories = EXCLUDED.expected_task_categories,
  expected_models = EXCLUDED.expected_models,
  expected_usage_level = EXCLUDED.expected_usage_level,
  active = EXCLUDED.active,
  updated_by = EXCLUDED.updated_by,
  updated_at = now()`,
		entry.ContextType,
		entry.Name,
		entry.Description,
		entry.Keywords,
		entry.Aliases,
		entry.Owner,
		entry.ExpectedTaskCategories,
		entry.ExpectedModels,
		entry.ExpectedUsageLevel,
		entry.Active,
		entry.CreatedBy,
		entry.UpdatedBy,
	)
	return err
}

func (r Repository) GetTraceDetail(ctx context.Context, traceID string) (TraceDetail, error) {
	if r.db == nil {
		return TraceDetail{}, ErrAdminDBRequired
	}
	var detail TraceDetail
	err := r.db.QueryRow(ctx, `
SELECT trace_id, method, path, route_pattern, protocol_family, status_code,
       employee_no_snapshot, fingerprint_display, model_requested, usage_total_tokens,
       created_at::text, request_raw_ref, response_raw_ref, request_headers_ref,
       response_headers_ref, identity_resolution_status, analysis_status
FROM traces
WHERE trace_id = $1
LIMIT 1`, traceID).Scan(
		&detail.TraceID,
		&detail.Method,
		&detail.Path,
		&detail.RoutePattern,
		&detail.ProtocolFamily,
		&detail.StatusCode,
		&detail.EmployeeNo,
		&detail.FingerprintDisplay,
		&detail.ModelRequested,
		&detail.UsageTotalTokens,
		&detail.CreatedAt,
		&detail.RequestRawRef,
		&detail.ResponseRawRef,
		&detail.RequestHeadersRef,
		&detail.ResponseHeadersRef,
		&detail.IdentityResolutionStatus,
		&detail.AnalysisStatus,
	)
	if err != nil {
		return TraceDetail{}, err
	}
	messages, err := r.listNormalizedMessages(ctx, traceID)
	if err != nil {
		return TraceDetail{}, err
	}
	results, err := r.listAnalysisResults(ctx, traceID)
	if err != nil {
		return TraceDetail{}, err
	}
	detail.NormalizedMessages = messages
	detail.AnalysisResults = results
	return detail, nil
}

func (r Repository) listNormalizedMessages(ctx context.Context, traceID string) ([]NormalizedMessageSummary, error) {
	rows, err := r.db.Query(ctx, `
SELECT direction, sequence_index, role, modality, content_text, media_url,
       protocol_item_type, token_count_estimate
FROM normalized_messages
WHERE trace_id = $1
ORDER BY sequence_index ASC`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NormalizedMessageSummary{}
	for rows.Next() {
		var item NormalizedMessageSummary
		if err := rows.Scan(
			&item.Direction,
			&item.SequenceIndex,
			&item.Role,
			&item.Modality,
			&item.ContentText,
			&item.MediaURL,
			&item.ProtocolItemType,
			&item.TokenCountEstimate,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) listAnalysisResults(ctx context.Context, traceID string) ([]AnalysisResultSummary, error) {
	rows, err := r.db.Query(ctx, `
SELECT analyzer_name, category, label, score::text, confidence::text,
       severity, result_json::text, created_at::text
FROM analysis_results
WHERE trace_id = $1
ORDER BY created_at ASC`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AnalysisResultSummary{}
	for rows.Next() {
		var item AnalysisResultSummary
		if err := rows.Scan(
			&item.AnalyzerName,
			&item.Category,
			&item.Label,
			&item.Score,
			&item.Confidence,
			&item.Severity,
			&item.ResultJSON,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) ListContextCatalog(ctx context.Context, activeOnly bool, limit int) ([]ContextCatalogEntry, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	where := "1=1"
	args := []any{limit}
	if activeOnly {
		where = "active = true"
	}
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
SELECT id, context_type, name, description, keywords, aliases, owner,
       expected_task_categories, expected_models, expected_usage_level, active,
       created_by, updated_by, created_at::text, updated_at::text
FROM context_catalog
WHERE %s
ORDER BY context_type, name
LIMIT $1`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ContextCatalogEntry{}
	for rows.Next() {
		var item ContextCatalogEntry
		if err := rows.Scan(
			&item.ID,
			&item.ContextType,
			&item.Name,
			&item.Description,
			(*pgtype.FlatArray[string])(&item.Keywords),
			(*pgtype.FlatArray[string])(&item.Aliases),
			&item.Owner,
			(*pgtype.FlatArray[string])(&item.ExpectedTaskCategories),
			(*pgtype.FlatArray[string])(&item.ExpectedModels),
			&item.ExpectedUsageLevel,
			&item.Active,
			&item.CreatedBy,
			&item.UpdatedBy,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) ListAuditActionLogs(ctx context.Context, limit int) ([]AuditActionLogSummary, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.db.Query(ctx, `
SELECT actor_username, action, target_type, target_id, fingerprint_display,
       trace_id, metadata_json::text, created_at::text
FROM audit_action_logs
ORDER BY created_at DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AuditActionLogSummary{}
	for rows.Next() {
		var item AuditActionLogSummary
		if err := rows.Scan(
			&item.ActorUsername,
			&item.Action,
			&item.TargetType,
			&item.TargetID,
			&item.FingerprintDisplay,
			&item.TraceID,
			&item.MetadataJSON,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
