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

func TestRepositoryListTracesBuildsBoundedQuery(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)
	db.rows = &fakeRows{}

	_, err := repo.ListTraces(context.Background(), TraceFilter{
		Username:     "E10001",
		RoutePattern: "/v1/chat/completions",
		Limit:        500,
	})

	if err != nil {
		t.Fatalf("ListTraces error: %v", err)
	}
	if !strings.Contains(db.querySQL, "FROM traces") {
		t.Fatalf("query = %s", db.querySQL)
	}
	for _, column := range []string{"t.usage_prompt_tokens", "t.usage_completion_tokens", "t.usage_cached_tokens", "t.usage_total_tokens"} {
		if !strings.Contains(db.querySQL, column) {
			t.Fatalf("query missing %s: %s", column, db.querySQL)
		}
	}
	if strings.Contains(db.querySQL, "500") {
		t.Fatalf("query interpolated limit instead of binding/capping: %s", db.querySQL)
	}
	if len(db.queryArgs) == 0 {
		t.Fatal("expected bound query args")
	}
	if got := db.queryArgs[len(db.queryArgs)-1]; got != 100 {
		t.Fatalf("limit arg = %#v, want capped 100", got)
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
		rowQueue: []pgx.Row{
			scanFuncRow{scan: scanTokenIdentity("E10001", 42, "prod key", 1)},
			scanErrorRow{err: errors.New("anomaly count failed")},
		},
	}
	repo := NewRepository(db)

	_, err := repo.LookupTokenSummary(context.Background(), "fingerprint-value", "tkfp_example")

	if err == nil {
		t.Fatal("LookupTokenSummary returned nil error for anomaly count scan failure")
	}
}

func TestRepositoryLookupTokenSummaryToleratesMissingIdentityCacheRow(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{
			scanErrorRow{err: pgx.ErrNoRows},
			scanFuncRow{scan: scanAnomalyCount(3)},
		},
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
				*(dest[19].(*string)) = "complete"
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
	if !db.queried("FROM normalized_messages") || !db.queried("FROM analysis_results") {
		t.Fatalf("expected detail helper queries, got %#v", db.querySQLs)
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
}

type recordingAdminDB struct {
	sql       string
	args      []any
	querySQL  string
	querySQLs []string
	queryArgs []any
	rows      pgx.Rows
	rowsQueue []pgx.Rows
	row       pgx.Row
	rowQueue  []pgx.Row
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
	if len(db.rowQueue) > 0 {
		row := db.rowQueue[0]
		db.rowQueue = db.rowQueue[1:]
		return row
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
