package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestRepositoryCreateSessionStoresOnlySessionID(t *testing.T) {
	execer := &recordingAdminDB{}
	repo := NewRepository(execer)

	err := repo.CreateSession(context.Background(), Session{
		SessionID: "sess_123",
		UserID:    7,
		ExpiresAt: time.Unix(2000, 0).UTC(),
		CSRFToken: "csrf_123",
	})

	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	if !strings.Contains(execer.sql, "INSERT INTO audit_sessions") {
		t.Fatalf("sql = %s", execer.sql)
	}
	if len(execer.args) != 4 {
		t.Fatalf("arg count = %d, want 4", len(execer.args))
	}
	if execer.args[0] != "sess_123" || execer.args[1] != int64(7) || execer.args[3] != "csrf_123" {
		t.Fatalf("args = %#v", execer.args)
	}
}

func TestRepositoryFindActiveUserByID(t *testing.T) {
	db := &recordingAdminDB{
		row: scanFuncRow{scan: func(dest ...any) error {
			*(dest[0].(*int64)) = int64(7)
			*(dest[1].(*string)) = "alice"
			*(dest[2].(*string)) = "$2a$10$abcdefghijklmnopqrstuu"
			*(dest[3].(*string)) = "Alice"
			*(dest[4].(*string)) = "alice@example.com"
			*(dest[5].(*Role)) = RoleAuditor
			*(dest[6].(*string)) = "active"
			*(dest[7].(*time.Time)) = time.Unix(100, 0).UTC()
			*(dest[8].(*time.Time)) = time.Unix(200, 0).UTC()
			return nil
		}},
	}
	repo := NewRepository(db)

	user, err := repo.FindActiveUserByID(context.Background(), 7)

	if err != nil {
		t.Fatalf("FindActiveUserByID error: %v", err)
	}
	if user.ID != 7 || user.Username != "alice" || user.Role != RoleAuditor {
		t.Fatalf("user = %#v", user)
	}
	if !strings.Contains(db.querySQL, "FROM audit_users") {
		t.Fatalf("querySQL = %s", db.querySQL)
	}
	if !strings.Contains(db.querySQL, "WHERE id = $1 AND status = 'active'") {
		t.Fatalf("querySQL missing active id predicate: %s", db.querySQL)
	}
	if len(db.queryArgs) != 1 || db.queryArgs[0] != int64(7) {
		t.Fatalf("queryArgs = %#v", db.queryArgs)
	}
}

func TestRepositoryFindActiveUserByIDRequiresDB(t *testing.T) {
	repo := NewRepository(nil)

	_, err := repo.FindActiveUserByID(context.Background(), 7)

	if !errors.Is(err, ErrAdminDBRequired) {
		t.Fatalf("FindActiveUserByID error = %v, want ErrAdminDBRequired", err)
	}
}

func TestRepositoryUpdateUserPassword(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)
	now := time.Unix(3000, 0).UTC()

	err := repo.UpdateUserPassword(context.Background(), 7, "$2a$10$newhashabcdefghijkl", now)

	if err != nil {
		t.Fatalf("UpdateUserPassword error: %v", err)
	}
	if !strings.Contains(db.sql, "UPDATE audit_users") {
		t.Fatalf("sql = %s", db.sql)
	}
	if !strings.Contains(db.sql, "SET password_hash = $2, updated_at = $3") {
		t.Fatalf("sql missing password update assignment: %s", db.sql)
	}
	if !strings.Contains(db.sql, "WHERE id = $1") {
		t.Fatalf("sql missing user id predicate: %s", db.sql)
	}
	if len(db.args) != 3 || db.args[0] != int64(7) || db.args[1] != "$2a$10$newhashabcdefghijkl" || db.args[2] != now {
		t.Fatalf("args = %#v", db.args)
	}
}

func TestRepositoryUpdateUserPasswordRequiresDB(t *testing.T) {
	repo := NewRepository(nil)

	err := repo.UpdateUserPassword(context.Background(), 7, "$2a$10$newhashabcdefghijkl", time.Unix(3000, 0).UTC())

	if !errors.Is(err, ErrAdminDBRequired) {
		t.Fatalf("UpdateUserPassword error = %v, want ErrAdminDBRequired", err)
	}
}

func TestRepositoryRevokeOtherSessions(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)
	now := time.Unix(3000, 0).UTC()

	err := repo.RevokeOtherSessions(context.Background(), 7, "sess_keep", now)

	if err != nil {
		t.Fatalf("RevokeOtherSessions error: %v", err)
	}
	if !strings.Contains(db.sql, "UPDATE audit_sessions") {
		t.Fatalf("sql = %s", db.sql)
	}
	for _, fragment := range []string{
		"user_id = $1",
		"session_id <> $2",
		"revoked_at IS NULL",
		"expires_at > $3",
	} {
		if !strings.Contains(db.sql, fragment) {
			t.Fatalf("sql missing %q: %s", fragment, db.sql)
		}
	}
	if len(db.args) != 3 || db.args[0] != int64(7) || db.args[1] != "sess_keep" || db.args[2] != now {
		t.Fatalf("args = %#v", db.args)
	}
}

func TestRepositoryRevokeOtherSessionsRequiresDB(t *testing.T) {
	repo := NewRepository(nil)

	err := repo.RevokeOtherSessions(context.Background(), 7, "sess_keep", time.Unix(3000, 0).UTC())

	if !errors.Is(err, ErrAdminDBRequired) {
		t.Fatalf("RevokeOtherSessions error = %v, want ErrAdminDBRequired", err)
	}
}

func TestRepositoryChangeUserPasswordUsesAtomicStatement(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)
	now := time.Unix(3000, 0).UTC()

	err := repo.ChangeUserPassword(context.Background(), 7, "$2a$10$newhashabcdefghijkl", "sess_keep", AuditActionLog{
		ActorUserID:        7,
		ActorUsername:      "alice",
		Action:             "password_changed",
		TargetType:         "audit_user",
		TargetID:           "alice",
		TokenFingerprint:   "fingerprint-value",
		FingerprintDisplay: "tkfp_example",
		TraceID:            "trace_123",
		IPHash:             "ip_hash",
		UserAgentHash:      "ua_hash",
		MetadataJSON:       "   ",
	}, now)

	if err != nil {
		t.Fatalf("ChangeUserPassword error: %v", err)
	}
	for _, fragment := range []string{
		"WITH updated_user AS",
		"UPDATE audit_users",
		"SET password_hash = $2, updated_at = $3",
		"WHERE id = $1",
		"revoked_sessions AS",
		"UPDATE audit_sessions",
		"session_id <> $4",
		"INSERT INTO audit_action_logs",
		"$15::jsonb",
		"FROM updated_user",
	} {
		if !strings.Contains(db.sql, fragment) {
			t.Fatalf("sql missing %q: %s", fragment, db.sql)
		}
	}
	if len(db.args) != 16 {
		t.Fatalf("arg count = %d, want 16: %#v", len(db.args), db.args)
	}
	wantArgs := []any{
		int64(7),
		"$2a$10$newhashabcdefghijkl",
		now,
		"sess_keep",
		int64(7),
		"alice",
		"password_changed",
		"audit_user",
		"alice",
		"fingerprint-value",
		"tkfp_example",
		"trace_123",
		"ip_hash",
		"ua_hash",
		"{}",
		now,
	}
	for i, want := range wantArgs {
		if db.args[i] != want {
			t.Fatalf("arg %d = %#v, want %#v", i, db.args[i], want)
		}
	}
}

func TestRepositoryChangeUserPasswordRequiresDB(t *testing.T) {
	repo := NewRepository(nil)

	err := repo.ChangeUserPassword(context.Background(), 7, "$2a$10$newhashabcdefghijkl", "sess_keep", AuditActionLog{}, time.Unix(3000, 0).UTC())

	if !errors.Is(err, ErrAdminDBRequired) {
		t.Fatalf("ChangeUserPassword error = %v, want ErrAdminDBRequired", err)
	}
}

func TestRepositoryInsertAuditActionLogWritesMetadataJSON(t *testing.T) {
	execer := &recordingAdminDB{}
	repo := NewRepository(execer)

	err := repo.InsertAuditActionLog(context.Background(), AuditActionLog{
		ActorUserID:        7,
		ActorUsername:      "alice",
		Action:             "api_key_lookup",
		TargetType:         "token",
		TargetID:           "tkfp_example",
		TokenFingerprint:   "fingerprint-value",
		FingerprintDisplay: "tkfp_example",
		MetadataJSON:       `{"result":"hit"}`,
		CreatedAt:          time.Unix(3000, 0).UTC(),
	})

	if err != nil {
		t.Fatalf("InsertAuditActionLog error: %v", err)
	}
	if !strings.Contains(execer.sql, "INSERT INTO audit_action_logs") {
		t.Fatalf("sql = %s", execer.sql)
	}
	if !strings.Contains(execer.sql, "$11::jsonb") {
		t.Fatalf("sql does not cast metadata JSON: %s", execer.sql)
	}
	joined := strings.TrimSpace(strings.Join(anyStrings(execer.args), " "))
	if strings.Contains(joined, "sk-secret") {
		t.Fatalf("audit log args leaked plaintext key: %#v", execer.args)
	}
}

func TestRepositoryInsertReviewDecision(t *testing.T) {
	execer := &recordingAdminDB{}
	repo := NewRepository(execer)

	err := repo.InsertReviewDecision(context.Background(), ReviewDecision{
		TargetType:       "anomaly",
		TargetID:         "anom_1",
		Decision:         "acknowledge",
		ReviewerID:       7,
		ReviewerUsername: "alice",
		Note:             "reviewed",
		CreatedAt:        time.Unix(4000, 0).UTC(),
	})

	if err != nil {
		t.Fatalf("InsertReviewDecision error: %v", err)
	}
	if !strings.Contains(execer.sql, "INSERT INTO review_decisions") {
		t.Fatalf("sql = %s", execer.sql)
	}
}

func TestRepositoryListTracesReturnsPaginationMetadata(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{
			scanFuncRow{scan: func(dest ...any) error {
				*(dest[0].(*int64)) = int64(120)
				return nil
			}},
		},
		rowsQueue: []pgx.Rows{
			&scanRows{scans: []func(dest ...any) error{
				func(dest ...any) error {
					*(dest[0].(*string)) = "trace_120"
					*(dest[1].(*string)) = "POST"
					*(dest[2].(*string)) = "/v1/chat/completions"
					*(dest[3].(*string)) = "/v1/chat/completions"
					*(dest[4].(*string)) = "openai_chat"
					*(dest[5].(*int)) = 200
					*(dest[6].(*string)) = "E10001"
					*(dest[7].(*string)) = "tkfp_abcd"
					*(dest[8].(*string)) = "gpt-5"
					*(dest[9].(*int)) = 10
					*(dest[10].(*int)) = 5
					*(dest[11].(*int)) = 2
					*(dest[12].(*int)) = 15
					*(dest[13].(*string)) = "2026-06-03 10:00:00+00"
					*(dest[14].(*bool)) = true
					return nil
				},
			}},
		},
	}
	repo := NewRepository(db)

	result, err := repo.ListTraces(context.Background(), TraceFilter{
		Page:  3,
		Limit: 50,
	})

	if err != nil {
		t.Fatalf("ListTraces error: %v", err)
	}
	if len(result.Traces) != 1 {
		t.Fatalf("trace count = %d, want 1", len(result.Traces))
	}
	if result.Traces[0].TraceID != "trace_120" || !result.Traces[0].NeedsReview {
		t.Fatalf("trace row = %#v", result.Traces[0])
	}
	if result.Pagination.Page != 3 || result.Pagination.PageSize != 50 {
		t.Fatalf("pagination page = %#v", result.Pagination)
	}
	if result.Pagination.TotalItems != 120 || result.Pagination.TotalPages != 3 {
		t.Fatalf("pagination totals = %#v", result.Pagination)
	}
	if !result.Pagination.HasPrev || result.Pagination.HasNext {
		t.Fatalf("pagination nav flags = %#v", result.Pagination)
	}
	if len(db.querySQLs) != 2 {
		t.Fatalf("querySQLs = %#v, want count + list queries", db.querySQLs)
	}
	if !strings.Contains(db.querySQLs[0], "SELECT count(*)") {
		t.Fatalf("count query = %s", db.querySQLs[0])
	}
	if !strings.Contains(db.querySQLs[1], "ORDER BY t.created_at DESC, t.trace_id DESC") {
		t.Fatalf("list query = %s", db.querySQLs[1])
	}
	if got := db.queryArgsLog[1]; len(got) != 2 || got[0] != 50 || got[1] != 100 {
		t.Fatalf("list query args = %#v, want [50 100]", got)
	}
}

func TestRepositoryListTracesClampsPageToLastPage(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{
			scanFuncRow{scan: func(dest ...any) error {
				*(dest[0].(*int64)) = int64(60)
				return nil
			}},
		},
		rowsQueue: []pgx.Rows{&fakeRows{}},
	}
	repo := NewRepository(db)

	result, err := repo.ListTraces(context.Background(), TraceFilter{
		Page:  99,
		Limit: 50,
	})

	if err != nil {
		t.Fatalf("ListTraces error: %v", err)
	}
	if result.Pagination.Page != 2 || result.Pagination.TotalPages != 2 {
		t.Fatalf("pagination = %#v, want last page 2/2", result.Pagination)
	}
	if got := db.queryArgsLog[1]; len(got) != 2 || got[0] != 50 || got[1] != 50 {
		t.Fatalf("list query args = %#v, want [50 50]", got)
	}
}

func TestRepositoryListTracesReturnsFirstPageForEmptyResults(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{
			scanFuncRow{scan: func(dest ...any) error {
				*(dest[0].(*int64)) = int64(0)
				return nil
			}},
		},
		rowsQueue: []pgx.Rows{&fakeRows{}},
	}
	repo := NewRepository(db)

	result, err := repo.ListTraces(context.Background(), TraceFilter{
		Page:  9,
		Limit: 50,
	})

	if err != nil {
		t.Fatalf("ListTraces error: %v", err)
	}
	if result.Pagination.Page != 1 || result.Pagination.TotalPages != 0 || result.Pagination.TotalItems != 0 {
		t.Fatalf("pagination = %#v, want empty result page 1 with zero totals", result.Pagination)
	}
	if result.Pagination.HasPrev || result.Pagination.HasNext {
		t.Fatalf("pagination nav flags = %#v, want no navigation", result.Pagination)
	}
	if got := db.queryArgsLog[1]; len(got) != 2 || got[0] != 50 || got[1] != 0 {
		t.Fatalf("list query args = %#v, want [50 0]", got)
	}
}

func TestRepositoryListTracesBindsFiltersAndCapsLimit(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{
			scanFuncRow{scan: func(dest ...any) error {
				*(dest[0].(*int64)) = int64(0)
				return nil
			}},
		},
		rowsQueue: []pgx.Rows{&fakeRows{}},
	}
	repo := NewRepository(db)

	_, err := repo.ListTraces(context.Background(), TraceFilter{
		TraceID:          "trace_123",
		Username:         "E10001",
		TokenFingerprint: "fp_123",
		RoutePattern:     "/v1/chat/completions",
		Model:            "gpt-5",
		StatusCode:       429,
		Limit:            500,
	})

	if err != nil {
		t.Fatalf("ListTraces error: %v", err)
	}
	if !strings.Contains(db.querySQLs[0], "t.trace_id = $1") ||
		!strings.Contains(db.querySQLs[0], "t.username_snapshot = $2") ||
		!strings.Contains(db.querySQLs[0], "t.token_fingerprint = $3") ||
		!strings.Contains(db.querySQLs[0], "t.route_pattern = $4") ||
		!strings.Contains(db.querySQLs[0], "t.model_requested = $5") ||
		!strings.Contains(db.querySQLs[0], "t.status_code = $6") {
		t.Fatalf("count query filters = %s", db.querySQLs[0])
	}
	if strings.Contains(db.querySQLs[1], "500") {
		t.Fatalf("list query interpolated raw limit: %s", db.querySQLs[1])
	}
	if got := db.queryArgsLog[0]; len(got) != 6 {
		t.Fatalf("count query args = %#v, want 6 filters", got)
	}
	if got := db.queryArgsLog[1]; len(got) != 8 || got[6] != 100 || got[7] != 0 {
		t.Fatalf("list query args = %#v, want filters + [100 0]", got)
	}
}

func TestRepositoryLookupTokenSummaryReturnsIdentityScanError(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{scanErrorRow{err: errors.New("identity scan failed")}},
	}
	repo := NewRepository(db)

	_, err := repo.LookupTokenSummary(context.Background(), "fingerprint-value", "tkfp_example")

	if err == nil {
		t.Fatal("LookupTokenSummary returned nil error for identity scan failure")
	}
}

func TestRepositoryLookupTokenSummaryReturnsAnomalyCountScanError(t *testing.T) {
	db := &recordingAdminDB{
		queryRowFunc: func(sql string, args ...any) pgx.Row {
			switch {
			case strings.Contains(sql, "FROM token_identity_cache"):
				return scanFuncRow{scan: scanTokenIdentity("E10001", 42, "prod key", 1)}
			case strings.Contains(sql, "FROM usage_anomalies"):
				return scanErrorRow{err: errors.New("anomaly count failed")}
			default:
				return fakeRow{}
			}
		},
		rowsQueue: []pgx.Rows{&fakeRows{}},
	}
	repo := NewRepository(db)

	_, err := repo.LookupTokenSummary(context.Background(), "fingerprint-value", "tkfp_example")

	if err == nil {
		t.Fatal("LookupTokenSummary returned nil error for anomaly count scan failure")
	}
}

func TestRepositoryLookupTokenSummaryToleratesMissingIdentityCacheRow(t *testing.T) {
	db := &recordingAdminDB{
		queryRowFunc: func(sql string, args ...any) pgx.Row {
			switch {
			case strings.Contains(sql, "FROM token_identity_cache"):
				return scanErrorRow{err: pgx.ErrNoRows}
			case strings.Contains(sql, "FROM usage_anomalies"):
				return scanFuncRow{scan: scanAnomalyCount(3)}
			default:
				return fakeRow{}
			}
		},
		rowsQueue: []pgx.Rows{&fakeRows{}},
	}
	repo := NewRepository(db)

	summary, err := repo.LookupTokenSummary(context.Background(), "fingerprint-value", "tkfp_example")

	if err != nil {
		t.Fatalf("LookupTokenSummary error: %v", err)
	}
	if summary.TokenFingerprint != "fingerprint-value" || summary.FingerprintDisplay != "tkfp_example" {
		t.Fatalf("summary fingerprint fields = %#v", summary)
	}
	if summary.Username != "" || summary.NewAPITokenID != 0 || summary.TokenName != "" || summary.TokenStatus != 0 {
		t.Fatalf("identity fields were populated for missing cache row: %#v", summary)
	}
	if summary.OpenAnomalyCount != 3 {
		t.Fatalf("OpenAnomalyCount = %d, want 3", summary.OpenAnomalyCount)
	}
}

func TestRepositoryLookupTokenSummaryReturnsRecentTracesWithoutTraceCountQuery(t *testing.T) {
	db := &recordingAdminDB{
		queryRowFunc: func(sql string, args ...any) pgx.Row {
			switch {
			case strings.Contains(sql, "FROM token_identity_cache"):
				return scanFuncRow{scan: scanTokenIdentity("E10001", 42, "prod key", 1)}
			case strings.Contains(sql, "SELECT count(*) FROM traces"):
				return scanErrorRow{err: errors.New("unexpected trace count query")}
			case strings.Contains(sql, "FROM usage_anomalies"):
				return scanFuncRow{scan: scanAnomalyCount(2)}
			default:
				return fakeRow{}
			}
		},
		rowsQueue: []pgx.Rows{
			&scanRows{scans: []func(dest ...any) error{
				func(dest ...any) error {
					*(dest[0].(*string)) = "trace_123"
					*(dest[1].(*string)) = "POST"
					*(dest[2].(*string)) = "/v1/chat/completions"
					*(dest[3].(*string)) = "/v1/chat/completions"
					*(dest[4].(*string)) = "openai_chat"
					*(dest[5].(*int)) = 200
					*(dest[6].(*string)) = "E10001"
					*(dest[7].(*string)) = "tkfp_example"
					*(dest[8].(*string)) = "gpt-5"
					*(dest[9].(*int)) = 10
					*(dest[10].(*int)) = 5
					*(dest[11].(*int)) = 2
					*(dest[12].(*int)) = 17
					*(dest[13].(*string)) = "2026-06-03 10:00:00+00"
					*(dest[14].(*bool)) = true
					return nil
				},
			}},
		},
	}
	repo := NewRepository(db)

	summary, err := repo.LookupTokenSummary(context.Background(), "fingerprint-value", "tkfp_example")

	if err != nil {
		t.Fatalf("LookupTokenSummary error: %v", err)
	}
	if summary.Username != "E10001" || summary.TokenName != "prod key" || summary.OpenAnomalyCount != 2 {
		t.Fatalf("summary identity/anomaly fields = %#v", summary)
	}
	if len(summary.RecentTraces) != 1 {
		t.Fatalf("RecentTraces len = %d, want 1", len(summary.RecentTraces))
	}
	trace := summary.RecentTraces[0]
	if trace.TraceID != "trace_123" || trace.ModelRequested != "gpt-5" || !trace.NeedsReview {
		t.Fatalf("recent trace = %#v", trace)
	}
	for _, sql := range db.querySQLs {
		if strings.Contains(sql, "SELECT count(*) FROM traces") {
			t.Fatalf("LookupTokenSummary unexpectedly issued trace count query: %s", sql)
		}
	}
}

func TestRepositoryOverviewSummaryUsesBoundedWindows(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{
			scanFuncRow{scan: func(dest ...any) error {
				*(dest[0].(*int64)) = 12
				*(dest[1].(*int64)) = 10
				*(dest[2].(*int64)) = 2
				*(dest[3].(*int64)) = 3400
				*(dest[4].(*int64)) = 3
				*(dest[5].(*int64)) = 4
				*(dest[6].(*int64)) = 1
				return nil
			}},
		},
	}
	repo := NewRepository(db)

	summary, err := repo.OverviewSummary(context.Background(), time.Unix(1000, 0).UTC())

	if err != nil {
		t.Fatalf("OverviewSummary error: %v", err)
	}
	if summary.RequestCount24h != 12 || summary.TotalTokens24h != 3400 || summary.OpenCoverageAlerts != 4 {
		t.Fatalf("summary = %#v", summary)
	}
	overviewSQL := db.querySQLs[0]
	if !strings.Contains(overviewSQL, "created_at >= $1") {
		t.Fatalf("overview query missing bounded window: %s", overviewSQL)
	}
	if !strings.Contains(overviewSQL, "capture_mode = 'raw_only'") {
		t.Fatalf("overview query missing raw-only capture mode filter: %s", overviewSQL)
	}
	if strings.Contains(overviewSQL, "route_support_level") {
		t.Fatalf("overview query used non-schema route_support_level column: %s", overviewSQL)
	}
}

func TestRepositoryOverviewSummaryIncludesThirtyDayTokenUsage(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{
			scanFuncRow{scan: func(dest ...any) error {
				*(dest[0].(*int64)) = 12
				*(dest[1].(*int64)) = 10
				*(dest[2].(*int64)) = 2
				*(dest[3].(*int64)) = 3400
				*(dest[4].(*int64)) = 3
				*(dest[5].(*int64)) = 4
				*(dest[6].(*int64)) = 1
				return nil
			}},
		},
		rowsQueue: []pgx.Rows{
			&scanRows{scans: []func(dest ...any) error{
				func(dest ...any) error {
					*(dest[0].(*string)) = "2026-05-01"
					*(dest[1].(*int64)) = 1000
					return nil
				},
				func(dest ...any) error {
					*(dest[0].(*string)) = "2026-05-29"
					*(dest[1].(*int64)) = 2500
					return nil
				},
			}},
		},
	}
	repo := NewRepository(db)

	summary, err := repo.OverviewSummary(context.Background(), time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC))

	if err != nil {
		t.Fatalf("OverviewSummary error: %v", err)
	}
	if len(summary.TokenUsageDaily) != 30 {
		t.Fatalf("daily token points = %d, want 30: %#v", len(summary.TokenUsageDaily), summary.TokenUsageDaily)
	}
	if summary.TokenUsageDaily[0].Date != "2026-04-30" || summary.TokenUsageDaily[0].TotalTokens != 0 {
		t.Fatalf("first daily token point = %#v, want 2026-04-30 zero", summary.TokenUsageDaily[0])
	}
	if summary.TokenUsageDaily[1].Date != "2026-05-01" || summary.TokenUsageDaily[1].TotalTokens != 1000 {
		t.Fatalf("second daily token point = %#v, want 2026-05-01 total 1000", summary.TokenUsageDaily[1])
	}
	if summary.TokenUsageDaily[29].Date != "2026-05-29" || summary.TokenUsageDaily[29].TotalTokens != 2500 {
		t.Fatalf("last daily token point = %#v, want 2026-05-29 total 2500", summary.TokenUsageDaily[29])
	}
	if !db.queried("FROM usage_aggregates") {
		t.Fatalf("expected daily usage query, got %#v", db.querySQLs)
	}
	if !strings.Contains(db.querySQLs[len(db.querySQLs)-1], "bucket_size = 'day'") {
		t.Fatalf("daily usage query missing day bucket filter: %s", db.querySQLs[len(db.querySQLs)-1])
	}
}

func TestRepositoryListUsageAggregatesCapsLimitAndBindsFilters(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)

	_, err := repo.ListUsageAggregates(context.Background(), UsageFilter{
		Username:         "E10001",
		TokenFingerprint: "fingerprint-value",
		BucketSize:       "hour",
		Limit:            500,
	})

	if err != nil {
		t.Fatalf("ListUsageAggregates error: %v", err)
	}
	if !strings.Contains(db.querySQL, "FROM usage_aggregates") {
		t.Fatalf("query = %s", db.querySQL)
	}
	for _, column := range []string{"prompt_tokens", "completion_tokens", "cached_tokens", "total_tokens"} {
		if !strings.Contains(db.querySQL, column) {
			t.Fatalf("query missing %s: %s", column, db.querySQL)
		}
	}
	if strings.Contains(db.querySQL, "500") {
		t.Fatalf("query interpolated limit instead of binding/capping: %s", db.querySQL)
	}
	if got := db.queryArgs[len(db.queryArgs)-1]; got != 100 {
		t.Fatalf("limit arg = %#v, want capped 100", got)
	}
}

func TestRepositoryEmployeeUsageTrendBuildsBoundedDailyQueries(t *testing.T) {
	db := &recordingAdminDB{rowsQueue: []pgx.Rows{&scanRows{}, &scanRows{}, &scanRows{}}}
	repo := NewRepository(db)
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)

	_, err := repo.EmployeeUsageTrend(context.Background(), EmployeeUsageFilter{
		Username:   "E10001",
		Range:      "30d",
		Model:      "gpt-5.2",
		Start:      start,
		End:        end,
		BucketSize: "day",
	})

	if err != nil {
		t.Fatalf("EmployeeUsageTrend error: %v", err)
	}
	joined := strings.Join(db.queryLog, "\n")
	if strings.Count(joined, "FROM usage_aggregates") != 3 {
		t.Fatalf("expected 3 usage_aggregates queries, got:\n%s", joined)
	}
	for _, required := range []string{
		"bucket_size = $4",
		"username = $1",
		"bucket_start >= $2",
		"bucket_start < $3",
		"SELECT DISTINCT model",
		"GROUP BY bucket_start",
		"GROUP BY model",
		"prompt_tokens",
		"completion_tokens",
		"cached_tokens",
		"total_tokens",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("query log missing %q:\n%s", required, joined)
		}
	}
	if len(db.queryArgsLog) != 3 {
		t.Fatalf("queryArgsLog len = %d, want 3", len(db.queryArgsLog))
	}
	for i, args := range db.queryArgsLog {
		if args[0] != "E10001" || args[1] != start || args[2] != end {
			t.Fatalf("args[%d] = %#v", i, args)
		}
	}
	if got := db.queryArgsLog[0][3]; got != "day" {
		t.Fatalf("distinct model bucket arg = %#v, want day", got)
	}
	if got := db.queryArgsLog[1][3]; got != "day" {
		t.Fatalf("points bucket arg = %#v, want day", got)
	}
	if got := db.queryArgsLog[1][4]; got != "gpt-5.2" {
		t.Fatalf("daily model arg = %#v, want gpt-5.2", got)
	}
	if got := db.queryArgsLog[2][3]; got != "day" {
		t.Fatalf("model summary bucket arg = %#v, want day", got)
	}
	if got := db.queryArgsLog[2][4]; got != "gpt-5.2" {
		t.Fatalf("model summary arg = %#v, want gpt-5.2", got)
	}
}

func TestRepositoryEmployeeUsageTrendScansModelsDailyAndSummary(t *testing.T) {
	db := &recordingAdminDB{rowsQueue: []pgx.Rows{
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				return nil
			},
			func(dest ...any) error {
				*(dest[0].(*string)) = "claude-4-sonnet"
				return nil
			},
		}},
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "2026-05-29 00:00:00+00"
				*(dest[1].(*int64)) = 12
				*(dest[2].(*int64)) = 11
				*(dest[3].(*int64)) = 1
				*(dest[4].(*int64)) = 1200
				*(dest[5].(*int64)) = 340
				*(dest[6].(*int64)) = 90
				*(dest[7].(*int64)) = 1540
				return nil
			},
		}},
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				*(dest[1].(*int64)) = 12
				*(dest[2].(*int64)) = 11
				*(dest[3].(*int64)) = 1
				*(dest[4].(*int64)) = 1200
				*(dest[5].(*int64)) = 340
				*(dest[6].(*int64)) = 90
				*(dest[7].(*int64)) = 1540
				return nil
			},
		}},
	}}
	repo := NewRepository(db)

	trend, err := repo.EmployeeUsageTrend(context.Background(), EmployeeUsageFilter{
		Username:   "E10001",
		Range:      "30d",
		Start:      time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		End:        time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
		BucketSize: "day",
	})

	if err != nil {
		t.Fatalf("EmployeeUsageTrend error: %v", err)
	}
	if trend.Username != "E10001" || trend.Range != "30d" {
		t.Fatalf("trend identity = %#v", trend)
	}
	if got := strings.Join(trend.Models, ","); got != "gpt-5.2,claude-4-sonnet" {
		t.Fatalf("models = %q", got)
	}
	if len(trend.Points) != 1 || trend.Points[0].PromptTokens != 1200 || trend.Points[0].TotalTokens != 1540 {
		t.Fatalf("points = %#v", trend.Points)
	}
	if len(trend.ModelSummary) != 1 || trend.ModelSummary[0].Model != "gpt-5.2" {
		t.Fatalf("model summary = %#v", trend.ModelSummary)
	}
	if trend.Summary.RequestCount != 12 || trend.Summary.PromptTokens != 1200 || trend.Summary.TotalTokens != 1540 {
		t.Fatalf("summary = %#v", trend.Summary)
	}
}

func TestRepositoryEmployeeUsageTrendIncludesBlankModelSummary(t *testing.T) {
	db := &recordingAdminDB{rowsQueue: []pgx.Rows{
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				return nil
			},
		}},
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "2026-05-29 00:00:00+00"
				*(dest[1].(*int64)) = int64(3)
				*(dest[2].(*int64)) = int64(3)
				*(dest[3].(*int64)) = int64(0)
				*(dest[4].(*int64)) = int64(30)
				*(dest[5].(*int64)) = int64(15)
				*(dest[6].(*int64)) = int64(5)
				*(dest[7].(*int64)) = int64(45)
				return nil
			},
		}},
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = ""
				*(dest[1].(*int64)) = int64(1)
				*(dest[2].(*int64)) = int64(1)
				*(dest[3].(*int64)) = int64(0)
				*(dest[4].(*int64)) = int64(10)
				*(dest[5].(*int64)) = int64(5)
				*(dest[6].(*int64)) = int64(2)
				*(dest[7].(*int64)) = int64(15)
				return nil
			},
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				*(dest[1].(*int64)) = int64(2)
				*(dest[2].(*int64)) = int64(2)
				*(dest[3].(*int64)) = int64(0)
				*(dest[4].(*int64)) = int64(20)
				*(dest[5].(*int64)) = int64(10)
				*(dest[6].(*int64)) = int64(3)
				*(dest[7].(*int64)) = int64(30)
				return nil
			},
		}},
	}}
	repo := NewRepository(db)

	trend, err := repo.EmployeeUsageTrend(context.Background(), EmployeeUsageFilter{
		Username:   "E10001",
		Range:      "30d",
		Model:      "   ",
		Start:      time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		End:        time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
		BucketSize: "day",
	})

	if err != nil {
		t.Fatalf("EmployeeUsageTrend error: %v", err)
	}
	if trend.SelectedModel != "" {
		t.Fatalf("SelectedModel = %q, want empty", trend.SelectedModel)
	}
	if got := strings.Join(trend.Models, ","); got != "gpt-5.2" {
		t.Fatalf("models = %q", got)
	}
	if len(db.queryLog) != 3 {
		t.Fatalf("queryLog len = %d, want 3", len(db.queryLog))
	}
	if !strings.Contains(db.queryLog[0], "model <> ''") {
		t.Fatalf("distinct model query should exclude blank models:\n%s", db.queryLog[0])
	}
	if strings.Contains(db.queryLog[2], "model <> ''") {
		t.Fatalf("model summary query should include blank models:\n%s", db.queryLog[2])
	}
	if len(trend.ModelSummary) != 2 {
		t.Fatalf("model summary = %#v", trend.ModelSummary)
	}
	if trend.ModelSummary[0].Model != "" {
		t.Fatalf("first model summary model = %q, want blank unknown bucket", trend.ModelSummary[0].Model)
	}
	var total int64
	for _, item := range trend.ModelSummary {
		total += item.TotalTokens
	}
	if total != trend.Summary.TotalTokens {
		t.Fatalf("model summary total = %d, summary total = %d", total, trend.Summary.TotalTokens)
	}
}

func TestRepositoryEmployeeUsageTrendPadsHourlyPointsFor1D(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	db := &recordingAdminDB{rowsQueue: []pgx.Rows{
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				return nil
			},
		}},
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "2026-06-04T15:00:00Z"
				*(dest[1].(*int64)) = 2
				*(dest[2].(*int64)) = 2
				*(dest[3].(*int64)) = 0
				*(dest[4].(*int64)) = 100
				*(dest[5].(*int64)) = 40
				*(dest[6].(*int64)) = 0
				*(dest[7].(*int64)) = 140
				return nil
			},
		}},
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				*(dest[1].(*int64)) = 2
				*(dest[2].(*int64)) = 2
				*(dest[3].(*int64)) = 0
				*(dest[4].(*int64)) = 100
				*(dest[5].(*int64)) = 40
				*(dest[6].(*int64)) = 0
				*(dest[7].(*int64)) = 140
				return nil
			},
		}},
	}}
	repo := NewRepository(db)

	trend, err := repo.EmployeeUsageTrend(context.Background(), EmployeeUsageFilter{
		Username:        "roy.zhang",
		Range:           "1d",
		Start:           start,
		End:             end,
		BucketSize:      "hour",
		ExpectedBuckets: 24,
	})
	if err != nil {
		t.Fatalf("EmployeeUsageTrend error: %v", err)
	}
	if trend.BucketSize != "hour" {
		t.Fatalf("BucketSize=%q, want hour", trend.BucketSize)
	}
	if len(trend.Points) != 24 {
		t.Fatalf("len(Points)=%d, want 24", len(trend.Points))
	}
	if trend.ActiveBucketCount != 1 {
		t.Fatalf("ActiveBucketCount=%d, want 1", trend.ActiveBucketCount)
	}
	if trend.Points[3].TotalTokens != 140 {
		t.Fatalf("Points[3].TotalTokens=%d, want 140", trend.Points[3].TotalTokens)
	}
	if trend.Points[0].TotalTokens != 0 || trend.Points[23].TotalTokens != 0 {
		t.Fatalf("expected zero-filled edges, got first=%d last=%d", trend.Points[0].TotalTokens, trend.Points[23].TotalTokens)
	}
}

func TestRepositoryGlobalUsageSummaryBuildsTopEmployeeAndModelLists(t *testing.T) {
	db := &recordingAdminDB{rowsQueue: []pgx.Rows{
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*int64)) = 18420
				*(dest[1].(*int64)) = 17
				*(dest[2].(*int64)) = 42
				*(dest[3].(*int64)) = 6
				return nil
			},
		}},
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "roy.zhang"
				*(dest[1].(*string)) = "Roy Zhang"
				*(dest[2].(*string)) = "Platform"
				*(dest[3].(*int64)) = 9000
				*(dest[4].(*int64)) = 12
				*(dest[5].(*string)) = "2026-06-05 08:00:00+00"
				return nil
			},
		}},
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				*(dest[1].(*int64)) = 12000
				*(dest[2].(*int64)) = 21
				return nil
			},
		}},
	}}
	repo := NewRepository(db)

	summary, err := repo.GlobalUsageSummary(context.Background(), time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GlobalUsageSummary error: %v", err)
	}
	if summary.TotalTokens != 18420 || summary.ActiveEmployees != 17 || summary.RequestCount != 42 || summary.ActiveModels != 6 {
		t.Fatalf("summary=%#v", summary)
	}
	if len(summary.TopEmployees) != 1 || summary.TopEmployees[0].Username != "roy.zhang" {
		t.Fatalf("TopEmployees=%#v", summary.TopEmployees)
	}
	if summary.TopEmployees[0].DisplayName != "Roy Zhang" || summary.TopEmployees[0].Department != "Platform" {
		t.Fatalf("TopEmployees identity fields=%#v", summary.TopEmployees[0])
	}
	if !strings.Contains(db.querySQLs[1], "LEFT JOIN token_identity_cache c") || !strings.Contains(db.querySQLs[1], "LEFT JOIN audit_subjects s") {
		t.Fatalf("top employees query should join identity tables, query=%s", db.querySQLs[1])
	}
	if len(summary.TopModels) != 1 || summary.TopModels[0].Model != "gpt-5.2" {
		t.Fatalf("TopModels=%#v", summary.TopModels)
	}
}

func TestRepositorySearchUsageEmployeesMatchesUsernameAndDisplayName(t *testing.T) {
	db := &recordingAdminDB{rowsQueue: []pgx.Rows{
		&scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "roy.zhang"
				*(dest[1].(*string)) = "Roy Zhang"
				*(dest[2].(*string)) = "Platform"
				*(dest[3].(*string)) = "2026-06-05 08:00:00+00"
				return nil
			},
		}},
	}}
	repo := NewRepository(db)

	results, err := repo.SearchUsageEmployees(context.Background(), UsageEmployeeSearchFilter{
		Query: "roy",
		Limit: 8,
	})
	if err != nil {
		t.Fatalf("SearchUsageEmployees error: %v", err)
	}
	if len(results) != 1 || results[0].DisplayName != "Roy Zhang" {
		t.Fatalf("results=%#v", results)
	}
	if !strings.Contains(db.querySQL, "ILIKE") {
		t.Fatalf("expected ILIKE fuzzy search, query=%s", db.querySQL)
	}
}

func TestListTokenIdentitiesQueryUsesCacheAndSubjects(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)
	_, _ = repo.ListTokenIdentities(context.Background(), TokenIdentityFilter{Username: "E10001", Limit: 500})

	if !strings.Contains(db.querySQL, "FROM token_identity_cache") {
		t.Fatalf("query = %s", db.querySQL)
	}
	if !strings.Contains(db.querySQL, "LEFT JOIN audit_subjects") {
		t.Fatalf("query missing audit subject enrichment: %s", db.querySQL)
	}
	if got := db.queryArgs[len(db.queryArgs)-1]; got != 100 {
		t.Fatalf("limit = %#v, want capped 100", got)
	}
}

func TestListReviewDecisionsQuery(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)
	_, _ = repo.ListReviewDecisions(context.Background(), ReviewDecisionFilter{TargetType: "anomaly", Limit: 500})

	if !strings.Contains(db.querySQL, "FROM review_decisions") {
		t.Fatalf("query = %s", db.querySQL)
	}
	if got := db.queryArgs[len(db.queryArgs)-1]; got != 100 {
		t.Fatalf("limit = %#v, want capped 100", got)
	}
}

func TestRepositoryInsertContextCatalogEntryWritesAuditorIdentity(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)

	err := repo.InsertContextCatalogEntry(context.Background(), ContextCatalogEntry{
		ContextType:            "repo",
		Name:                   "new-api-gateway",
		Description:            "Audit gateway repository",
		Keywords:               []string{"gateway", "new-api"},
		Aliases:                []string{"audit gateway"},
		Owner:                  "platform",
		ExpectedTaskCategories: []string{"coding", "debugging"},
		ExpectedModels:         []string{"gpt-5.2"},
		ExpectedUsageLevel:     "medium",
		Active:                 true,
		CreatedBy:              "alice",
		UpdatedBy:              "alice",
	})

	if err != nil {
		t.Fatalf("InsertContextCatalogEntry error: %v", err)
	}
	if !strings.Contains(db.sql, "INSERT INTO context_catalog") {
		t.Fatalf("sql = %s", db.sql)
	}
	if !strings.Contains(db.sql, "ON CONFLICT (context_type, name) DO UPDATE") {
		t.Fatalf("missing upsert clause: %s", db.sql)
	}
}

func TestRepositoryGetTraceDetailScansMessagesAndAnalysisResults(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{
			scanFuncRow{scan: func(dest ...any) error {
				*(dest[0].(*string)) = "trace-123"
				*(dest[1].(*string)) = "POST"
				*(dest[2].(*string)) = "/v1/chat/completions"
				*(dest[3].(*string)) = "/v1/chat/completions"
				*(dest[4].(*string)) = "openai"
				*(dest[5].(*int)) = 200
				*(dest[6].(*string)) = "E10001"
				*(dest[7].(*string)) = "prod key"
				*(dest[8].(*string)) = "gpt-5.2"
				*(dest[9].(*int)) = 111
				*(dest[10].(*int)) = 222
				*(dest[11].(*int)) = 33
				*(dest[12].(*int)) = 321
				*(dest[13].(*string)) = "2026-04-28 10:00:00+00"
				*(dest[14].(*string)) = "raw/request.json"
				*(dest[15].(*string)) = "raw/response.json"
				*(dest[16].(*string)) = "raw/request-headers.json"
				*(dest[17].(*string)) = "raw/response-headers.json"
				*(dest[18].(*string)) = "resolved"
				*(dest[19].(*string)) = "completed"
				*(dest[20].(*bool)) = true
				*(dest[21].(*string)) = "failed"
				return nil
			}},
		},
		rowsQueue: []pgx.Rows{
			&scanRows{scans: []func(dest ...any) error{
				func(dest ...any) error {
					*(dest[0].(*string)) = "request"
					*(dest[1].(*int)) = 0
					*(dest[2].(*string)) = "user"
					*(dest[3].(*string)) = "text"
					*(dest[4].(*string)) = "hello"
					*(dest[5].(*string)) = ""
					*(dest[6].(*string)) = "message"
					*(dest[7].(*int)) = 8
					return nil
				},
			}},
			&scanRows{scans: []func(dest ...any) error{
				func(dest ...any) error {
					*(dest[0].(*string)) = "work_relevance"
					*(dest[1].(*string)) = "work_relevance"
					*(dest[2].(*string)) = "work"
					*(dest[3].(*string)) = "0.92"
					*(dest[4].(*string)) = "0.88"
					*(dest[5].(*string)) = "low"
					*(dest[6].(*string)) = `{"matched":"gateway"}`
					*(dest[7].(*string)) = "2026-04-28 10:01:00+00"
					return nil
				},
			}},
			&scanRows{scans: []func(dest ...any) error{
				func(dest ...any) error {
					*(dest[0].(*string)) = "anom-1"
					*(dest[1].(*string)) = "non_work_job_search"
					*(dest[2].(*string)) = "high"
					*(dest[3].(*string)) = "open"
					*(dest[4].(*string)) = "E10001"
					*(dest[5].(*string)) = "tkfp_display"
					*(dest[6].(*string)) = "300"
					*(dest[7].(*string)) = "0"
					*(dest[8].(*string)) = "resume rewrite detected"
					*(dest[9].(*string)) = "2026-04-28 10:02:00+00"
					return nil
				},
			}},
		},
	}
	repo := NewRepository(db)

	detail, err := repo.GetTraceDetail(context.Background(), "trace-123")

	if err != nil {
		t.Fatalf("GetTraceDetail error: %v", err)
	}
	if detail.TraceID != "trace-123" || detail.Username != "E10001" || detail.RequestRawRef != "raw/request.json" {
		t.Fatalf("detail representative fields = %#v", detail)
	}
	if detail.UsagePromptTokens != 111 || detail.UsageCompletionTokens != 222 || detail.UsageCachedTokens != 33 || detail.UsageTotalTokens != 321 {
		t.Fatalf("usage fields = %#v", detail.TraceSummary)
	}
	if len(detail.NormalizedMessages) != 1 || detail.NormalizedMessages[0].ContentText != "hello" || detail.NormalizedMessages[0].TokenCountEstimate != 8 {
		t.Fatalf("normalized messages = %#v", detail.NormalizedMessages)
	}
	if len(detail.AnalysisResults) != 1 || detail.AnalysisResults[0].Label != "work" || detail.AnalysisResults[0].ResultJSON != `{"matched":"gateway"}` {
		t.Fatalf("analysis results = %#v", detail.AnalysisResults)
	}
	if len(detail.Anomalies) != 1 || detail.Anomalies[0].AnomalyType != "non_work_job_search" {
		t.Fatalf("anomalies = %#v", detail.Anomalies)
	}
	if detail.AnalysisStatus != "completed_with_enrichment_failure" {
		t.Fatalf("analysis_status = %q", detail.AnalysisStatus)
	}
	if !db.queried("FROM normalized_messages") || !db.queried("FROM analysis_results") || !db.queried("FROM usage_anomalies") {
		t.Fatalf("expected detail helper queries, got %#v", db.querySQLs)
	}
}

func TestRepositoryListAnalysisRuntimeHistory(t *testing.T) {
	db := &recordingAdminDB{
		rowsQueue: []pgx.Rows{
			&scanRows{scans: []func(dest ...any) error{
				func(dest ...any) error {
					*(dest[0].(*string)) = "2026-06-03T10:00:00Z"
					*(dest[1].(*int64)) = 12
					*(dest[2].(*int64)) = 45
					*(dest[3].(*int64)) = 1200
					*(dest[4].(*int64)) = 900
					return nil
				},
			}},
		},
	}
	repo := NewRepository(db)
	since := time.Date(2026, 6, 3, 9, 45, 0, 0, time.UTC)

	items, err := repo.ListAnalysisRuntimeHistory(context.Background(), "core", since)

	if err != nil {
		t.Fatalf("ListAnalysisRuntimeHistory error: %v", err)
	}
	if len(items) != 1 || items[0].Stage != "core" || items[0].QueueDepth != 12 {
		t.Fatalf("items = %#v", items)
	}
	if !strings.Contains(db.querySQL, "FROM analysis_runtime_samples") {
		t.Fatalf("query = %s", db.querySQL)
	}
	if !strings.Contains(db.querySQL, "sampled_at >= $2") {
		t.Fatalf("query missing sampled_at lower bound: %s", db.querySQL)
	}
	if !strings.Contains(db.querySQL, "ORDER BY sampled_at ASC") {
		t.Fatalf("query missing ascending sampled_at order: %s", db.querySQL)
	}
	if got := db.queryArgs[0]; got != "core" {
		t.Fatalf("stage arg = %#v, want core", got)
	}
	if got := db.queryArgs[1]; got != since {
		t.Fatalf("since arg = %#v, want %#v", got, since)
	}
}

func TestRepositoryListContextCatalogScansArraysAndCapsLimit(t *testing.T) {
	db := &recordingAdminDB{
		rowsQueue: []pgx.Rows{
			&scanRows{scans: []func(dest ...any) error{
				func(dest ...any) error {
					*(dest[0].(*int64)) = 7
					*(dest[1].(*string)) = "repo"
					*(dest[2].(*string)) = "new-api-gateway"
					*(dest[3].(*string)) = "Audit gateway repository"
					*(dest[4].(*pgtype.FlatArray[string])) = pgtype.FlatArray[string]{"gateway", "new-api"}
					*(dest[5].(*pgtype.FlatArray[string])) = pgtype.FlatArray[string]{"audit gateway"}
					*(dest[6].(*string)) = "platform"
					*(dest[7].(*pgtype.FlatArray[string])) = pgtype.FlatArray[string]{"coding", "debugging"}
					*(dest[8].(*pgtype.FlatArray[string])) = pgtype.FlatArray[string]{"gpt-5.2"}
					*(dest[9].(*string)) = "medium"
					*(dest[10].(*bool)) = true
					*(dest[11].(*string)) = "alice"
					*(dest[12].(*string)) = "bob"
					*(dest[13].(*string)) = "2026-04-28 09:00:00+00"
					*(dest[14].(*string)) = "2026-04-28 10:00:00+00"
					return nil
				},
			}},
		},
	}
	repo := NewRepository(db)

	items, err := repo.ListContextCatalog(context.Background(), true, 500)

	if err != nil {
		t.Fatalf("ListContextCatalog error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item := items[0]
	if item.ID != 7 || item.Name != "new-api-gateway" || item.Owner != "platform" || !item.Active {
		t.Fatalf("context catalog representative fields = %#v", item)
	}
	if strings.Join(item.Keywords, ",") != "gateway,new-api" || strings.Join(item.ExpectedModels, ",") != "gpt-5.2" {
		t.Fatalf("array fields = %#v", item)
	}
	if got := db.queryArgs[len(db.queryArgs)-1]; got != 100 {
		t.Fatalf("limit arg = %#v, want capped 100", got)
	}
}

func TestRepositoryListAuditActionLogsScansRowsAndCapsLimit(t *testing.T) {
	db := &recordingAdminDB{
		rowsQueue: []pgx.Rows{
			&scanRows{scans: []func(dest ...any) error{
				func(dest ...any) error {
					*(dest[0].(*string)) = "alice"
					*(dest[1].(*string)) = "api_key_lookup"
					*(dest[2].(*string)) = "token"
					*(dest[3].(*string)) = "tkfp_example"
					*(dest[4].(*string)) = "prod key"
					*(dest[5].(*string)) = "trace-123"
					*(dest[6].(*string)) = `{"result":"hit"}`
					*(dest[7].(*string)) = "2026-04-28 10:00:00+00"
					return nil
				},
			}},
		},
	}
	repo := NewRepository(db)

	items, err := repo.ListAuditActionLogs(context.Background(), 500)

	if err != nil {
		t.Fatalf("ListAuditActionLogs error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item := items[0]
	if item.ActorUsername != "alice" || item.Action != "api_key_lookup" || item.MetadataJSON != `{"result":"hit"}` {
		t.Fatalf("audit log representative fields = %#v", item)
	}
	if got := db.queryArgs[len(db.queryArgs)-1]; got != 100 {
		t.Fatalf("limit arg = %#v, want capped 100", got)
	}
}

func TestRepositoryDetailHelpersRequireDB(t *testing.T) {
	repo := NewRepository(nil)

	if _, err := repo.listNormalizedMessages(context.Background(), "trace-123"); !errors.Is(err, ErrAdminDBRequired) {
		t.Fatalf("listNormalizedMessages error = %v, want ErrAdminDBRequired", err)
	}
	if _, err := repo.listAnalysisResults(context.Background(), "trace-123"); !errors.Is(err, ErrAdminDBRequired) {
		t.Fatalf("listAnalysisResults error = %v, want ErrAdminDBRequired", err)
	}
	if _, err := repo.listTraceAnomalies(context.Background(), "trace-123"); !errors.Is(err, ErrAdminDBRequired) {
		t.Fatalf("listTraceAnomalies error = %v, want ErrAdminDBRequired", err)
	}
}

type recordingAdminDB struct {
	sql          string
	args         []any
	querySQL     string
	queryArgs    []any
	queryLog     []string
	queryArgsLog [][]any
	querySQLs    []string
	queryRowFunc func(sql string, args ...any) pgx.Row
	rows         pgx.Rows
	rowsQueue    []pgx.Rows
	row          pgx.Row
	rowQueue     []pgx.Row
}

func (db *recordingAdminDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	db.sql = sql
	db.args = append([]any(nil), args...)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (db *recordingAdminDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.querySQL = sql
	db.querySQLs = append(db.querySQLs, sql)
	db.queryArgs = append([]any(nil), args...)
	db.queryLog = append(db.queryLog, sql)
	db.queryArgsLog = append(db.queryArgsLog, append([]any(nil), args...))
	if len(db.rowsQueue) > 0 {
		rows := db.rowsQueue[0]
		db.rowsQueue = db.rowsQueue[1:]
		return rows, nil
	}
	if db.rows != nil {
		return db.rows, nil
	}
	return &fakeRows{}, nil
}

func (db *recordingAdminDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	db.querySQL = sql
	db.querySQLs = append(db.querySQLs, sql)
	db.queryArgs = append([]any(nil), args...)
	db.queryLog = append(db.queryLog, sql)
	db.queryArgsLog = append(db.queryArgsLog, append([]any(nil), args...))
	if db.queryRowFunc != nil {
		return db.queryRowFunc(sql, args...)
	}
	if len(db.rowQueue) > 0 {
		row := db.rowQueue[0]
		db.rowQueue = db.rowQueue[1:]
		return row
	}
	if len(db.rowsQueue) > 0 {
		rows := db.rowsQueue[0]
		db.rowsQueue = db.rowsQueue[1:]
		return rowsToRow{rows: rows}
	}
	if db.row != nil {
		return db.row
	}
	return fakeRow{}
}

func (db *recordingAdminDB) queried(fragment string) bool {
	for _, sql := range db.querySQLs {
		if strings.Contains(sql, fragment) {
			return true
		}
	}
	return false
}

type fakeRows struct{}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool                                   { return false }
func (r *fakeRows) Scan(dest ...any) error                       { return pgx.ErrNoRows }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

type rowsToRow struct {
	rows pgx.Rows
}

func (r rowsToRow) Scan(dest ...any) error {
	if r.rows == nil || !r.rows.Next() {
		return pgx.ErrNoRows
	}
	return r.rows.Scan(dest...)
}

type fakeRow struct{}

func (fakeRow) Scan(dest ...any) error { return pgx.ErrNoRows }

type scanRows struct {
	scans   []func(dest ...any) error
	current int
}

func (r *scanRows) Close()                                       {}
func (r *scanRows) Err() error                                   { return nil }
func (r *scanRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *scanRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *scanRows) Values() ([]any, error)                       { return nil, nil }
func (r *scanRows) RawValues() [][]byte                          { return nil }
func (r *scanRows) Conn() *pgx.Conn                              { return nil }

func (r *scanRows) Next() bool {
	if r.current >= len(r.scans) {
		return false
	}
	r.current++
	return true
}

func (r *scanRows) Scan(dest ...any) error {
	if r.current == 0 || r.current > len(r.scans) {
		return pgx.ErrNoRows
	}
	return r.scans[r.current-1](dest...)
}

type scanErrorRow struct {
	err error
}

func (r scanErrorRow) Scan(dest ...any) error { return r.err }

type scanFuncRow struct {
	scan func(dest ...any) error
}

func (r scanFuncRow) Scan(dest ...any) error { return r.scan(dest...) }

func scanTokenIdentity(employeeNo string, tokenID int, tokenName string, tokenStatus int) func(dest ...any) error {
	return func(dest ...any) error {
		*(dest[0].(*string)) = employeeNo
		*(dest[1].(*int)) = tokenID
		*(dest[2].(*string)) = tokenName
		*(dest[3].(*int)) = tokenStatus
		return nil
	}
}

func scanAnomalyCount(count int) func(dest ...any) error {
	return func(dest ...any) error {
		*(dest[0].(*int)) = count
		return nil
	}
}

func anyStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s, ok := value.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
