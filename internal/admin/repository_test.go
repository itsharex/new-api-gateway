package admin

import (
	"context"
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

type recordingAdminDB struct {
	sql       string
	args      []any
	querySQL  string
	queryArgs []any
	rows      pgx.Rows
	row       pgx.Row
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

func anyStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s, ok := value.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
