package identity

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakePostgresCacheDB struct {
	row       pgx.Row
	querySQL  string
	queryArgs []any
	execSQL   string
	execArgs  []any
	execErr   error
}

func (f *fakePostgresCacheDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	f.querySQL = sql
	f.queryArgs = append([]any(nil), args...)
	return f.row
}

func (f *fakePostgresCacheDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execSQL = sql
	f.execArgs = append([]any(nil), args...)
	return pgconn.NewCommandTag("UPDATE 1"), f.execErr
}

type fakePostgresRow struct {
	values []any
	err    error
}

func (r fakePostgresRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("unexpected scan destination count")
	}
	for i := range dest {
		target := reflect.ValueOf(dest[i])
		if target.Kind() != reflect.Ptr || target.IsNil() {
			return errors.New("scan destination is not pointer")
		}
		target.Elem().Set(reflect.ValueOf(r.values[i]))
	}
	return nil
}

func TestPostgresCacheGetReadsFreshSnapshot(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	db := &fakePostgresCacheDB{
		row: fakePostgresRow{values: []any{
			"tkfp_abc",
			42,
			"E10001 token",
			"E10001",
			1,
			"staff",
			int64(1711111111),
			int64(1712222222),
			300,
			25,
			true,
			true,
			`{"gpt-4":10}`,
		}},
	}
	cache := PostgresCache{DB: db, Now: func() time.Time { return now }}

	got, ok, err := cache.Get(context.Background(), "fp-read")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.TokenFingerprint != "fp-read" || got.EmployeeNo != "E10001" || got.NewAPITokenID != 42 {
		t.Fatalf("unexpected snapshot %#v", got)
	}
	if got.ResolutionStatus != ResolutionStatusResolved || got.IdentityCacheStatus != IdentityCacheStatusHit {
		t.Fatalf("unexpected statuses %#v", got)
	}
	if !strings.Contains(db.querySQL, "FROM token_identity_cache") {
		t.Fatalf("query SQL = %q, want token_identity_cache read", db.querySQL)
	}
}

func TestPostgresCacheSetUpsertsSnapshot(t *testing.T) {
	db := &fakePostgresCacheDB{}
	cache := PostgresCache{
		DB:  db,
		TTL: time.Hour,
		Now: func() time.Time { return time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC) },
	}

	err := cache.Set(context.Background(), Snapshot{
		TokenFingerprint:   "fp-set",
		FingerprintDisplay: "tkfp_set",
		NewAPITokenID:      42,
		TokenNameRaw:       "E10001 token",
		EmployeeNo:         "E10001",
	})
	if err != nil {
		t.Fatalf("Set error: %v", err)
	}
	if !strings.Contains(db.execSQL, "ON CONFLICT (token_fingerprint)") {
		t.Fatalf("exec SQL = %q, want upsert on token_fingerprint", db.execSQL)
	}
	if len(db.execArgs) == 0 || db.execArgs[0] != "fp-set" {
		t.Fatalf("first exec arg = %#v, want fingerprint", db.execArgs)
	}
}

func TestPostgresCacheRejectsTypedNilDB(t *testing.T) {
	var db *fakePostgresCacheDB
	cache := PostgresCache{DB: db}

	_, _, getErr := cache.Get(context.Background(), "fp")
	if !errors.Is(getErr, ErrPostgresCacheDBRequired) {
		t.Fatalf("Get error = %v, want %v", getErr, ErrPostgresCacheDBRequired)
	}
	setErr := cache.Set(context.Background(), Snapshot{TokenFingerprint: "fp"})
	if !errors.Is(setErr, ErrPostgresCacheDBRequired) {
		t.Fatalf("Set error = %v, want %v", setErr, ErrPostgresCacheDBRequired)
	}
}

func TestChainCacheReadsSecondCacheAndBackfillsFirst(t *testing.T) {
	first := &fakeCache{}
	second := &fakeCache{
		ok: true,
		value: Snapshot{
			TokenFingerprint: "fp-chain",
			EmployeeNo:       "E10001",
		},
	}
	cache := ChainCache{Caches: []Cache{first, second}}

	got, ok, err := cache.Get(context.Background(), "fp-chain")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if !ok {
		t.Fatal("expected chain cache hit")
	}
	if got.EmployeeNo != "E10001" {
		t.Fatalf("EmployeeNo = %q", got.EmployeeNo)
	}
	if first.setCalls != 1 {
		t.Fatalf("first cache Set called %d times", first.setCalls)
	}
}
