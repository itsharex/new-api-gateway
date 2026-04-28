# Admin API RBAC and Audit Logs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the first admin backend slice: local account login, signed sessions, action-level RBAC, audit action logs, review decisions, trace/anomaly/coverage query APIs, raw evidence access auditing, and API key lookup without storing plaintext keys.

**Architecture:** Keep the existing proxy gateway unchanged for model traffic and mount an `/admin/api/*` HTTP surface beside it in the same Go binary. Admin handlers use focused repositories over PostgreSQL, signed HTTP-only session cookies for MVP authentication, bcrypt password verification, and explicit permission checks before raw evidence or API key lookup operations.

**Tech Stack:** Go 1.26, standard `net/http`, PostgreSQL via existing `pgx` patterns, filesystem evidence store, HMAC-SHA256 fingerprints, bcrypt from `golang.org/x/crypto/bcrypt`, existing gateway config and tests.

---

## Scope Check

The approved design includes Admin API, Web UI, RBAC, audit logs, raw evidence access, API key lookup, review decisions, dashboards, SSO/OIDC, and operational hardening. This plan implements only the backend foundation needed for an MVP auditor workflow:

- local account/session login;
- role-to-permission checks for `viewer`, `auditor`, `raw_access`, and `admin`;
- audit logs for login/logout, raw evidence access, API key lookup, review decisions, and settings-like mutations introduced here;
- query APIs for traces, anomalies, coverage alerts, context catalog rows, and usage aggregates;
- API key lookup by deriving the existing HMAC fingerprint and discarding the submitted key immediately;
- review decision writes for traces, anomalies, and coverage alerts.

This plan does not implement the Web UI, SSO/OIDC, CSRF origin allowlists, rate limiting, route registry editing, user management screens, or object-storage backends beyond the existing filesystem evidence store. Those should be separate plans after these APIs exist and are testable.

## Existing Context

- Existing module path: `github.com/your-company/new-api-gateway`.
- Current process entrypoint: `cmd/audit-gateway/main.go`.
- Existing config loader: `internal/config/config.go`.
- Existing fingerprint code: `internal/fingerprint/fingerprint.go`.
- Existing API key canonicalization behavior: `internal/authkeys/extractor.go`.
- Existing evidence store interface: `internal/evidence/store.go`.
- Existing trace repository style: `internal/traces/repository.go` with fake `Exec` tests.
- Existing anomaly/coverage tables: `migrations/0004_worker_anomaly_coverage.sql`.
- Existing work relevance/context catalog table: `migrations/0005_context_catalog_work_relevance.sql`.

## File Structure

- Create `migrations/0006_admin_rbac_audit_logs.sql`: `audit_users`, `audit_sessions`, `audit_action_logs`, `review_decisions`, indexes, and role constraint.
- Modify `go.mod`: add `golang.org/x/crypto`.
- Modify `internal/config/config.go`: add admin session secret, cookie name, and cookie secure flag.
- Modify `internal/config/config_test.go`: assert admin config validation.
- Create `internal/admin/models.go`: admin user, session, action log, review decision, filter, summary, and API response structs.
- Create `internal/admin/passwords.go`: bcrypt hash/check helpers used by repository tests and bootstrap scripts.
- Create `internal/admin/permissions.go`: role permission mapping and permission checks.
- Create `internal/admin/repository.go`: PostgreSQL repository for users, sessions, logs, reviews, lookup summaries, traces, anomalies, coverage alerts, usage aggregates, and context catalog rows.
- Create `internal/admin/repository_test.go`: fake-execer/queryer tests for SQL shape and secret omission.
- Create `internal/admin/auth.go`: login service, signed session cookies, middleware, and request principal helpers.
- Create `internal/admin/auth_test.go`: bcrypt verification, signed cookie tamper rejection, and permission checks.
- Create `internal/admin/handlers.go`: admin HTTP routes and JSON handlers.
- Create `internal/admin/handlers_test.go`: httptest coverage for login/me/logout, RBAC denial, raw access audit logging, review writes, and API key lookup privacy.
- Modify `cmd/audit-gateway/main.go`: mount admin routes together with the gateway proxy routes.
- Modify `cmd/audit-gateway/main_test.go`: assert admin handler wiring and proxy fallback.
- Modify `docs/development.md`: document admin environment variables, seed-user SQL, and curl smoke checks.

---

### Task 1: Admin Schema Migration

**Files:**
- Create: `migrations/0006_admin_rbac_audit_logs.sql`

- [ ] **Step 1: Write the migration**

Create `migrations/0006_admin_rbac_audit_logs.sql`:

```sql
CREATE TABLE IF NOT EXISTS audit_users (
    id BIGSERIAL PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    auth_provider TEXT NOT NULL DEFAULT 'local',
    external_subject TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (role IN ('viewer', 'auditor', 'raw_access', 'admin')),
    CHECK (status IN ('active', 'disabled'))
);

CREATE INDEX IF NOT EXISTS idx_audit_users_status_role
    ON audit_users(status, role);

CREATE TABLE IF NOT EXISTS audit_sessions (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES audit_users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_sessions_user_expires
    ON audit_sessions(user_id, expires_at DESC);

CREATE TABLE IF NOT EXISTS audit_action_logs (
    id BIGSERIAL PRIMARY KEY,
    actor_user_id BIGINT NOT NULL DEFAULT 0,
    actor_username TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    token_fingerprint TEXT NOT NULL DEFAULT '',
    fingerprint_display TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    ip_hash TEXT NOT NULL DEFAULT '',
    user_agent_hash TEXT NOT NULL DEFAULT '',
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_action_logs_actor_created
    ON audit_action_logs(actor_user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_action_logs_action_created
    ON audit_action_logs(action, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_action_logs_trace
    ON audit_action_logs(trace_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_action_logs_token
    ON audit_action_logs(token_fingerprint, created_at DESC);

CREATE TABLE IF NOT EXISTS review_decisions (
    id BIGSERIAL PRIMARY KEY,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL,
    decision TEXT NOT NULL,
    reviewer_id BIGINT NOT NULL,
    reviewer_username TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (target_type IN ('trace', 'anomaly', 'coverage_alert')),
    CHECK (decision IN ('acknowledge', 'dismiss', 'confirm', 'mark_personal_use', 'mark_abuse', 'needs_normalizer', 'mark_fixed', 'ignore_for_now'))
);

CREATE INDEX IF NOT EXISTS idx_review_decisions_target_created
    ON review_decisions(target_type, target_id, created_at DESC);
```

- [ ] **Step 2: Verify migration text has expected objects**

Run:

```bash
rg -n "CREATE TABLE IF NOT EXISTS (audit_users|audit_sessions|audit_action_logs|review_decisions)" migrations/0006_admin_rbac_audit_logs.sql
```

Expected: four matches, one for each table.

- [ ] **Step 3: Verify all migrations still apply in the existing e2e path**

Run:

```bash
E2E_DB=audit_gateway_plan_0006 ./scripts/e2e_worker_anomaly_coverage.sh
```

Expected: PASS. The script recreates the database and applies every migration, including `0006_admin_rbac_audit_logs.sql`.

- [ ] **Step 4: Commit**

```bash
git add migrations/0006_admin_rbac_audit_logs.sql
git commit -m "feat: add admin rbac audit schema"
```

---

### Task 2: Admin Config and Password Helpers

**Files:**
- Modify: `go.mod`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Create: `internal/admin/passwords.go`
- Create: `internal/admin/passwords_test.go`

- [ ] **Step 1: Add bcrypt dependency**

Run:

```bash
go get golang.org/x/crypto/bcrypt
```

Expected: `go.mod` includes `golang.org/x/crypto`.

- [ ] **Step 2: Write config tests first**

Append to `internal/config/config_test.go`:

```go
func TestLoadFromEnvLoadsAdminSettings(t *testing.T) {
	setValidEnv(t)
	t.Setenv("ADMIN_SESSION_SECRET", "admin-session-secret-0123456789abcdef")
	t.Setenv("ADMIN_COOKIE_NAME", "audit_admin_session")
	t.Setenv("ADMIN_COOKIE_SECURE", "true")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if cfg.AdminSessionSecret != "admin-session-secret-0123456789abcdef" {
		t.Fatalf("AdminSessionSecret = %q", cfg.AdminSessionSecret)
	}
	if cfg.AdminCookieName != "audit_admin_session" {
		t.Fatalf("AdminCookieName = %q", cfg.AdminCookieName)
	}
	if !cfg.AdminCookieSecure {
		t.Fatal("AdminCookieSecure = false, want true")
	}
}

func TestLoadFromEnvRejectsShortAdminSessionSecret(t *testing.T) {
	setValidEnv(t)
	t.Setenv("ADMIN_SESSION_SECRET", "short")

	_, err := LoadFromEnv()
	assertErrorContains(t, err, "ADMIN_SESSION_SECRET must be at least 32 characters")
}
```

- [ ] **Step 3: Run config tests and verify failure**

Run:

```bash
go test ./internal/config -run 'TestLoadFromEnvLoadsAdminSettings|TestLoadFromEnvRejectsShortAdminSessionSecret' -v
```

Expected: FAIL because `Config` has no admin session fields.

- [ ] **Step 4: Extend config**

Modify `internal/config/config.go`:

```go
type Config struct {
	ListenAddr         string
	NewAPIBaseURL      string
	AuditHMACSecret    string
	EvidenceStorageDir string
	PostgresDSN        string
	RedisAddr          string
	EmployeeNoPattern  *regexp.Regexp
	AdminSessionSecret string
	AdminCookieName    string
	AdminCookieSecure  bool
}
```

Inside `LoadFromEnv`, after `auditHMACSecret` validation input is loaded, add:

```go
adminSessionSecret, err := getenvDefault("ADMIN_SESSION_SECRET", auditHMACSecret)
if err != nil {
	return Config{}, err
}
if len(adminSessionSecret) < 32 {
	return Config{}, errors.New("ADMIN_SESSION_SECRET must be at least 32 characters")
}

adminCookieName, err := getenvDefault("ADMIN_COOKIE_NAME", "audit_admin_session")
if err != nil {
	return Config{}, err
}

adminCookieSecureRaw, err := getenvDefault("ADMIN_COOKIE_SECURE", "false")
if err != nil {
	return Config{}, err
}
adminCookieSecure, err := strconv.ParseBool(adminCookieSecureRaw)
if err != nil {
	return Config{}, fmt.Errorf("invalid ADMIN_COOKIE_SECURE: must be true or false")
}
```

Add these fields to the `cfg := Config{...}` literal:

```go
AdminSessionSecret: adminSessionSecret,
AdminCookieName:    adminCookieName,
AdminCookieSecure:  adminCookieSecure,
```

- [ ] **Step 5: Write password helper tests**

Create `internal/admin/passwords_test.go`:

```go
package admin

import "testing"

func TestHashPasswordAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("hash must not equal plaintext password")
	}
	if err := CheckPassword(hash, "correct horse battery staple"); err != nil {
		t.Fatalf("CheckPassword returned error for correct password: %v", err)
	}
	if err := CheckPassword(hash, "wrong password"); err == nil {
		t.Fatal("CheckPassword accepted wrong password")
	}
}
```

- [ ] **Step 6: Run password tests and verify failure**

Run:

```bash
go test ./internal/admin -run TestHashPasswordAndCheckPassword -v
```

Expected: FAIL because package `internal/admin` does not exist.

- [ ] **Step 7: Add password helpers**

Create `internal/admin/passwords.go`:

```go
package admin

import "golang.org/x/crypto/bcrypt"

func HashPassword(plaintext string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func CheckPassword(hash, plaintext string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
}
```

- [ ] **Step 8: Run tests and tidy**

Run:

```bash
go test ./internal/config ./internal/admin -v
go mod tidy
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/config/config.go internal/config/config_test.go internal/admin/passwords.go internal/admin/passwords_test.go
git commit -m "feat: add admin auth config and password helpers"
```

---

### Task 3: Admin Models, Permissions, and Repository

**Files:**
- Create: `internal/admin/models.go`
- Create: `internal/admin/permissions.go`
- Create: `internal/admin/repository.go`
- Create: `internal/admin/permissions_test.go`
- Create: `internal/admin/repository_test.go`

- [ ] **Step 1: Write permission tests first**

Create `internal/admin/permissions_test.go`:

```go
package admin

import "testing"

func TestRolePermissions(t *testing.T) {
	tests := []struct {
		role       Role
		permission Permission
		want       bool
	}{
		{RoleViewer, PermissionViewAggregates, true},
		{RoleViewer, PermissionViewNormalizedTraces, false},
		{RoleAuditor, PermissionViewNormalizedTraces, true},
		{RoleAuditor, PermissionReview, true},
		{RoleAuditor, PermissionRawEvidence, false},
		{RoleRawAccess, PermissionRawEvidence, true},
		{RoleRawAccess, PermissionAPIKeyLookup, true},
		{RoleAdmin, PermissionManageUsers, true},
		{RoleAdmin, PermissionAPIKeyLookup, true},
	}

	for _, tt := range tests {
		if got := tt.role.Allows(tt.permission); got != tt.want {
			t.Fatalf("role %q Allows(%q) = %v, want %v", tt.role, tt.permission, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run permission tests and verify failure**

Run:

```bash
go test ./internal/admin -run TestRolePermissions -v
```

Expected: FAIL because role types are not defined.

- [ ] **Step 3: Add models and permissions**

Create `internal/admin/models.go`:

```go
package admin

import "time"

type Role string

const (
	RoleViewer    Role = "viewer"
	RoleAuditor   Role = "auditor"
	RoleRawAccess Role = "raw_access"
	RoleAdmin     Role = "admin"
)

type Permission string

const (
	PermissionViewAggregates       Permission = "view_aggregates"
	PermissionViewNormalizedTraces Permission = "view_normalized_traces"
	PermissionReview               Permission = "review"
	PermissionRawEvidence          Permission = "raw_evidence"
	PermissionAPIKeyLookup         Permission = "api_key_lookup"
	PermissionManageUsers          Permission = "manage_users"
)

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	DisplayName  string    `json:"display_name"`
	Email        string    `json:"email"`
	Role         Role      `json:"role"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Session struct {
	SessionID string
	UserID    int64
	ExpiresAt time.Time
}

type Principal struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        Role   `json:"role"`
}

type AuditActionLog struct {
	ActorUserID       int64
	ActorUsername     string
	Action            string
	TargetType        string
	TargetID          string
	TokenFingerprint  string
	FingerprintDisplay string
	TraceID           string
	IPHash            string
	UserAgentHash     string
	MetadataJSON      string
	CreatedAt         time.Time
}

type ReviewDecision struct {
	TargetType       string    `json:"target_type"`
	TargetID         string    `json:"target_id"`
	Decision         string    `json:"decision"`
	ReviewerID       int64     `json:"reviewer_id"`
	ReviewerUsername string    `json:"reviewer_username"`
	Note             string    `json:"note"`
	CreatedAt        time.Time `json:"created_at"`
}

type TraceFilter struct {
	TraceID          string
	EmployeeNo       string
	TokenFingerprint string
	RoutePattern     string
	Model            string
	StatusCode       int
	Limit            int
}

type TraceSummary struct {
	TraceID            string `json:"trace_id"`
	Method             string `json:"method"`
	Path               string `json:"path"`
	RoutePattern       string `json:"route_pattern"`
	ProtocolFamily     string `json:"protocol_family"`
	StatusCode         int    `json:"status_code"`
	EmployeeNo         string `json:"employee_no"`
	FingerprintDisplay string `json:"fingerprint_display"`
	ModelRequested     string `json:"model_requested"`
	UsageTotalTokens   int    `json:"usage_total_tokens"`
	CreatedAt          string `json:"created_at"`
}

type LookupSummary struct {
	FingerprintDisplay string         `json:"fingerprint_display"`
	TokenFingerprint   string         `json:"token_fingerprint"`
	EmployeeNo         string         `json:"employee_no"`
	NewAPITokenID      int            `json:"new_api_token_id"`
	TokenName          string         `json:"token_name"`
	TokenStatus        int            `json:"token_status"`
	RecentTraces       []TraceSummary `json:"recent_traces"`
	OpenAnomalyCount   int            `json:"open_anomaly_count"`
}
```

Create `internal/admin/permissions.go`:

```go
package admin

var rolePermissions = map[Role]map[Permission]bool{
	RoleViewer: {
		PermissionViewAggregates: true,
	},
	RoleAuditor: {
		PermissionViewAggregates:       true,
		PermissionViewNormalizedTraces: true,
		PermissionReview:               true,
	},
	RoleRawAccess: {
		PermissionViewAggregates:       true,
		PermissionViewNormalizedTraces: true,
		PermissionReview:               true,
		PermissionRawEvidence:          true,
		PermissionAPIKeyLookup:         true,
	},
	RoleAdmin: {
		PermissionViewAggregates:       true,
		PermissionViewNormalizedTraces: true,
		PermissionReview:               true,
		PermissionRawEvidence:          true,
		PermissionAPIKeyLookup:         true,
		PermissionManageUsers:          true,
	},
}

func (r Role) Allows(permission Permission) bool {
	return rolePermissions[r][permission]
}
```

- [ ] **Step 4: Write repository tests**

Create `internal/admin/repository_test.go`:

```go
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

func (r *fakeRows) Close() {}
func (r *fakeRows) Err() error { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool { return false }
func (r *fakeRows) Scan(dest ...any) error { return pgx.ErrNoRows }
func (r *fakeRows) Values() ([]any, error) { return nil, nil }
func (r *fakeRows) RawValues() [][]byte { return nil }
func (r *fakeRows) Conn() *pgx.Conn { return nil }

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
```

- [ ] **Step 5: Run repository tests and verify failure**

Run:

```bash
go test ./internal/admin -run 'TestRepository' -v
```

Expected: FAIL because repository methods are missing.

- [ ] **Step 6: Add repository**

Create `internal/admin/repository.go`:

```go
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
LIMIT 1`, sessionID, now).Scan(&principal.UserID, &principal.Username, &principal.DisplayName, &principal.Role)
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
```

- [ ] **Step 7: Run admin tests**

Run:

```bash
go test ./internal/admin -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/admin/models.go internal/admin/permissions.go internal/admin/repository.go internal/admin/permissions_test.go internal/admin/repository_test.go
git commit -m "feat: add admin repository and permissions"
```

---

### Task 4: Session Auth and Login Handlers

**Files:**
- Create: `internal/admin/auth.go`
- Create: `internal/admin/auth_test.go`
- Create: `internal/admin/handlers.go`
- Create: `internal/admin/handlers_test.go`

- [ ] **Step 1: Write auth tests first**

Create `internal/admin/auth_test.go`:

```go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSignAndVerifySessionCookie(t *testing.T) {
	auth := Auth{SessionSecret: "session-secret-0123456789abcdef", CookieName: "audit_admin_session"}
	cookie := auth.sessionCookie("sess_123", time.Unix(2000, 0).UTC())

	sessionID, ok := auth.verifyCookie(cookie.Value)
	if !ok {
		t.Fatal("verifyCookie rejected signed cookie")
	}
	if sessionID != "sess_123" {
		t.Fatalf("sessionID = %q", sessionID)
	}
	if _, ok := auth.verifyCookie(cookie.Value + "tampered"); ok {
		t.Fatal("verifyCookie accepted tampered cookie")
	}
}

func TestRequirePermissionRejectsMissingPermission(t *testing.T) {
	auth := Auth{SessionSecret: "session-secret-0123456789abcdef", CookieName: "audit_admin_session"}
	handlerCalled := false
	handler := auth.Require(PermissionRawEvidence, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	}))
	req := httptest.NewRequest(http.MethodGet, "/admin/api/raw", nil)
	req = req.WithContext(WithPrincipal(req.Context(), Principal{UserID: 1, Username: "viewer", Role: RoleViewer}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if handlerCalled {
		t.Fatal("handler was called despite missing permission")
	}
}
```

- [ ] **Step 2: Run auth tests and verify failure**

Run:

```bash
go test ./internal/admin -run 'TestSignAndVerifySessionCookie|TestRequirePermissionRejectsMissingPermission' -v
```

Expected: FAIL because `Auth` is missing.

- [ ] **Step 3: Add auth middleware**

Create `internal/admin/auth.go`:

```go
package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

type contextKey string

const principalContextKey contextKey = "admin_principal"

type Auth struct {
	Repo          Repository
	SessionSecret string
	CookieName    string
	CookieSecure  bool
	Now           func() time.Time
}

func (a Auth) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

func NewSessionID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sess_" + hex.EncodeToString(b[:]), nil
}

func (a Auth) sessionCookie(sessionID string, expiresAt time.Time) *http.Cookie {
	value := a.signSessionID(sessionID)
	return &http.Cookie{
		Name:     a.CookieName,
		Value:    value,
		Path:     "/admin",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   a.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a Auth) clearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     a.CookieName,
		Value:    "",
		Path:     "/admin",
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a Auth) signSessionID(sessionID string) string {
	mac := hmac.New(sha256.New, []byte(a.SessionSecret))
	mac.Write([]byte(sessionID))
	signature := hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(sessionID + "." + signature))
}

func (a Auth) verifyCookie(value string) (string, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(decoded), ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	sessionID := parts[0]
	expected := a.signSessionID(sessionID)
	return sessionID, hmac.Equal([]byte(expected), []byte(value))
}

func (a Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(a.CookieName)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sessionID, ok := a.verifyCookie(cookie.Value)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		principal, err := a.Repo.PrincipalBySession(r.Context(), sessionID, a.now())
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

func (a Auth) Require(permission Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !principal.Role.Allows(permission) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey).(Principal)
	return principal, ok
}
```

- [ ] **Step 4: Write handler tests first**

Create `internal/admin/handlers_test.go`:

```go
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginMeLogoutFlow(t *testing.T) {
	passwordHash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	repo := &memoryAdminRepo{
		user: User{ID: 1, Username: "alice", PasswordHash: passwordHash, DisplayName: "Alice", Role: RoleAuditor, Status: "active"},
	}
	auth := Auth{Repo: NewRepository(repo), SessionSecret: "session-secret-0123456789abcdef", CookieName: "audit_admin_session", Now: func() time.Time {
		return time.Unix(1000, 0).UTC()
	}}
	handler := NewHandler(HandlerConfig{Repo: NewRepository(repo), Auth: auth})

	loginBody := bytes.NewBufferString(`{"username":"alice","password":"secret-password"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/admin/api/login", loginBody)
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "audit_admin_session" {
		t.Fatalf("cookies = %#v", cookies)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	meReq.AddCookie(cookies[0])
	meRec := httptest.NewRecorder()
	handler.ServeHTTP(meRec, meReq)

	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", meRec.Code, meRec.Body.String())
	}
	var body struct {
		User Principal `json:"user"`
	}
	if err := json.Unmarshal(meRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode me body: %v", err)
	}
	if body.User.Username != "alice" || body.User.Role != RoleAuditor {
		t.Fatalf("me body = %#v", body)
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/admin/api/logout", nil)
	logoutReq.AddCookie(cookies[0])
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", logoutRec.Code)
	}
	if repo.revokedSessionID == "" {
		t.Fatal("session was not revoked")
	}
}

type memoryAdminRepo struct {
	user             User
	session          Session
	revokedSessionID string
}

func (m *memoryAdminRepo) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (m *memoryAdminRepo) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return &fakeRows{}, nil
}

func (m *memoryAdminRepo) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return memoryRow{repo: m, sql: sql, args: args}
}
```

Add these imports to `internal/admin/handlers_test.go` after the first test compile failure shows the missing pgx imports:

```go
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
```

Add this fake row implementation in the same file:

```go
type memoryRow struct {
	repo *memoryAdminRepo
	sql  string
	args []any
}

func (r memoryRow) Scan(dest ...any) error {
	if strings.Contains(r.sql, "FROM audit_users") {
		*(dest[0].(*int64)) = r.repo.user.ID
		*(dest[1].(*string)) = r.repo.user.Username
		*(dest[2].(*string)) = r.repo.user.PasswordHash
		*(dest[3].(*string)) = r.repo.user.DisplayName
		*(dest[4].(*string)) = r.repo.user.Email
		*(dest[5].(*Role)) = r.repo.user.Role
		*(dest[6].(*string)) = r.repo.user.Status
		*(dest[7].(*time.Time)) = time.Unix(900, 0).UTC()
		*(dest[8].(*time.Time)) = time.Unix(900, 0).UTC()
		return nil
	}
	if strings.Contains(r.sql, "FROM audit_sessions") {
		*(dest[0].(*int64)) = r.repo.user.ID
		*(dest[1].(*string)) = r.repo.user.Username
		*(dest[2].(*string)) = r.repo.user.DisplayName
		*(dest[3].(*Role)) = r.repo.user.Role
		return nil
	}
	return pgx.ErrNoRows
}
```

Also add `strings` to the standard imports.

- [ ] **Step 5: Run handler tests and verify failure**

Run:

```bash
go test ./internal/admin -run TestLoginMeLogoutFlow -v
```

Expected: FAIL because `NewHandler` and route handlers are missing.

- [ ] **Step 6: Add login/me/logout handlers**

Create `internal/admin/handlers.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"time"
)

type HandlerConfig struct {
	Repo Repository
	Auth Auth
}

type Handler struct {
	repo Repository
	auth Auth
	mux  *http.ServeMux
}

func NewHandler(cfg HandlerConfig) Handler {
	h := Handler{repo: cfg.Repo, auth: cfg.Auth, mux: http.NewServeMux()}
	h.mux.HandleFunc("POST /admin/api/login", h.login)
	h.mux.Handle("GET /admin/api/me", h.auth.Middleware(http.HandlerFunc(h.me)))
	h.mux.Handle("POST /admin/api/logout", h.auth.Middleware(http.HandlerFunc(h.logout)))
	return h
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h Handler) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	user, err := h.repo.FindActiveUserByUsername(r.Context(), input.Username)
	if err != nil || CheckPassword(user.PasswordHash, input.Password) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	sessionID, err := NewSessionID()
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	expiresAt := h.auth.now().Add(12 * time.Hour)
	if err := h.repo.CreateSession(r.Context(), Session{SessionID: sessionID, UserID: user.ID, ExpiresAt: expiresAt}); err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, h.auth.sessionCookie(sessionID, expiresAt))
	_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:   user.ID,
		ActorUsername: user.Username,
		Action:        "login",
		TargetType:    "audit_user",
		TargetID:      user.Username,
		MetadataJSON:  `{"auth_provider":"local"}`,
		CreatedAt:     h.auth.now(),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user": Principal{UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Role: user.Role},
	})
}

func (h Handler) me(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user": principal})
}

func (h Handler) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(h.auth.CookieName)
	if cookie != nil {
		if sessionID, ok := h.auth.verifyCookie(cookie.Value); ok {
			_ = h.repo.RevokeSession(r.Context(), sessionID, h.auth.now())
		}
	}
	if principal, ok := PrincipalFromContext(r.Context()); ok {
		_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
			ActorUserID:   principal.UserID,
			ActorUsername: principal.Username,
			Action:        "logout",
			TargetType:    "audit_user",
			TargetID:      principal.Username,
			CreatedAt:     h.auth.now(),
		})
	}
	http.SetCookie(w, h.auth.clearCookie())
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
```

- [ ] **Step 7: Run admin tests**

Run:

```bash
go test ./internal/admin -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/admin/auth.go internal/admin/auth_test.go internal/admin/handlers.go internal/admin/handlers_test.go
git commit -m "feat: add admin session handlers"
```

---

### Task 5: Query APIs and Review Decisions

**Files:**
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/repository.go`
- Modify: `internal/admin/repository_test.go`
- Modify: `internal/admin/handlers.go`
- Modify: `internal/admin/handlers_test.go`

- [ ] **Step 1: Add query and review handler tests**

Append to `internal/admin/handlers_test.go`:

```go
func TestViewerCannotCreateReviewDecision(t *testing.T) {
	repo := &memoryAdminRepo{
		user: User{ID: 2, Username: "viewer", PasswordHash: "$2a$10$012345678901234567890uRZMFv4I2rGgbJ5h1x3zsmYqzqzqzqzq", DisplayName: "Viewer", Role: RoleViewer, Status: "active"},
	}
	auth := Auth{Repo: NewRepository(repo), SessionSecret: "session-secret-0123456789abcdef", CookieName: "audit_admin_session", Now: func() time.Time {
		return time.Unix(1000, 0).UTC()
	}}
	handler := NewHandler(HandlerConfig{Repo: NewRepository(repo), Auth: auth})
	req := httptest.NewRequest(http.MethodPost, "/admin/api/reviews", bytes.NewBufferString(`{"target_type":"anomaly","target_id":"anom_1","decision":"acknowledge","note":"seen"}`))
	req = req.WithContext(WithPrincipal(req.Context(), Principal{UserID: 2, Username: "viewer", Role: RoleViewer}))
	rec := httptest.NewRecorder()

	handler.auth.Require(PermissionReview, http.HandlerFunc(handler.createReview)).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
```

Append to `internal/admin/repository_test.go`:

```go
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
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./internal/admin -run 'TestViewerCannotCreateReviewDecision|TestRepositoryInsertReviewDecision' -v
```

Expected: FAIL until review routes are mounted and `createReview` exists.

- [ ] **Step 3: Add list models**

Append to `internal/admin/models.go`:

```go
type AnomalySummary struct {
	AnomalyID          string `json:"anomaly_id"`
	AnomalyType        string `json:"anomaly_type"`
	Severity           string `json:"severity"`
	Status             string `json:"status"`
	EmployeeNo         string `json:"employee_no"`
	FingerprintDisplay string `json:"fingerprint_display"`
	ObservedValue      string `json:"observed_value"`
	ThresholdValue     string `json:"threshold_value"`
	Reason             string `json:"reason"`
	CreatedAt          string `json:"created_at"`
}

type CoverageAlertSummary struct {
	AlertID         string `json:"alert_id"`
	AlertCode       string `json:"alert_code"`
	Severity        string `json:"severity"`
	Status          string `json:"status"`
	Method          string `json:"method"`
	RoutePattern    string `json:"route_pattern"`
	RawPath         string `json:"raw_path"`
	ProtocolFamily  string `json:"protocol_family"`
	OccurrenceCount int64  `json:"occurrence_count"`
	Message         string `json:"message"`
	LastSeenAt      string `json:"last_seen_at"`
}
```

- [ ] **Step 4: Add repository list methods**

Append to `internal/admin/repository.go`:

```go
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
		if err := rows.Scan(&item.AnomalyID, &item.AnomalyType, &item.Severity, &item.Status, &item.EmployeeNo,
			&item.FingerprintDisplay, &item.ObservedValue, &item.ThresholdValue, &item.Reason, &item.CreatedAt); err != nil {
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
		if err := rows.Scan(&item.AlertID, &item.AlertCode, &item.Severity, &item.Status, &item.Method,
			&item.RoutePattern, &item.RawPath, &item.ProtocolFamily, &item.OccurrenceCount, &item.Message, &item.LastSeenAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
```

- [ ] **Step 5: Mount query and review handlers**

Modify `NewHandler` in `internal/admin/handlers.go` by adding:

```go
h.mux.Handle("GET /admin/api/traces", h.auth.Middleware(h.auth.Require(PermissionViewNormalizedTraces, http.HandlerFunc(h.listTraces))))
h.mux.Handle("GET /admin/api/anomalies", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listAnomalies))))
h.mux.Handle("GET /admin/api/coverage-alerts", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listCoverageAlerts))))
h.mux.Handle("POST /admin/api/reviews", h.auth.Middleware(h.auth.Require(PermissionReview, http.HandlerFunc(h.createReview))))
```

Append these handler methods:

```go
func (h Handler) listTraces(w http.ResponseWriter, r *http.Request) {
	filter := TraceFilter{
		TraceID:          r.URL.Query().Get("trace_id"),
		EmployeeNo:       r.URL.Query().Get("employee_no"),
		TokenFingerprint: r.URL.Query().Get("token_fingerprint"),
		RoutePattern:     r.URL.Query().Get("route_pattern"),
		Model:            r.URL.Query().Get("model"),
		Limit:            100,
	}
	items, err := h.repo.ListTraces(r.Context(), filter)
	if err != nil {
		http.Error(w, "failed to list traces", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": items})
}

func (h Handler) listAnomalies(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListAnomalies(r.Context(), 100)
	if err != nil {
		http.Error(w, "failed to list anomalies", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anomalies": items})
}

func (h Handler) listCoverageAlerts(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListCoverageAlerts(r.Context(), 100)
	if err != nil {
		http.Error(w, "failed to list coverage alerts", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"coverage_alerts": items})
}

func (h Handler) createReview(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	var input struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Decision   string `json:"decision"`
		Note       string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	decision := ReviewDecision{
		TargetType:       input.TargetType,
		TargetID:         input.TargetID,
		Decision:         input.Decision,
		ReviewerID:       principal.UserID,
		ReviewerUsername: principal.Username,
		Note:             input.Note,
		CreatedAt:        h.auth.now(),
	}
	if err := h.repo.InsertReviewDecision(r.Context(), decision); err != nil {
		http.Error(w, "failed to create review", http.StatusInternalServerError)
		return
	}
	_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:   principal.UserID,
		ActorUsername: principal.Username,
		Action:        "review_decision_create",
		TargetType:    input.TargetType,
		TargetID:      input.TargetID,
		MetadataJSON:  `{"decision":"` + input.Decision + `"}`,
		CreatedAt:     h.auth.now(),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"review": decision})
}
```

- [ ] **Step 6: Run admin tests**

Run:

```bash
go test ./internal/admin -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/models.go internal/admin/repository.go internal/admin/repository_test.go internal/admin/handlers.go internal/admin/handlers_test.go
git commit -m "feat: add admin query and review APIs"
```

---

### Task 6: API Key Lookup and Raw Evidence Access

**Files:**
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/repository.go`
- Modify: `internal/admin/handlers.go`
- Modify: `internal/admin/handlers_test.go`

- [ ] **Step 1: Add handler config fields**

Modify `HandlerConfig` and `Handler` in `internal/admin/handlers.go`:

```go
type HandlerConfig struct {
	Repo          Repository
	Auth          Auth
	AuditSecret   string
	EvidenceStore evidence.Store
}

type Handler struct {
	repo          Repository
	auth          Auth
	auditSecret   string
	evidenceStore evidence.Store
	mux           *http.ServeMux
}
```

Add imports:

```go
	"io"
	"strings"

	"github.com/your-company/new-api-gateway/internal/authkeys"
	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/fingerprint"
```

Update `NewHandler` to set `auditSecret` and `evidenceStore` from config.

- [ ] **Step 2: Add repository lookup methods**

Append to `internal/admin/models.go`:

```go
type EvidenceObjectSummary struct {
	TraceID     string
	ObjectType  string
	ObjectRef   string
	ContentType string
	SizeBytes   int64
	SHA256      string
}
```

Append to `internal/admin/repository.go`:

```go
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
```

- [ ] **Step 3: Add API key lookup and raw access tests**

Append to `internal/admin/handlers_test.go`:

```go
func TestAPIKeyLookupDoesNotPersistPlaintextKeyInAuditLog(t *testing.T) {
	db := &recordingAdminDB{rows: &fakeRows{}, row: fakeRow{}}
	auth := Auth{SessionSecret: "session-secret-0123456789abcdef", CookieName: "audit_admin_session", Now: func() time.Time {
		return time.Unix(1000, 0).UTC()
	}}
	handler := NewHandler(HandlerConfig{Repo: NewRepository(db), Auth: auth, AuditSecret: "0123456789abcdef0123456789abcdef"})
	req := httptest.NewRequest(http.MethodPost, "/admin/api/api-key-lookup", bytes.NewBufferString(`{"api_key":"sk-secret-plain-text"}`))
	req = req.WithContext(WithPrincipal(req.Context(), Principal{UserID: 3, Username: "raw", Role: RoleRawAccess}))
	rec := httptest.NewRecorder()

	handler.createAPIKeyLookup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	joinedArgs := strings.Join(anyStrings(db.args), " ")
	if strings.Contains(joinedArgs, "sk-secret-plain-text") {
		t.Fatalf("plaintext key leaked to audit log args: %#v", db.args)
	}
	if len(db.args) < 8 || db.args[5] == "" || db.args[6] == "" {
		t.Fatal("lookup fingerprint was not computed")
	}
}
```

- [ ] **Step 4: Run lookup test and verify failure**

Run:

```bash
go test ./internal/admin -run TestAPIKeyLookupDoesNotPersistPlaintextKeyInAuditLog -v
```

Expected: FAIL because lookup handler is missing.

- [ ] **Step 5: Mount lookup and raw handlers**

In `NewHandler`, add:

```go
h.mux.Handle("POST /admin/api/api-key-lookup", h.auth.Middleware(h.auth.Require(PermissionAPIKeyLookup, http.HandlerFunc(h.createAPIKeyLookup))))
h.mux.Handle("GET /admin/api/raw-evidence/{trace_id}/{object_type}", h.auth.Middleware(h.auth.Require(PermissionRawEvidence, http.HandlerFunc(h.getRawEvidence))))
```

Append these handler methods to `internal/admin/handlers.go`:

```go
func (h Handler) createAPIKeyLookup(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	var input struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	canonical, ok := canonicalizeLookupKey(input.APIKey)
	input.APIKey = ""
	if !ok {
		http.Error(w, "api_key is required", http.StatusBadRequest)
		return
	}
	fp := fingerprint.Compute(canonical, h.auditSecret)
	canonical = ""
	summary, err := h.repo.LookupTokenSummary(r.Context(), fp.Value, fp.Display)
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:        principal.UserID,
		ActorUsername:      principal.Username,
		Action:             "api_key_lookup",
		TargetType:         "token",
		TargetID:           fp.Display,
		TokenFingerprint:   fp.Value,
		FingerprintDisplay: fp.Display,
		MetadataJSON:       `{"plaintext_discarded":true}`,
		CreatedAt:          h.auth.now(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"lookup": summary})
}

func canonicalizeLookupKey(value string) (string, bool) {
	req, _ := http.NewRequest(http.MethodGet, "/lookup", nil)
	req.Header.Set("Authorization", "Bearer "+value)
	result, ok := authkeys.Extract(req)
	if ok {
		return result.CanonicalKey, true
	}
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "sk-")
	return value, value != ""
}

func (h Handler) getRawEvidence(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	traceID := r.PathValue("trace_id")
	objectType := r.PathValue("object_type")
	object, err := h.repo.FindRawEvidenceObject(r.Context(), traceID, objectType)
	if err != nil {
		http.Error(w, "raw evidence not found", http.StatusNotFound)
		return
	}
	if h.evidenceStore == nil {
		http.Error(w, "evidence store unavailable", http.StatusServiceUnavailable)
		return
	}
	reader, err := h.evidenceStore.Get(r.Context(), object.ObjectRef)
	if err != nil {
		http.Error(w, "failed to read raw evidence", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:   principal.UserID,
		ActorUsername: principal.Username,
		Action:        "raw_evidence_access",
		TargetType:    "raw_evidence",
		TargetID:      objectType,
		TraceID:       traceID,
		MetadataJSON:  `{"object_type":"` + objectType + `"}`,
		CreatedAt:     h.auth.now(),
	})
	if object.ContentType != "" {
		w.Header().Set("Content-Type", object.ContentType)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Audit-Evidence-SHA256", object.SHA256)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}
```

- [ ] **Step 6: Run admin tests**

Run:

```bash
go test ./internal/admin -v
```

Expected: PASS. Raw evidence route resolves the object ref from PostgreSQL, reads through the existing evidence store, streams bytes to authorized callers, and writes an audit action log.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/models.go internal/admin/repository.go internal/admin/handlers.go internal/admin/handlers_test.go
git commit -m "feat: add admin api key lookup audit"
```

---

### Task 7: Wire Admin Routes into the Gateway Binary

**Files:**
- Modify: `cmd/audit-gateway/main.go`
- Modify: `cmd/audit-gateway/main_test.go`
- Modify: `docs/development.md`

- [ ] **Step 1: Write main wiring test first**

Append to `cmd/audit-gateway/main_test.go`:

```go
func TestBuildHTTPHandlerRoutesAdminBeforeProxy(t *testing.T) {
	cfg := config.Config{
		ListenAddr:          "127.0.0.1:8080",
		NewAPIBaseURL:       "https://new-api.example.test/base",
		AuditHMACSecret:     "0123456789abcdef0123456789abcdef",
		AdminSessionSecret:  "admin-session-secret-0123456789abcdef",
		AdminCookieName:     "audit_admin_session",
		EvidenceStorageDir:  t.TempDir(),
		EmployeeNoPattern:   regexp.MustCompile(`^E[0-9]+$`),
	}

	handler := buildHTTPHandler(cfg, nil, nil, log.New(ioDiscard{}, "", 0))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusBadGateway {
		t.Fatal("admin route fell through to proxy")
	}
}
```

Add imports to `cmd/audit-gateway/main_test.go`:

```go
	"net/http/httptest"
	"strings"
```

- [ ] **Step 2: Run main test and verify failure**

Run:

```bash
go test ./cmd/audit-gateway -run TestBuildHTTPHandlerRoutesAdminBeforeProxy -v
```

Expected: FAIL because `buildHTTPHandler` does not exist.

- [ ] **Step 3: Wire combined handler**

Modify `cmd/audit-gateway/main.go` imports:

```go
	"github.com/your-company/new-api-gateway/internal/admin"
```

Change `run`:

```go
handler := buildHTTPHandler(cfg, pool, redisClient, logger)
```

Add this function:

```go
func buildHTTPHandler(cfg config.Config, pool *pgxpool.Pool, redisClient *redis.Client, logger *log.Logger) http.Handler {
	gatewayHandler := buildHandler(cfg, pool, redisClient, logger)
	adminRepo := admin.NewRepository(pool)
	adminAuth := admin.Auth{
		Repo:          adminRepo,
		SessionSecret: cfg.AdminSessionSecret,
		CookieName:    cfg.AdminCookieName,
		CookieSecure:  cfg.AdminCookieSecure,
	}
	adminHandler := admin.NewHandler(admin.HandlerConfig{
		Repo:          adminRepo,
		Auth:          adminAuth,
		AuditSecret:   cfg.AuditHMACSecret,
		EvidenceStore: evidence.NewFilesystemStore(cfg.EvidenceStorageDir),
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/admin/api/") {
			adminHandler.ServeHTTP(w, r)
			return
		}
		gatewayHandler.ServeHTTP(w, r)
	})
}
```

Add `strings` to the standard imports.

- [ ] **Step 4: Update development docs**

Append to `docs/development.md`:

```markdown
## Admin API MVP

Admin API routes live under `/admin/api/*` in the same Go process as the proxy. Required local settings:

```bash
export ADMIN_SESSION_SECRET=admin-session-secret-0123456789abcdef
export ADMIN_COOKIE_NAME=audit_admin_session
export ADMIN_COOKIE_SECURE=false
```

Create a local admin user for the password `change-me-admin-password`:

```sql
INSERT INTO audit_users (username, password_hash, display_name, email, role, status)
VALUES ('admin', '$2a$10$NJhAxMc8237jiQCEz483Oe2jF8UwU.AM22x2GQSMtro6ADmiHfs0u', 'Local Admin', 'admin@example.test', 'admin', 'active')
ON CONFLICT (username) DO NOTHING;
```

Smoke login:

```bash
curl -i -c /tmp/audit.cookies \
  -H 'content-type: application/json' \
  -d '{"username":"admin","password":"change-me-admin-password"}' \
  http://localhost:8080/admin/api/login

curl -b /tmp/audit.cookies http://localhost:8080/admin/api/me
```

API key lookup computes the same HMAC fingerprint used by the gateway and clears the submitted plaintext key before writing the audit log.
```

- [ ] **Step 5: Run full verification**

Run:

```bash
go test ./...
cd workers/analysis_worker && uv run pytest -q
```

Expected: all Go and Python tests pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/audit-gateway/main.go cmd/audit-gateway/main_test.go docs/development.md
git commit -m "feat: wire admin api into gateway"
```

---

## Self-Review

Spec coverage:

- Local login/RBAC is covered by Tasks 1-4 and wired in Task 7.
- Audit action logs are covered by Tasks 1, 3, 4, 5, and 6.
- Trace Explorer backend filters are covered by Task 5.
- Anomaly Inbox and Coverage Alerts list APIs are covered by Task 5.
- Review Decisions are covered by Tasks 1 and 5.
- API Key Lookup privacy is covered by Task 6.
- Raw Evidence permission, object-ref lookup, filesystem streaming, and audit logging are covered by Task 6.
- Web UI, SSO/OIDC, rate limits, full user-management APIs, and operational hardening are intentionally outside this backend foundation plan.

Placeholder scan:

- No unresolved placeholder wording is used.
- Each code-changing step includes concrete code blocks, exact commands, and expected outcomes.

Type consistency:

- `Role`, `Permission`, `Principal`, `Repository`, `Auth`, `HandlerConfig`, and `LookupSummary` names are consistent across tasks.
- The route paths all use `/admin/api/*`.
- The permission names match the role-permission map and handler checks.
