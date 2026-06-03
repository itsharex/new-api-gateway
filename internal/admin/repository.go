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

func (r Repository) FindActiveUserByID(ctx context.Context, userID int64) (User, error) {
	if r.db == nil {
		return User{}, ErrAdminDBRequired
	}
	var user User
	err := r.db.QueryRow(ctx, `
SELECT id, username, password_hash, display_name, email, role, status, created_at, updated_at
FROM audit_users
WHERE id = $1 AND status = 'active'
LIMIT 1`, userID).Scan(
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
INSERT INTO audit_sessions (session_id, user_id, expires_at, csrf_token)
VALUES ($1,$2,$3,$4)`, session.SessionID, session.UserID, session.ExpiresAt, session.CSRFToken)
	return err
}

func (r Repository) PrincipalBySession(ctx context.Context, sessionID string, now time.Time) (Principal, error) {
	if r.db == nil {
		return Principal{}, ErrAdminDBRequired
	}
	var principal Principal
	err := r.db.QueryRow(ctx, `
	SELECT u.id, u.username, u.display_name, u.role, s.csrf_token
	FROM audit_sessions s
	JOIN audit_users u ON u.id = s.user_id
	WHERE s.session_id = $1
  AND s.revoked_at IS NULL
  AND s.expires_at > $2
  AND u.status = 'active'
	LIMIT 1`, sessionID, now).Scan(
		&principal.UserID, &principal.Username, &principal.DisplayName, &principal.Role, &principal.CSRFToken,
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

func (r Repository) UpdateUserPassword(ctx context.Context, userID int64, passwordHash string, now time.Time) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	_, err := r.db.Exec(ctx, `
UPDATE audit_users
SET password_hash = $2, updated_at = $3
WHERE id = $1`, userID, passwordHash, now)
	return err
}

func (r Repository) RevokeOtherSessions(ctx context.Context, userID int64, keepSessionID string, now time.Time) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	_, err := r.db.Exec(ctx, `
UPDATE audit_sessions
SET revoked_at = $3
WHERE user_id = $1
  AND session_id <> $2
  AND revoked_at IS NULL
  AND expires_at > $3`, userID, keepSessionID, now)
	return err
}

func (r Repository) ChangeUserPassword(ctx context.Context, userID int64, passwordHash string, keepSessionID string, log AuditActionLog, now time.Time) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = now
	}
	if strings.TrimSpace(log.MetadataJSON) == "" {
		log.MetadataJSON = `{}`
	}
	_, err := r.db.Exec(ctx, `
WITH updated_user AS (
  UPDATE audit_users
  SET password_hash = $2, updated_at = $3
  WHERE id = $1
  RETURNING id
), revoked_sessions AS (
  UPDATE audit_sessions
  SET revoked_at = $3
  WHERE user_id = $1
    AND session_id <> $4
    AND revoked_at IS NULL
    AND expires_at > $3
  RETURNING id
)
INSERT INTO audit_action_logs (
  actor_user_id, actor_username, action, target_type, target_id,
  token_fingerprint, fingerprint_display, trace_id, ip_hash, user_agent_hash,
  metadata_json, created_at
)
SELECT $5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::jsonb,$16
FROM updated_user`, userID, passwordHash, now, keepSessionID,
		log.ActorUserID, log.ActorUsername, log.Action, log.TargetType, log.TargetID,
		log.TokenFingerprint, log.FingerprintDisplay, log.TraceID, log.IPHash, log.UserAgentHash,
		log.MetadataJSON, log.CreatedAt,
	)
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

func (r Repository) ListTraces(ctx context.Context, filter TraceFilter) (TraceListResult, error) {
	if r.db == nil {
		return TraceListResult{}, ErrAdminDBRequired
	}
	page := filter.Page
	if page < 1 {
		page = 1
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
		add("t.trace_id = $%d", filter.TraceID)
	}
	if filter.Username != "" {
		add("t.username_snapshot = $%d", filter.Username)
	}
	if filter.TokenFingerprint != "" {
		add("t.token_fingerprint = $%d", filter.TokenFingerprint)
	}
	if filter.RoutePattern != "" {
		add("t.route_pattern = $%d", filter.RoutePattern)
	}
	if filter.Model != "" {
		add("t.model_requested = $%d", filter.Model)
	}
	if filter.StatusCode != 0 {
		add("t.status_code = $%d", filter.StatusCode)
	}

	var totalItems int64
	countQuery := fmt.Sprintf(`SELECT count(*) FROM traces t WHERE %s`, strings.Join(where, " AND "))
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&totalItems); err != nil {
		return TraceListResult{}, err
	}

	totalPages := 0
	if totalItems > 0 {
		totalPages = int((totalItems + int64(limit) - 1) / int64(limit))
		if page > totalPages {
			page = totalPages
		}
	}

	offset := 0
	if totalPages > 0 {
		offset = (page - 1) * limit
	}

	listArgs := append(append([]any(nil), args...), limit, offset)
	query := fmt.Sprintf(`
SELECT t.trace_id, t.method, t.path, t.route_pattern, t.protocol_family, t.status_code,
       t.username_snapshot, t.fingerprint_display, t.model_requested,
       t.usage_prompt_tokens, t.usage_completion_tokens, t.usage_cached_tokens, t.usage_total_tokens,
       t.created_at::text,
       EXISTS(SELECT 1 FROM analysis_results WHERE trace_id = t.trace_id AND severity = 'review') AS needs_review
FROM traces t
WHERE %s
ORDER BY t.created_at DESC, t.trace_id DESC
LIMIT $%d OFFSET $%d`, strings.Join(where, " AND "), len(args)+1, len(args)+2)
	rows, err := r.db.Query(ctx, query, listArgs...)
	if err != nil {
		return TraceListResult{}, err
	}
	defer rows.Close()
	var traces []TraceSummary
	for rows.Next() {
		var trace TraceSummary
		if err := rows.Scan(
			&trace.TraceID, &trace.Method, &trace.Path, &trace.RoutePattern, &trace.ProtocolFamily,
			&trace.StatusCode, &trace.Username, &trace.FingerprintDisplay, &trace.ModelRequested,
			&trace.UsagePromptTokens, &trace.UsageCompletionTokens, &trace.UsageCachedTokens,
			&trace.UsageTotalTokens, &trace.CreatedAt, &trace.NeedsReview,
		); err != nil {
			return TraceListResult{}, err
		}
		traces = append(traces, trace)
	}
	if err := rows.Err(); err != nil {
		return TraceListResult{}, err
	}

	return TraceListResult{
		Traces: traces,
		Pagination: TracePagination{
			Page:       page,
			PageSize:   limit,
			TotalItems: totalItems,
			TotalPages: totalPages,
			HasPrev:    totalPages > 0 && page > 1,
			HasNext:    totalPages > 0 && page < totalPages,
		},
	}, nil
}

func (r Repository) ListAnomalies(ctx context.Context, limit int) ([]AnomalySummary, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.db.Query(ctx, `
SELECT anomaly_id, anomaly_type, severity, status, username, fingerprint_display,
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
			&item.Username, &item.FingerprintDisplay, &item.ObservedValue,
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
SELECT username, new_api_token_id, token_name_raw, token_status
FROM token_identity_cache
WHERE token_fingerprint = $1
LIMIT 1`, tokenFingerprint).Scan(&summary.Username, &summary.NewAPITokenID, &summary.TokenName, &summary.TokenStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LookupSummary{}, err
	}
	traceResult, err := r.ListTraces(ctx, TraceFilter{TokenFingerprint: tokenFingerprint, Page: 1, Limit: 20})
	if err != nil {
		return LookupSummary{}, err
	}
	summary.RecentTraces = traceResult.Traces
	if err := r.db.QueryRow(ctx, `
SELECT count(*)
FROM usage_anomalies
WHERE token_fingerprint = $1 AND status = 'open'`, tokenFingerprint).Scan(&summary.OpenAnomalyCount); err != nil {
		return LookupSummary{}, err
	}
	return summary, nil
}

func (r Repository) FindRawEvidenceObject(ctx context.Context, traceID, objectType, objectRef string) (EvidenceObjectSummary, error) {
	if r.db == nil {
		return EvidenceObjectSummary{}, ErrAdminDBRequired
	}
	var object EvidenceObjectSummary
	if strings.TrimSpace(objectRef) != "" {
		err := r.db.QueryRow(ctx, `
SELECT trace_id, object_type, object_ref, content_type, size_bytes, sha256
FROM raw_evidence_objects
WHERE trace_id = $1 AND object_type = $2 AND object_ref = $3
ORDER BY created_at DESC
LIMIT 1`, traceID, objectType, objectRef).Scan(
			&object.TraceID, &object.ObjectType, &object.ObjectRef,
			&object.ContentType, &object.SizeBytes, &object.SHA256,
		)
		return object, err
	}
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
	if err != nil {
		return summary, err
	}
	summary.TokenUsageDaily, err = r.dailyTokenUsage(ctx, now, 30)
	return summary, err
}

func (r Repository) dailyTokenUsage(ctx context.Context, now time.Time, days int) ([]TokenUsageDay, error) {
	if days <= 0 {
		days = 30
	}
	endDay := now.UTC().Truncate(24 * time.Hour)
	startDay := endDay.AddDate(0, 0, -(days - 1))
	points := make([]TokenUsageDay, 0, days)
	byDate := make(map[string]int64, days)

	rows, err := r.db.Query(ctx, `
SELECT (bucket_start AT TIME ZONE 'UTC')::date::text, COALESCE(SUM(total_tokens), 0)
FROM usage_aggregates
WHERE bucket_size = 'day'
  AND bucket_start >= $1
  AND bucket_start < $2
GROUP BY (bucket_start AT TIME ZONE 'UTC')::date
ORDER BY (bucket_start AT TIME ZONE 'UTC')::date`, startDay, endDay.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var date string
		var totalTokens int64
		if err := rows.Scan(&date, &totalTokens); err != nil {
			return nil, err
		}
		byDate[date] = totalTokens
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for day := startDay; !day.After(endDay); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		points = append(points, TokenUsageDay{Date: date, TotalTokens: byDate[date]})
	}
	return points, nil
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
	if filter.Username != "" {
		add("username = $%d", filter.Username)
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
SELECT bucket_start::text, bucket_size, username, token_name_snapshot, model, route_pattern,
       request_count, success_count, error_count,
       prompt_tokens, completion_tokens, cached_tokens, total_tokens, estimated_cost
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
			&item.Username,
			&item.FingerprintDisplay,
			&item.Model,
			&item.RoutePattern,
			&item.RequestCount,
			&item.SuccessCount,
			&item.ErrorCount,
			&item.PromptTokens,
			&item.CompletionTokens,
			&item.CachedTokens,
			&item.TotalTokens,
			&item.EstimatedCost,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) EmployeeUsageTrend(ctx context.Context, filter EmployeeUsageFilter) (EmployeeUsageTrend, error) {
	if r.db == nil {
		return EmployeeUsageTrend{}, ErrAdminDBRequired
	}
	selectedModel := strings.TrimSpace(filter.Model)
	trend := EmployeeUsageTrend{
		Username:      filter.Username,
		Range:         filter.Range,
		SelectedModel: selectedModel,
		Models:        []string{},
		Daily:         []UsageDailyPoint{},
		ModelSummary:  []UsageModelSummary{},
	}
	if strings.TrimSpace(filter.Username) == "" {
		return trend, nil
	}

	rows, err := r.db.Query(ctx, `
SELECT DISTINCT model
FROM usage_aggregates
WHERE bucket_size = 'day'
  AND username = $1
  AND bucket_start >= $2
  AND bucket_start < $3
  AND model <> ''
ORDER BY model`, filter.Username, filter.Start, filter.End)
	if err != nil {
		return trend, err
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return trend, err
		}
		trend.Models = append(trend.Models, model)
	}
	if err := rows.Err(); err != nil {
		return trend, err
	}

	dailyWhere := []string{
		"bucket_size = 'day'",
		"username = $1",
		"bucket_start >= $2",
		"bucket_start < $3",
	}
	dailyArgs := []any{filter.Username, filter.Start, filter.End}
	if selectedModel != "" {
		dailyArgs = append(dailyArgs, selectedModel)
		dailyWhere = append(dailyWhere, fmt.Sprintf("model = $%d", len(dailyArgs)))
	}
	rows, err = r.db.Query(ctx, fmt.Sprintf(`
SELECT bucket_start::text,
       COALESCE(SUM(request_count), 0) AS request_count,
       COALESCE(SUM(success_count), 0) AS success_count,
       COALESCE(SUM(error_count), 0) AS error_count,
       COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
       COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
       COALESCE(SUM(cached_tokens), 0) AS cached_tokens,
       COALESCE(SUM(total_tokens), 0) AS total_tokens
FROM usage_aggregates
WHERE %s
GROUP BY bucket_start
ORDER BY bucket_start`, strings.Join(dailyWhere, " AND ")), dailyArgs...)
	if err != nil {
		return trend, err
	}
	defer rows.Close()
	for rows.Next() {
		var point UsageDailyPoint
		if err := rows.Scan(
			&point.BucketStart,
			&point.RequestCount,
			&point.SuccessCount,
			&point.ErrorCount,
			&point.PromptTokens,
			&point.CompletionTokens,
			&point.CachedTokens,
			&point.TotalTokens,
		); err != nil {
			return trend, err
		}
		trend.Daily = append(trend.Daily, point)
		trend.Summary.RequestCount += point.RequestCount
		trend.Summary.SuccessCount += point.SuccessCount
		trend.Summary.ErrorCount += point.ErrorCount
		trend.Summary.PromptTokens += point.PromptTokens
		trend.Summary.CompletionTokens += point.CompletionTokens
		trend.Summary.CachedTokens += point.CachedTokens
		trend.Summary.TotalTokens += point.TotalTokens
	}
	if err := rows.Err(); err != nil {
		return trend, err
	}

	summaryWhere := []string{
		"bucket_size = 'day'",
		"username = $1",
		"bucket_start >= $2",
		"bucket_start < $3",
	}
	summaryArgs := []any{filter.Username, filter.Start, filter.End}
	if selectedModel != "" {
		summaryArgs = append(summaryArgs, selectedModel)
		summaryWhere = append(summaryWhere, fmt.Sprintf("model = $%d", len(summaryArgs)))
	}
	rows, err = r.db.Query(ctx, fmt.Sprintf(`
SELECT model,
       COALESCE(SUM(request_count), 0) AS request_count,
       COALESCE(SUM(success_count), 0) AS success_count,
       COALESCE(SUM(error_count), 0) AS error_count,
       COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
       COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
       COALESCE(SUM(cached_tokens), 0) AS cached_tokens,
       COALESCE(SUM(total_tokens), 0) AS total_tokens
FROM usage_aggregates
WHERE %s
GROUP BY model
ORDER BY total_tokens DESC`, strings.Join(summaryWhere, " AND ")), summaryArgs...)
	if err != nil {
		return trend, err
	}
	defer rows.Close()
	for rows.Next() {
		var summary UsageModelSummary
		if err := rows.Scan(
			&summary.Model,
			&summary.RequestCount,
			&summary.SuccessCount,
			&summary.ErrorCount,
			&summary.PromptTokens,
			&summary.CompletionTokens,
			&summary.CachedTokens,
			&summary.TotalTokens,
		); err != nil {
			return trend, err
		}
		trend.ModelSummary = append(trend.ModelSummary, summary)
	}
	return trend, rows.Err()
}

func (r Repository) ListTokenIdentities(ctx context.Context, filter TokenIdentityFilter) ([]TokenIdentitySummary, error) {
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
	if filter.Username != "" {
		add("c.username = $%d", filter.Username)
	}
	if filter.TokenFingerprint != "" {
		add("c.token_fingerprint = $%d", filter.TokenFingerprint)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`
SELECT c.fingerprint_display, c.token_fingerprint, c.new_api_token_id,
       c.token_name_raw, c.username, COALESCE(s.display_name, ''),
       COALESCE(s.department, c.department), c.token_status, c.token_group,
       c.last_seen_at::text
FROM token_identity_cache c
LEFT JOIN audit_subjects s ON s.username = c.username
WHERE %s
ORDER BY c.last_seen_at DESC
LIMIT $%d`, strings.Join(where, " AND "), len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TokenIdentitySummary{}
	for rows.Next() {
		var item TokenIdentitySummary
		if err := rows.Scan(
			&item.FingerprintDisplay,
			&item.TokenFingerprint,
			&item.NewAPITokenID,
			&item.TokenNameRaw,
			&item.Username,
			&item.DisplayName,
			&item.Department,
			&item.TokenStatus,
			&item.TokenGroup,
			&item.LastSeenAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) ListReviewDecisions(ctx context.Context, filter ReviewDecisionFilter) ([]ReviewDecision, error) {
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
	if filter.TargetType != "" {
		add("target_type = $%d", filter.TargetType)
	}
	if filter.TargetID != "" {
		add("target_id = $%d", filter.TargetID)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`
SELECT target_type, target_id, decision, reviewer_id, reviewer_username,
       note, created_at
FROM review_decisions
WHERE %s
ORDER BY created_at DESC
LIMIT $%d`, strings.Join(where, " AND "), len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReviewDecision{}
	for rows.Next() {
		var item ReviewDecision
		if err := rows.Scan(
			&item.TargetType,
			&item.TargetID,
			&item.Decision,
			&item.ReviewerID,
			&item.ReviewerUsername,
			&item.Note,
			&item.CreatedAt,
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
       username_snapshot, fingerprint_display, model_requested,
       usage_prompt_tokens, usage_completion_tokens, usage_cached_tokens, usage_total_tokens,
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
		&detail.Username,
		&detail.FingerprintDisplay,
		&detail.ModelRequested,
		&detail.UsagePromptTokens,
		&detail.UsageCompletionTokens,
		&detail.UsageCachedTokens,
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
	anomalies, err := r.listTraceAnomalies(ctx, traceID)
	if err != nil {
		return TraceDetail{}, err
	}
	detail.NormalizedMessages = messages
	detail.AnalysisResults = results
	detail.Anomalies = anomalies
	return detail, nil
}

func (r Repository) listNormalizedMessages(ctx context.Context, traceID string) ([]NormalizedMessageSummary, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
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
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
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

func (r Repository) listTraceAnomalies(ctx context.Context, traceID string) ([]AnomalySummary, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	rows, err := r.db.Query(ctx, `
SELECT anomaly_id, anomaly_type, severity, status, username, fingerprint_display,
       observed_value, threshold_value, reason, created_at::text
FROM usage_anomalies
WHERE $1 = ANY(sample_trace_ids)
ORDER BY created_at DESC`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AnomalySummary{}
	for rows.Next() {
		var item AnomalySummary
		if err := rows.Scan(
			&item.AnomalyID,
			&item.AnomalyType,
			&item.Severity,
			&item.Status,
			&item.Username,
			&item.FingerprintDisplay,
			&item.ObservedValue,
			&item.ThresholdValue,
			&item.Reason,
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
ORDER BY created_at DESC
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
