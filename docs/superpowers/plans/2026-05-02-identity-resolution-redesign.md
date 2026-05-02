# Identity Resolution Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace token-name-as-employee-number identity resolution with a direct new-api database lookup that resolves API keys to actual usernames via a JOIN query.

**Architecture:** Add a second read-only PostgreSQL connection to new-api's database. A new `NewAPILookup` component executes a single `tokens JOIN users` query. The existing `TokenLookup` interface stays; only the implementation changes. The `employee` package is removed entirely. All `employee_no` fields are renamed to `username` across Go, SQL, and Python.

**Tech Stack:** Go 1.22+, pgx/v5, go-redis/v9, Python 3.12+ (analysis worker)

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/config/config.go` | Modify | Add `NewAPIPostgresDSN`, remove `EmployeeNoPattern` |
| `internal/identity/stores.go` | Modify | Add `Username` to `NewAPIToken`; rename `EmployeeNo` → `Username` in `Snapshot`; remove old status constants |
| `internal/identity/newapi_lookup.go` | Create | `NewAPILookup` — queries new-api DB with `tokens JOIN users` |
| `internal/identity/newapi_lookup_test.go` | Create | Tests for the new lookup |
| `internal/identity/postgres_token_lookup.go` | Modify | Remove old `PostgresTokenLookup` struct (replaced by `NewAPILookup`) |
| `internal/identity/postgres_token_lookup_test.go` | Modify | Remove old tests (replaced by newapi_lookup tests) |
| `internal/identity/resolver.go` | Modify | Remove employee validation, use `Username` from lookup |
| `internal/identity/resolver_test.go` | Modify | Rewrite tests for new behavior |
| `internal/identity/postgres_cache.go` | Modify | Adapt SQL for `username` column |
| `internal/identity/postgres_cache_test.go` | Modify | Update field names in tests |
| `internal/employee/` | Delete | Entire package |
| `cmd/audit-gateway/main.go` | Modify | Wire new `NewAPILookup` with new-api DB pool |
| `internal/gateway/proxy.go` | Modify | `EmployeeNoSnapshot` → `UsernameSnapshot` |
| `internal/traces/model.go` | Modify | `EmployeeNoSnapshot` → `UsernameSnapshot` |
| `internal/traces/repository.go` | Modify | SQL column rename |
| `internal/traces/repository_test.go` | Modify | Update field names |
| `internal/jobs/jobs.go` | Modify | `EmployeeNo` → `Username` |
| `internal/jobs/jobs_test.go` | Modify | Update field names |
| `migrations/0012_rename_employee_no_to_username.sql` | Create | Rename DB columns and indexes |
| `workers/analysis_worker/models.py` | Modify | `employee_no` → `username` |
| `workers/analysis_worker/contract_example.json` | Modify | Update example |
| `workers/analysis_worker/rules.py` | Modify | Update references |
| `workers/analysis_worker/repository.py` | Modify | Update SQL column refs |
| `workers/analysis_worker/main.py` | Modify | Update field refs |
| `workers/analysis_worker/tests/*.py` | Modify | Update all test references |

---

### Task 1: Config — Add NewAPIPostgresDSN, Remove EmployeeNoPattern

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add `NewAPIPostgresDSN` field to `Config` struct and remove `EmployeeNoPattern`**

In `internal/config/config.go`:

Remove `EmployeeNoPattern *regexp.Regexp` from the `Config` struct (line ~23).

Add `NewAPIPostgresDSN string` to the `Config` struct, after `PostgresDSN`.

- [ ] **Step 2: Remove EMPLOYEE_NO_PATTERN loading and add NEW_API_POSTGRES_DSN loading**

In `LoadFromEnv()`:

Remove the block that loads `EMPLOYEE_NO_PATTERN` (the `pattern, err := getenvDefault(...)` and `compiled, err := regexp.Compile(...)` lines near the top of the function).

After the `postgresDSN` loading block (after `validatePostgresDSN`), add:

```go
newAPIPostgresDSN, err := requiredEnv("NEW_API_POSTGRES_DSN")
if err != nil {
    return Config{}, err
}
if err := validatePostgresDSN(newAPIPostgresDSN); err != nil {
    return Config{}, fmt.Errorf("invalid NEW_API_POSTGRES_DSN: %w", err)
}
```

- [ ] **Step 3: Update the Config struct initialization**

In the `cfg := Config{...}` block:

Remove `EmployeeNoPattern: compiled,`.

Add `NewAPIPostgresDSN: newAPIPostgresDSN,`.

- [ ] **Step 4: Remove unused imports**

Remove `"regexp"` from the import list if no other code uses it. Check if `regexp` is used elsewhere in the file — it's not, so remove it.

- [ ] **Step 5: Run tests to verify compilation fails as expected**

Run: `cd /Users/roy/codes/new-api-gateway && go build ./internal/config/`

Expected: Compile errors in downstream packages that reference `EmployeeNoPattern` and `employee` package. This is expected — we'll fix them in subsequent tasks.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add NewAPIPostgresDSN, remove EmployeeNoPattern"
```

---

### Task 2: Model Updates — Snapshot and NewAPIToken

**Files:**
- Modify: `internal/identity/stores.go`

- [ ] **Step 1: Add `Username` to `NewAPIToken` and rename `EmployeeNo` → `Username` in `Snapshot`**

In `internal/identity/stores.go`:

In `NewAPIToken` struct, add a `Username string` field after `TokenName`:

```go
type NewAPIToken struct {
	TokenID            int
	TokenName          string
	Username           string
	TokenStatus        int
	TokenGroup         string
	ExpiredTime        int64
	AccessedTime       int64
	RemainQuota        int
	UsedQuota          int
	UnlimitedQuota     bool
	ModelLimitsEnabled bool
	ModelLimits        string
}
```

In `Snapshot` struct, rename `EmployeeNo string` to `Username string`:

```go
type Snapshot struct {
	TokenFingerprint    string
	FingerprintDisplay  string
	NewAPITokenID       int
	TokenNameRaw        string
	Username            string
	TokenStatus         int
	TokenGroup          string
	ExpiredTime         int64
	AccessedTime        int64
	RemainQuota         int
	UsedQuota           int
	UnlimitedQuota      bool
	ModelLimitsEnabled  bool
	ModelLimits         string
	ResolutionStatus    string
	IdentityCacheStatus string
}
```

- [ ] **Step 2: Remove employee-related resolution status constants**

In `internal/identity/stores.go` (or `resolver.go` where they're defined), remove:

```go
ResolutionStatusMissingEmployeeNo = "missing_employee_no"
ResolutionStatusInvalidEmployeeNo = "invalid_employee_no"
```

Keep all others:
```go
const (
	ResolutionStatusResolved          = "resolved"
	ResolutionStatusDBError           = "db_error"
	ResolutionStatusNotFound          = "not_found"
	ResolutionStatusExtractFailed     = "extract_failed"
	ResolutionStatusResolveFailed     = "resolve_failed"
)
```

- [ ] **Step 3: Commit**

```bash
git add internal/identity/stores.go
git commit -m "feat(identity): add Username to NewAPIToken, rename EmployeeNo to Username in Snapshot"
```

---

### Task 3: NewAPILookup Component

**Files:**
- Create: `internal/identity/newapi_lookup.go`
- Create: `internal/identity/newapi_lookup_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/identity/newapi_lookup_test.go`:

```go
package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewAPILookupQueryJoinTokensAndUsers(t *testing.T) {
	query := normalizeSQL(newAPIUserLookupQuery())
	if !strings.Contains(query, "from tokens t") {
		t.Fatalf("query does not read tokens table: %s", query)
	}
	if !strings.Contains(query, "join users u") {
		t.Fatalf("query must join users table: %s", query)
	}
	if !strings.Contains(query, "u.username") {
		t.Fatalf("query must select u.username: %s", query)
	}
	if !strings.Contains(query, "t.deleted_at is null") {
		t.Fatalf("query must filter soft-deleted tokens: %s", query)
	}
	if !strings.Contains(query, "u.status = 1") {
		t.Fatalf("query must filter enabled users: %s", query)
	}
	if !strings.Contains(query, "where t.key = $1") {
		t.Fatalf("query must filter by key: %s", query)
	}
	if !strings.Contains(query, "limit 1") {
		t.Fatalf("query must have limit 1: %s", query)
	}
}

func TestNewAPILookupQuerySelectsColumnsInOrder(t *testing.T) {
	query := normalizeSQL(newAPIUserLookupQuery())
	assertSQLFragmentsInOrder(t, query,
		"t.id as token_id",
		"t.name as token_name",
		"u.username",
		"t.status as token_status",
		`"group" as token_group`,
		"t.expired_time",
		"t.accessed_time",
		"t.remain_quota",
		"t.used_quota",
		"t.unlimited_quota",
		"t.model_limits_enabled",
		"t.model_limits",
		"from tokens t",
		"join users u on t.user_id = u.id",
		"where t.key = $1",
		"and t.deleted_at is null",
		"and u.status = 1",
		"limit 1",
	)
}

func TestNewAPILookupRequiresPool(t *testing.T) {
	_, err := NewAPILookup{}.FindByCanonicalKey(context.Background(), "key")
	if !errors.Is(err, ErrNewAPILookupPoolRequired) {
		t.Fatalf("FindByCanonicalKey error = %v, want %v", err, ErrNewAPILookupPoolRequired)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/roy/codes/new-api-gateway && go test ./internal/identity/ -run TestNewAPILookup -v`

Expected: FAIL — `newAPIUserLookupQuery` and `NewAPILookup` not defined.

- [ ] **Step 3: Write the implementation**

Create `internal/identity/newapi_lookup.go`:

```go
package identity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNewAPILookupPoolRequired = errors.New("identity new-api lookup pool is nil")

// NewAPILookup resolves API keys to tokens + usernames by querying
// the new-api PostgreSQL database (tokens JOIN users).
type NewAPILookup struct {
	Pool *pgxpool.Pool
}

func (l NewAPILookup) FindByCanonicalKey(ctx context.Context, canonicalKey string) (NewAPIToken, error) {
	if l.Pool == nil {
		return NewAPIToken{}, ErrNewAPILookupPoolRequired
	}

	var token NewAPIToken
	err := l.Pool.QueryRow(ctx, newAPIUserLookupQuery(), canonicalKey).Scan(
		&token.TokenID,
		&token.TokenName,
		&token.Username,
		&token.TokenStatus,
		&token.TokenGroup,
		&token.ExpiredTime,
		&token.AccessedTime,
		&token.RemainQuota,
		&token.UsedQuota,
		&token.UnlimitedQuota,
		&token.ModelLimitsEnabled,
		&token.ModelLimits,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return NewAPIToken{}, ErrTokenNotFound
	}
	return token, err
}

func newAPIUserLookupQuery() string {
	return `
SELECT
  t.id AS token_id,
  t.name AS token_name,
  u.username,
  t.status AS token_status,
  t."group" AS token_group,
  t.expired_time,
  t.accessed_time,
  t.remain_quota,
  t.used_quota,
  t.unlimited_quota,
  t.model_limits_enabled,
  t.model_limits
FROM tokens t
JOIN users u ON t.user_id = u.id
WHERE t.key = $1
  AND t.deleted_at IS NULL
  AND u.status = 1
LIMIT 1`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/roy/codes/new-api-gateway && go test ./internal/identity/ -run TestNewAPILookup -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/identity/newapi_lookup.go internal/identity/newapi_lookup_test.go
git commit -m "feat(identity): add NewAPILookup with tokens JOIN users query"
```

---

### Task 4: Resolver Rewrite

**Files:**
- Modify: `internal/identity/resolver.go`
- Modify: `internal/identity/resolver_test.go`

- [ ] **Step 1: Rewrite the failing tests**

Replace the entire contents of `internal/identity/resolver_test.go`:

```go
package identity

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestResolverUsesCacheHit(t *testing.T) {
	cache := &fakeCache{ok: true, value: Snapshot{TokenFingerprint: "fp", Username: "alice", ResolutionStatus: ResolutionStatusResolved}}
	lookup := &fakeLookup{}
	resolver := Resolver{Cache: cache, Lookup: lookup}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.Username != "alice" || got.IdentityCacheStatus != IdentityCacheStatusHit {
		t.Fatalf("unexpected snapshot %#v", got)
	}
	if lookup.calls != 0 {
		t.Fatalf("lookup called %d times on cache hit", lookup.calls)
	}
}

func TestResolverReturnsErrorForNilLookup(t *testing.T) {
	resolver := Resolver{}

	if _, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc"); err == nil {
		t.Fatal("expected error for nil lookup")
	}
}

func TestResolverReturnsErrorForTypedNilLookup(t *testing.T) {
	var lookup *fakeLookup
	resolver := Resolver{Lookup: lookup}

	if _, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc"); err == nil {
		t.Fatal("expected error for typed nil lookup")
	}
}

func TestResolverTreatsTypedNilCacheAsNoCache(t *testing.T) {
	var cache *fakeCache
	lookup := &fakeLookup{token: NewAPIToken{TokenID: 12, Username: "bob", TokenStatus: 1}}
	resolver := Resolver{Cache: cache, Lookup: lookup}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("lookup called %d times", lookup.calls)
	}
	if got.ResolutionStatus != ResolutionStatusResolved || got.IdentityCacheStatus != IdentityCacheStatusMissDBLookup {
		t.Fatalf("unexpected snapshot %#v", got)
	}
}

func TestResolverUsesUsernameFromLookup(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:  cache,
		Lookup: &fakeLookup{token: NewAPIToken{TokenID: 12, Username: "charlie", TokenStatus: 1}},
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.Username != "charlie" {
		t.Fatalf("Username = %q", got.Username)
	}
	if got.ResolutionStatus != ResolutionStatusResolved {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache Set called %d times", cache.setCalls)
	}
}

func TestResolverCopiesTokenMetadata(t *testing.T) {
	token := NewAPIToken{
		TokenID:            12,
		TokenName:          "my-token",
		Username:           "dave",
		TokenStatus:        1,
		TokenGroup:         "staff",
		ExpiredTime:        1711111111,
		AccessedTime:       1712222222,
		RemainQuota:        300,
		UsedQuota:          25,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: true,
		ModelLimits:        `{"gpt-4":10}`,
	}
	resolver := Resolver{Lookup: &fakeLookup{token: token}}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.TokenFingerprint != "fp" || got.FingerprintDisplay != "tkfp_abc" {
		t.Fatalf("fingerprint fields not copied: %#v", got)
	}
	if got.NewAPITokenID != token.TokenID || got.TokenNameRaw != token.TokenName || got.Username != token.Username {
		t.Fatalf("identity metadata not copied: %#v", got)
	}
	if got.TokenStatus != token.TokenStatus || got.TokenGroup != token.TokenGroup {
		t.Fatalf("basic token metadata not copied: %#v", got)
	}
	if got.ExpiredTime != token.ExpiredTime || got.AccessedTime != token.AccessedTime || got.RemainQuota != token.RemainQuota || got.UsedQuota != token.UsedQuota {
		t.Fatalf("quota/time metadata not copied: %#v", got)
	}
	if got.UnlimitedQuota != token.UnlimitedQuota || got.ModelLimitsEnabled != token.ModelLimitsEnabled || got.ModelLimits != token.ModelLimits {
		t.Fatalf("limit metadata not copied: %#v", got)
	}
}

func TestResolverMarksTokenNotFound(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:  cache,
		Lookup: &fakeLookup{err: ErrTokenNotFound},
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != ResolutionStatusNotFound {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if got.TokenFingerprint != "fp" || got.FingerprintDisplay != "tkfp_abc" {
		t.Fatalf("fingerprint fields not copied: %#v", got)
	}
	if cache.setCalls != 0 {
		t.Fatalf("cache Set called %d times for token not found", cache.setCalls)
	}
}

func TestResolverMarksWrappedTokenNotFound(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:  cache,
		Lookup: &fakeLookup{err: fmt.Errorf("lookup failed: %w", ErrTokenNotFound)},
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != ResolutionStatusNotFound {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if cache.setCalls != 0 {
		t.Fatalf("cache Set called %d times for wrapped token not found", cache.setCalls)
	}
}

func TestResolverMarksLookupErrorsAsDBError(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:  cache,
		Lookup: &fakeLookup{err: errors.New("database unavailable")},
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != ResolutionStatusDBError {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if got.IdentityCacheStatus != IdentityCacheStatusMissDBLookup {
		t.Fatalf("IdentityCacheStatus = %q", got.IdentityCacheStatus)
	}
	if cache.setCalls != 0 {
		t.Fatalf("cache Set called %d times for lookup error", cache.setCalls)
	}
}

func TestResolverContinuesToLookupAfterCacheGetError(t *testing.T) {
	cache := &fakeCache{getErr: errors.New("redis unavailable")}
	lookup := &fakeLookup{token: NewAPIToken{TokenID: 12, Username: "eve", TokenStatus: 1}}
	resolver := Resolver{Cache: cache, Lookup: lookup}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("lookup called %d times", lookup.calls)
	}
	if got.ResolutionStatus != ResolutionStatusResolved || got.IdentityCacheStatus != IdentityCacheStatusCacheError {
		t.Fatalf("unexpected snapshot %#v", got)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache Set called %d times", cache.setCalls)
	}
}

func TestResolverIgnoresCacheSetError(t *testing.T) {
	cache := &fakeCache{setErr: errors.New("redis unavailable")}
	resolver := Resolver{
		Cache:  cache,
		Lookup: &fakeLookup{token: NewAPIToken{TokenID: 12, Username: "frank", TokenStatus: 1}},
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != ResolutionStatusResolved {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache Set called %d times", cache.setCalls)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/roy/codes/new-api-gateway && go test ./internal/identity/ -run TestResolver -v`

Expected: FAIL — `Resolver` struct still has `EmployeeNoPattern`, still imports `employee`, etc.

- [ ] **Step 3: Rewrite the Resolver**

Replace the entire contents of `internal/identity/resolver.go`:

```go
package identity

import (
	"context"
	"errors"
	"reflect"
)

var errResolverLookupRequired = errors.New("identity resolver lookup is nil")

// Resolver resolves API key fingerprints to user identities via a
// TokenLookup (typically NewAPILookup) with optional caching.
type Resolver struct {
	Cache  Cache
	Lookup TokenLookup
}

func (r Resolver) Resolve(ctx context.Context, canonicalKey, fingerprintValue, fingerprintDisplay string) (Snapshot, error) {
	if isNilInterface(r.Lookup) {
		return Snapshot{}, errResolverLookupRequired
	}

	cacheStatus := IdentityCacheStatusMissDBLookup
	cache := r.Cache
	if !isNilInterface(cache) {
		cached, ok, err := cache.Get(ctx, fingerprintValue)
		if err != nil {
			cacheStatus = IdentityCacheStatusCacheError
		} else if ok {
			cached.IdentityCacheStatus = IdentityCacheStatusHit
			return cached, nil
		}
	}

	token, err := r.Lookup.FindByCanonicalKey(ctx, canonicalKey)
	if err != nil {
		status := ResolutionStatusDBError
		if errors.Is(err, ErrTokenNotFound) {
			status = ResolutionStatusNotFound
		}
		return Snapshot{
			TokenFingerprint:    fingerprintValue,
			FingerprintDisplay:  fingerprintDisplay,
			ResolutionStatus:    status,
			IdentityCacheStatus: cacheStatus,
		}, nil
	}

	snapshot := Snapshot{
		TokenFingerprint:    fingerprintValue,
		FingerprintDisplay:  fingerprintDisplay,
		NewAPITokenID:       token.TokenID,
		TokenNameRaw:        token.TokenName,
		Username:            token.Username,
		TokenStatus:         token.TokenStatus,
		TokenGroup:          token.TokenGroup,
		ExpiredTime:         token.ExpiredTime,
		AccessedTime:        token.AccessedTime,
		RemainQuota:         token.RemainQuota,
		UsedQuota:           token.UsedQuota,
		UnlimitedQuota:      token.UnlimitedQuota,
		ModelLimitsEnabled:  token.ModelLimitsEnabled,
		ModelLimits:         token.ModelLimits,
		ResolutionStatus:    ResolutionStatusResolved,
		IdentityCacheStatus: cacheStatus,
	}
	if !isNilInterface(cache) && snapshot.ResolutionStatus == ResolutionStatusResolved {
		_ = cache.Set(ctx, snapshot)
	}
	return snapshot, nil
}

func isNilInterface[T any](v T) bool {
	if any(v) == nil {
		return true
	}

	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/roy/codes/new-api-gateway && go test ./internal/identity/ -run TestResolver -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/identity/resolver.go internal/identity/resolver_test.go
git commit -m "feat(identity): rewrite resolver to use username from lookup, remove employee validation"
```

---

### Task 5: Remove Old PostgresTokenLookup and Employee Package

**Files:**
- Delete: `internal/identity/postgres_token_lookup.go`
- Delete: `internal/identity/postgres_token_lookup_test.go`
- Delete: `internal/employee/employee.go`
- Delete: `internal/employee/employee_test.go`

- [ ] **Step 1: Remove old PostgresTokenLookup files**

```bash
rm internal/identity/postgres_token_lookup.go internal/identity/postgres_token_lookup_test.go
```

The `PostgresTokenLookup` is replaced by `NewAPILookup`. The `TokenLookup` interface in `stores.go` stays the same — `NewAPILookup` implements it.

- [ ] **Step 2: Remove employee package**

```bash
rm -r internal/employee/
```

- [ ] **Step 3: Run tests to verify**

Run: `cd /Users/roy/codes/new-api-gateway && go test ./internal/identity/ -v`

Expected: All tests pass. No references to `employee` package remain in `internal/identity/`.

- [ ] **Step 4: Commit**

```bash
git add -u internal/identity/postgres_token_lookup.go internal/identity/postgres_token_lookup_test.go internal/employee/
git commit -m "chore: remove PostgresTokenLookup and employee package"
```

---

### Task 6: Update PostgresCache for Username Field

**Files:**
- Modify: `internal/identity/postgres_cache.go`
- Modify: `internal/identity/postgres_cache_test.go`

- [ ] **Step 1: Update PostgresCache.Get to scan Username instead of EmployeeNo**

In `internal/identity/postgres_cache.go`, in the `Get` method's `Scan` call, change `&snapshot.EmployeeNo` to `&snapshot.Username`:

```go
func (c PostgresCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	if isNilInterface(c.DB) {
		return Snapshot{}, false, ErrPostgresCacheDBRequired
	}
	now := c.now()
	var snapshot Snapshot
	var expired bool
	err := c.DB.QueryRow(ctx, postgresCacheGetSQL(), fingerprint, now).Scan(
		&snapshot.FingerprintDisplay,
		&snapshot.NewAPITokenID,
		&snapshot.TokenNameRaw,
		&snapshot.Username,
		&snapshot.TokenStatus,
		&snapshot.TokenGroup,
		&snapshot.ExpiredTime,
		&snapshot.AccessedTime,
		&snapshot.RemainQuota,
		&snapshot.UsedQuota,
		&snapshot.UnlimitedQuota,
		&snapshot.ModelLimitsEnabled,
		&snapshot.ModelLimits,
		&expired,
	)
	// ... rest unchanged
```

- [ ] **Step 2: Update PostgresCache.Set to use Username**

In the `Set` method, change `snapshot.EmployeeNo` to `snapshot.Username`:

```go
func (c PostgresCache) Set(ctx context.Context, snapshot Snapshot) error {
	// ...
	_, err := c.DB.Exec(ctx, postgresCacheSetSQL(),
		snapshot.TokenFingerprint,
		snapshot.FingerprintDisplay,
		snapshot.NewAPITokenID,
		snapshot.TokenNameRaw,
		snapshot.Username,    // changed from EmployeeNo
		// ... rest of args unchanged
```

- [ ] **Step 3: Update the SQL queries for the renamed column**

Update `postgresCacheGetSQL()` — change `employee_no` to `username`:

```go
func postgresCacheGetSQL() string {
	return `
SELECT
  fingerprint_display,
  new_api_token_id,
  token_name_raw,
  username,
  token_status,
  token_group,
  token_expired_time,
  token_accessed_time,
  remain_quota,
  used_quota,
  unlimited_quota,
  model_limits_enabled,
  model_limits,
  COALESCE(expires_at <= $2, true) AS expired
FROM token_identity_cache
WHERE token_fingerprint = $1
LIMIT 1`
}
```

Update `postgresCacheSetSQL()` — change `employee_no` to `username`:

```go
func postgresCacheSetSQL() string {
	return `
INSERT INTO token_identity_cache (
  token_fingerprint,
  fingerprint_display,
  new_api_token_id,
  token_name_raw,
  username,
  token_status,
  token_group,
  token_expired_time,
  token_accessed_time,
  remain_quota,
  used_quota,
  unlimited_quota,
  model_limits_enabled,
  model_limits,
  resolved_at,
  refreshed_at,
  expires_at,
  last_seen_at,
  resolution_error
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
  $11, $12, $13, $14, $15, $15, $16, $15, ''
)
ON CONFLICT (token_fingerprint) DO UPDATE SET
  fingerprint_display = EXCLUDED.fingerprint_display,
  new_api_token_id = EXCLUDED.new_api_token_id,
  token_name_raw = EXCLUDED.token_name_raw,
  username = EXCLUDED.username,
  token_status = EXCLUDED.token_status,
  token_group = EXCLUDED.token_group,
  token_expired_time = EXCLUDED.token_expired_time,
  token_accessed_time = EXCLUDED.token_accessed_time,
  remain_quota = EXCLUDED.remain_quota,
  used_quota = EXCLUDED.used_quota,
  unlimited_quota = EXCLUDED.unlimited_quota,
  model_limits_enabled = EXCLUDED.model_limits_enabled,
  model_limits = EXCLUDED.model_limits,
  refreshed_at = EXCLUDED.refreshed_at,
  expires_at = EXCLUDED.expires_at,
  last_seen_at = EXCLUDED.last_seen_at,
  resolution_error = EXCLUDED.resolution_error`
}
```

- [ ] **Step 4: Update tests**

In `internal/identity/postgres_cache_test.go`, replace all occurrences of `EmployeeNo` with `Username`, and `"E10001"` (employee number values) with `"alice"` (username values). Specifically:

- `TestPostgresCacheGetReadsFreshSnapshot`: Change the `E10001` value in fake row (index 3, the 4th value) from `"E10001"` to `"alice"`, and change assertion `got.EmployeeNo != "E10001"` to `got.Username != "alice"`.
- `TestPostgresCacheGetReturnsMissForExpiredRow`: Change the 4th fake row value from `"E10001"` to `"alice"`.
- `TestPostgresCacheGetUpdatesLastSeenAtBestEffortOnHit`: Same changes.
- `TestPostgresCacheSetUpsertsSnapshot`: Change `EmployeeNo: "E10001"` to `Username: "alice"`.
- `TestChainCacheReadsSecondCacheAndBackfillsFirst`: Change `EmployeeNo: "E10001"` to `Username: "alice"` and assertion accordingly.
- `TestChainCacheContinuesAfterFirstCacheErrorAndBackfills`: Same.
- `TestChainCacheSkipsTypedNilCaches`: Same.
- `TestPostgresCacheGetSQLTreatsNullExpiresAtAsExpired`: No change needed (no EmployeeNo reference).
- All SQL assertion strings that contain `employee_no` should be changed to `username`.

- [ ] **Step 5: Run tests**

Run: `cd /Users/roy/codes/new-api-gateway && go test ./internal/identity/ -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/identity/postgres_cache.go internal/identity/postgres_cache_test.go
git commit -m "feat(identity): update PostgresCache for username field rename"
```

---

### Task 7: Downstream Go Updates — Traces, Jobs, Gateway

**Files:**
- Modify: `internal/traces/model.go`
- Modify: `internal/traces/repository.go`
- Modify: `internal/traces/repository_test.go`
- Modify: `internal/jobs/jobs.go`
- Modify: `internal/jobs/jobs_test.go`
- Modify: `internal/gateway/proxy.go`

- [ ] **Step 1: Update traces model**

In `internal/traces/model.go`, rename `EmployeeNoSnapshot string` to `UsernameSnapshot string`:

```go
UsernameSnapshot string
```

- [ ] **Step 2: Update traces repository**

In `internal/traces/repository.go`:

In the `InsertTrace` SQL, rename `employee_no_snapshot` to `username_snapshot`:

Change the column list from `employee_no_snapshot` to `username_snapshot`.

Change the argument from `trace.EmployeeNoSnapshot` to `trace.UsernameSnapshot`.

- [ ] **Step 3: Update traces repository tests**

In `internal/traces/repository_test.go`, replace `EmployeeNoSnapshot` with `UsernameSnapshot` and update any test values from employee numbers (like `"E123"`) to usernames (like `"alice"`).

- [ ] **Step 4: Update jobs model**

In `internal/jobs/jobs.go`:

In `TraceCapturedInput` struct, rename `EmployeeNo string` to `Username string` and update the JSON tag from `json:"employee_no"` to `json:"username"`.

In `TraceCapturedJob` struct, rename `EmployeeNo string` to `Username string`.

In `NewTraceCaptured()`, change `EmployeeNo: input.EmployeeNo` to `Username: input.Username`.

- [ ] **Step 5: Update jobs tests**

In `internal/jobs/jobs_test.go`, replace all `EmployeeNo` references with `Username` and update JSON field assertions from `"employee_no"` to `"username"`.

- [ ] **Step 6: Update gateway proxy**

In `internal/gateway/proxy.go`:

In `insertTrace`, change `EmployeeNoSnapshot: record.snapshot.EmployeeNo` to `UsernameSnapshot: record.snapshot.Username`.

In `emitCoverageAlert` and `insertTrace` and the job publishing section, change `EmployeeNo: record.snapshot.EmployeeNo` to `Username: record.snapshot.Username` in the `TraceCapturedInput` struct literal.

- [ ] **Step 7: Run tests**

Run: `cd /Users/roy/codes/new-api-gateway && go test ./internal/traces/ ./internal/jobs/ ./internal/gateway/ -v`

Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/traces/ internal/jobs/ internal/gateway/
git commit -m "refactor: rename EmployeeNo to Username in traces, jobs, gateway"
```

---

### Task 8: Wire NewAPILookup in main.go

**Files:**
- Modify: `cmd/audit-gateway/main.go`

- [ ] **Step 1: Add new-api database pool initialization**

In `run()`, after creating the main `pool`, add:

```go
newAPIPool, err := pgxpool.New(ctx, cfg.NewAPIPostgresDSN)
if err != nil {
    return fmt.Errorf("new-api database: %w", err)
}
defer newAPIPool.Close()
```

Update the function signature calls to pass `newAPIPool`:
- Change `buildHTTPHandler(cfg, pool, redisClient, logger)` to `buildHTTPHandler(cfg, pool, newAPIPool, redisClient, logger)`

- [ ] **Step 2: Update buildHandler to use NewAPILookup**

Update `buildHandler` signature to accept `newAPIPool`:

```go
func buildHandler(cfg config.Config, pool *pgxpool.Pool, newAPIPool *pgxpool.Pool, redisClient *redis.Client, logger *log.Logger) gateway.Handler {
```

Replace `identity.PostgresTokenLookup{Pool: pool}` with `identity.NewAPILookup{Pool: newAPIPool}`.

Remove `EmployeeNoPattern: cfg.EmployeeNoPattern` from the `identity.Resolver` literal.

- [ ] **Step 3: Update buildHTTPHandler signature**

Update `buildHTTPHandler` signature to accept `newAPIPool`:

```go
func buildHTTPHandler(cfg config.Config, pool *pgxpool.Pool, newAPIPool *pgxpool.Pool, redisClient *redis.Client, logger *log.Logger) http.Handler {
```

Update the internal call from `buildHandler(cfg, pool, redisClient, logger)` to `buildHandler(cfg, pool, newAPIPool, redisClient, logger)`.

- [ ] **Step 4: Remove unused import**

Remove `"regexp"` from imports if it's no longer used (it was used for `bearerTokenPattern` at the bottom of the file, so it stays). Check: `regexp` is still used by `bearerTokenPattern` — keep it.

Remove `"fmt"` if not used elsewhere — it IS used by `fmt.Errorf` in the new code. Keep it.

- [ ] **Step 5: Run tests / verify compilation**

Run: `cd /Users/roy/codes/new-api-gateway && go build ./cmd/audit-gateway/`

Expected: Success — no compilation errors.

- [ ] **Step 6: Commit**

```bash
git add cmd/audit-gateway/main.go
git commit -m "feat(main): wire NewAPILookup with new-api database pool"
```

---

### Task 9: Database Migration — Rename employee_no Columns

**Files:**
- Create: `migrations/0012_rename_employee_no_to_username.sql`

- [ ] **Step 1: Check the latest migration number**

Run: `ls /Users/roy/codes/new-api-gateway/migrations/ | sort | tail -5`

Expected: Some numbered migrations. Create the next number (e.g., if 0011 is latest, create 0012).

- [ ] **Step 2: Write the migration**

Create `migrations/0012_rename_employee_no_to_username.sql`:

```sql
-- Rename employee_no columns to username across all tables and indexes.

-- traces table
ALTER TABLE traces RENAME COLUMN employee_no_snapshot TO username_snapshot;

-- token_identity_cache table
ALTER TABLE token_identity_cache RENAME COLUMN employee_no TO username;

-- Indexes
DROP INDEX IF EXISTS idx_traces_employee_created;
CREATE INDEX idx_traces_username_created ON traces(username_snapshot, created_at);

DROP INDEX IF EXISTS idx_token_identity_employee;
CREATE INDEX idx_token_identity_username ON token_identity_cache(username);
```

- [ ] **Step 3: Commit**

```bash
git add migrations/0012_rename_employee_no_to_username.sql
git commit -m "migrate: rename employee_no columns to username"
```

---

### Task 10: Python Worker Updates

**Files:**
- Modify: `workers/analysis_worker/models.py`
- Modify: `workers/analysis_worker/contract_example.json`
- Modify: `workers/analysis_worker/rules.py`
- Modify: `workers/analysis_worker/repository.py`
- Modify: `workers/analysis_worker/main.py`
- Modify: `workers/analysis_worker/tests/test_models.py`
- Modify: `workers/analysis_worker/tests/test_normalizers.py`
- Modify: `workers/analysis_worker/tests/test_pipeline.py`
- Modify: `workers/analysis_worker/tests/test_repository.py`
- Modify: `workers/analysis_worker/tests/test_rules.py`
- Modify: `workers/analysis_worker/tests/test_work_relevance.py`

- [ ] **Step 1: Update models.py**

In `workers/analysis_worker/models.py`, perform a global rename of `employee_no` → `username` across all dataclass fields, function parameters, and variable names.

Specific changes in `TraceCapturedInput`:
```python
employee_no: str  →  username: str
```

In `UsageBucket`:
```python
employee_no: str  →  username: str
```

In `UsageAnomaly`:
```python
employee_no: str  →  username: str
```

In `anomaly_id()`:
```python
def anomaly_id(rule_key: str, trace_id: str, username: str) -> str:
    return f"anom_{rule_key}_{stable_suffix(rule_key, trace_id, username)}"
```

- [ ] **Step 2: Update contract_example.json**

```json
"employee_no": "E12345",  →  "username": "alice",
```

- [ ] **Step 3: Update rules.py**

Replace all `employee_no` references with `username`. The field is accessed in rule matching logic.

- [ ] **Step 4: Update repository.py**

Replace `employee_no` with `username` in all SQL queries and Python code. Specifically, any INSERT/SELECT statements that reference `employee_no` columns should reference `username` columns.

- [ ] **Step 5: Update main.py**

Replace `employee_no` with `username` in field access from incoming job payloads and anywhere else it appears.

- [ ] **Step 6: Update all test files**

In every file under `workers/analysis_worker/tests/`, replace:
- `employee_no` → `username`
- `"E12345"` or similar employee number test values → `"alice"` or similar username test values
- `"invalid_employee_no"` status → remove or update (this status no longer exists)
- `"missing_employee_no"` status → remove or update

- [ ] **Step 7: Run Python tests**

Run: `cd /Users/roy/codes/new-api-gateway/workers/analysis_worker && uv run pytest -q`

Expected: All tests pass.

- [ ] **Step 8: Commit**

```bash
git add workers/analysis_worker/
git commit -m "refactor(worker): rename employee_no to username throughout analysis worker"
```

---

### Task 11: Final Verification and Cleanup

- [ ] **Step 1: Run full Go test suite**

Run: `cd /Users/roy/codes/new-api-gateway && make test`

Expected: All tests pass.

- [ ] **Step 2: Run Python test suite**

Run: `cd /Users/roy/codes/new-api-gateway/workers/analysis_worker && uv run pytest -q`

Expected: All tests pass.

- [ ] **Step 3: Verify no remaining employee references**

Run: `cd /Users/roy/codes/new-api-gateway && rg -i 'employee' --type go --type py -l`

Expected: No results (or only comments/docs that are acceptable).

Run: `cd /Users/roy/codes/new-api-gateway && rg 'EmployeeNo' --type go -l`

Expected: No results.

- [ ] **Step 4: Verify build succeeds**

Run: `cd /Users/roy/codes/new-api-gateway && go build ./...`

Expected: Success.

- [ ] **Step 5: Commit any remaining cleanup**

If any remaining references were missed:

```bash
git add -A
git commit -m "chore: final cleanup of employee_no references"
```
