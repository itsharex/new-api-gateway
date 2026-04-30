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
	execSQLs  []string
	execArgss [][]any
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
	f.execSQLs = append(f.execSQLs, sql)
	f.execArgss = append(f.execArgss, append([]any(nil), args...))
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
			false,
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

func TestPostgresCacheGetReturnsMissForNoRows(t *testing.T) {
	db := &fakePostgresCacheDB{row: fakePostgresRow{err: pgx.ErrNoRows}}
	cache := PostgresCache{DB: db}

	got, ok, err := cache.Get(context.Background(), "fp-missing")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if ok {
		t.Fatalf("ok = true, want false with snapshot %#v", got)
	}
	if len(db.execSQLs) != 0 {
		t.Fatalf("exec calls = %#v, want none for miss", db.execSQLs)
	}
}

func TestPostgresCacheGetReturnsMissForExpiredRow(t *testing.T) {
	db := &fakePostgresCacheDB{
		row: fakePostgresRow{values: []any{
			"tkfp_expired",
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
			true,
		}},
	}
	cache := PostgresCache{DB: db}

	got, ok, err := cache.Get(context.Background(), "fp-expired")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if ok {
		t.Fatalf("ok = true, want false with snapshot %#v", got)
	}
	if len(db.execSQLs) != 0 {
		t.Fatalf("exec calls = %#v, want no last_seen_at update for expired row", db.execSQLs)
	}
}

func TestPostgresCacheGetSQLTreatsNullExpiresAtAsExpired(t *testing.T) {
	sql := postgresCacheGetSQL()
	if !strings.Contains(sql, "COALESCE(expires_at <= $2, true) AS expired") {
		t.Fatalf("postgresCacheGetSQL() = %q, want NULL expires_at treated as expired", sql)
	}
}

func TestPostgresCacheGetUpdatesLastSeenAtBestEffortOnHit(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	db := &fakePostgresCacheDB{
		row: fakePostgresRow{values: []any{
			"tkfp_hit",
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
			false,
		}},
		execErr: errors.New("last_seen update unavailable"),
	}
	cache := PostgresCache{DB: db, Now: func() time.Time { return now }}

	_, ok, err := cache.Get(context.Background(), "fp-hit")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if !ok {
		t.Fatal("expected hit")
	}
	if len(db.execSQLs) != 1 {
		t.Fatalf("exec calls = %#v, want one best-effort update", db.execSQLs)
	}
	if !strings.Contains(db.execSQLs[0], "UPDATE token_identity_cache SET last_seen_at") {
		t.Fatalf("exec SQL = %q, want last_seen_at update", db.execSQLs[0])
	}
	if len(db.execArgss[0]) != 2 || db.execArgss[0][0] != "fp-hit" || db.execArgss[0][1] != now {
		t.Fatalf("exec args = %#v, want fingerprint and now", db.execArgss[0])
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

func TestPostgresCacheSetUsesDefaultTTL(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	db := &fakePostgresCacheDB{}
	cache := PostgresCache{DB: db, Now: func() time.Time { return now }}

	if err := cache.Set(context.Background(), Snapshot{TokenFingerprint: "fp-default"}); err != nil {
		t.Fatalf("Set error: %v", err)
	}
	if len(db.execArgs) < 16 {
		t.Fatalf("exec args = %#v, want expires_at arg", db.execArgs)
	}
	if got, want := db.execArgs[15], now.Add(15*time.Minute); got != want {
		t.Fatalf("expires_at = %#v, want %#v", got, want)
	}
}

func TestPostgresCacheSetUsesCustomTTL(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	db := &fakePostgresCacheDB{}
	cache := PostgresCache{DB: db, TTL: 45 * time.Minute, Now: func() time.Time { return now }}

	if err := cache.Set(context.Background(), Snapshot{TokenFingerprint: "fp-custom"}); err != nil {
		t.Fatalf("Set error: %v", err)
	}
	if len(db.execArgs) < 16 {
		t.Fatalf("exec args = %#v, want expires_at arg", db.execArgs)
	}
	if got, want := db.execArgs[15], now.Add(45*time.Minute); got != want {
		t.Fatalf("expires_at = %#v, want %#v", got, want)
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

func TestChainCacheContinuesAfterFirstCacheErrorAndBackfills(t *testing.T) {
	first := &fakeCache{getErr: errors.New("redis unavailable")}
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
		t.Fatal("expected hit from second cache")
	}
	if got.EmployeeNo != "E10001" {
		t.Fatalf("EmployeeNo = %q", got.EmployeeNo)
	}
	if first.setCalls != 1 {
		t.Fatalf("first cache Set called %d times", first.setCalls)
	}
}

func TestChainCacheSkipsTypedNilCaches(t *testing.T) {
	var first *fakeCache
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
	if !ok || got.EmployeeNo != "E10001" {
		t.Fatalf("unexpected result ok=%v snapshot=%#v", ok, got)
	}
}

func TestChainCacheSetFansOutAndReturnsFirstError(t *testing.T) {
	firstErr := errors.New("redis unavailable")
	secondErr := errors.New("postgres unavailable")
	first := &fakeCache{setErr: firstErr}
	second := &fakeCache{}
	third := &fakeCache{setErr: secondErr}
	cache := ChainCache{Caches: []Cache{first, second, third}}

	err := cache.Set(context.Background(), Snapshot{TokenFingerprint: "fp-chain"})
	if !errors.Is(err, firstErr) {
		t.Fatalf("Set error = %v, want %v", err, firstErr)
	}
	if first.setCalls != 1 || second.setCalls != 1 || third.setCalls != 1 {
		t.Fatalf("set calls first=%d second=%d third=%d, want fan-out", first.setCalls, second.setCalls, third.setCalls)
	}
}
