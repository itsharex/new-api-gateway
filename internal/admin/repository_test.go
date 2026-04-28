package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRepositoryCreateSessionStoresOnlySessionID(t *testing.T) {
	execer := &recordingAdminDB{}
	repo := NewRepository(execer)

	err := repo.CreateSession(context.Background(), Session{
		SessionID: "sess_123",
		UserID:    7,
		ExpiresAt: time.Unix(2000, 0).UTC(),
	})

	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	if !strings.Contains(execer.sql, "INSERT INTO audit_sessions") {
		t.Fatalf("sql = %s", execer.sql)
	}
	if len(execer.args) != 3 {
		t.Fatalf("arg count = %d, want 3", len(execer.args))
	}
	if execer.args[0] != "sess_123" || execer.args[1] != int64(7) {
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
		EmployeeNo:   "E10001",
		RoutePattern: "/v1/chat/completions",
		Limit:        500,
	})

	if err != nil {
		t.Fatalf("ListTraces error: %v", err)
	}
	if !strings.Contains(db.querySQL, "FROM traces") {
		t.Fatalf("query = %s", db.querySQL)
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
	if summary.EmployeeNo != "" || summary.NewAPITokenID != 0 || summary.TokenName != "" || summary.TokenStatus != 0 {
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
	if !strings.Contains(db.querySQL, "created_at >= $1") {
		t.Fatalf("overview query missing bounded window: %s", db.querySQL)
	}
	if !strings.Contains(db.querySQL, "capture_mode = 'raw_only'") {
		t.Fatalf("overview query missing raw-only capture mode filter: %s", db.querySQL)
	}
	if strings.Contains(db.querySQL, "route_support_level") {
		t.Fatalf("overview query used non-schema route_support_level column: %s", db.querySQL)
	}
}

func TestRepositoryListUsageAggregatesCapsLimitAndBindsFilters(t *testing.T) {
	db := &recordingAdminDB{}
	repo := NewRepository(db)

	_, err := repo.ListUsageAggregates(context.Background(), UsageFilter{
		EmployeeNo:       "E10001",
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
	if strings.Contains(db.querySQL, "500") {
		t.Fatalf("query interpolated limit instead of binding/capping: %s", db.querySQL)
	}
	if got := db.queryArgs[len(db.queryArgs)-1]; got != 100 {
		t.Fatalf("limit arg = %#v, want capped 100", got)
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

type recordingAdminDB struct {
	sql       string
	args      []any
	querySQL  string
	queryArgs []any
	rows      pgx.Rows
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
	db.queryArgs = append([]any(nil), args...)
	if db.rows != nil {
		return db.rows, nil
	}
	return &fakeRows{}, nil
}

func (db *recordingAdminDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	db.querySQL = sql
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
