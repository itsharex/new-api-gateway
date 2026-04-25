# New API Gateway Core MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first independently testable slice of the audit gateway: transparent proxying, route registry, API-key fingerprinting, new-api token-name-to-employee-number resolution, raw evidence capture, trace metadata, and coverage alerts.

**Architecture:** Implement a Go service that sits in front of new-api, resolves employee identity from `tokens.name`, captures raw evidence to a filesystem object-store abstraction, writes structured metadata to PostgreSQL, and emits analysis/coverage job envelopes for Python workers. This plan intentionally focuses on the gateway core; admin UI, full worker analysis, and advanced dashboards should be planned as follow-on slices after this core is running.

**Tech Stack:** Go 1.22+, standard `net/http`, PostgreSQL via `pgxpool`, Redis via `go-redis`, filesystem-backed object storage for MVP/dev, `httptest` for proxy tests, Python 3.11 worker contract fixtures managed with `uv`.

---

## Scope Split

The approved design covers several subsystems: gateway, admin UI, Python analyzers, dashboards, RBAC, and operations. This plan implements the gateway core as the first working slice. It produces a running service that can proxy selected new-api routes, capture evidence, resolve `employee_no`, and expose enough internal structure for later admin and worker plans.

Follow-on plans should cover:

1. Admin API and Web UI.
2. Python normalization and anomaly worker implementation.
3. Dashboards, review workflow, and RBAC screens.
4. Operational deployment hardening.

## File Structure

Create these files and responsibilities:

```text
cmd/audit-gateway/main.go                         process entrypoint
internal/config/config.go                         environment config parsing and validation
internal/ids/ids.go                               trace IDs and stable display IDs
internal/authkeys/extractor.go                    API key extraction and canonicalization
internal/authkeys/extractor_test.go               key extraction tests
internal/fingerprint/fingerprint.go               HMAC fingerprint generation
internal/fingerprint/fingerprint_test.go          fingerprint tests
internal/employee/employee.go                     employee number validation and snapshots
internal/identity/resolver.go                     identity cache + new-api DB lookup orchestration
internal/identity/resolver_test.go                resolver unit tests with fake stores
internal/identity/stores.go                       cache and token lookup interfaces
internal/routes/registry.go                       route registry and match logic
internal/routes/registry_test.go                  route matching tests
internal/evidence/store.go                        evidence object interface and filesystem store
internal/evidence/store_test.go                   evidence store tests
internal/traces/model.go                          trace and raw object domain structs
internal/traces/repository.go                     PostgreSQL trace repository interface and implementation
internal/traces/repository_test.go                repository tests with sqlmock or test container
internal/gateway/proxy.go                         reverse proxy pipeline
internal/gateway/proxy_test.go                    end-to-end httptest proxy tests
internal/gateway/capture.go                       request/response capture helpers
internal/gateway/stream.go                        SSE tee capture helpers
internal/gateway/multipart.go                     multipart capture helpers
internal/alerts/coverage.go                       coverage alert model and emitter
internal/jobs/jobs.go                             analysis job envelope model and publisher interface
migrations/0001_core_schema.sql                   PostgreSQL core schema
deploy/docker-compose.yml                         local PostgreSQL/Redis/MinIO-style services
Makefile                                          build, test, run commands
.env.example                                      documented runtime configuration
```

Keep files focused. Do not mix proxying, identity resolution, evidence storage, and schema persistence in a single large file.

---

### Task 1: Project Scaffold and Configuration

**Files:**
- Create: `go.mod`
- Create: `cmd/audit-gateway/main.go`
- Create: `internal/config/config.go`
- Create: `.env.example`
- Create: `Makefile`
- Create: `deploy/docker-compose.yml`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Initialize Go module**

Run:

```bash
go mod init github.com/your-company/new-api-gateway
```

Expected: `go.mod` exists with module path `github.com/your-company/new-api-gateway`.

- [ ] **Step 2: Add required dependencies**

Run:

```bash
go get github.com/jackc/pgx/v5/pgxpool github.com/redis/go-redis/v9
```

Expected: `go.mod` and `go.sum` include pgx and go-redis.

- [ ] **Step 3: Write config test first**

Create `internal/config/config_test.go`:

```go
package config

import "testing"

func TestLoadFromEnvRequiresCoreValues(t *testing.T) {
	t.Setenv("AUDIT_GATEWAY_LISTEN_ADDR", ":18080")
	t.Setenv("NEW_API_BASE_URL", "http://127.0.0.1:3000")
	t.Setenv("AUDIT_HMAC_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("EVIDENCE_STORAGE_DIR", t.TempDir())
	t.Setenv("POSTGRES_DSN", "postgres://audit:pass@localhost:5432/audit?sslmode=disable")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("EMPLOYEE_NO_PATTERN", `^[A-Z][0-9]{5}$`)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if cfg.ListenAddr != ":18080" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.NewAPIBaseURL != "http://127.0.0.1:3000" {
		t.Fatalf("NewAPIBaseURL = %q", cfg.NewAPIBaseURL)
	}
	if cfg.EmployeeNoPattern.String() != `^[A-Z][0-9]{5}$` {
		t.Fatalf("EmployeeNoPattern = %q", cfg.EmployeeNoPattern.String())
	}
}

func TestLoadFromEnvRejectsMissingSecret(t *testing.T) {
	t.Setenv("AUDIT_GATEWAY_LISTEN_ADDR", ":18080")
	t.Setenv("NEW_API_BASE_URL", "http://127.0.0.1:3000")
	t.Setenv("EVIDENCE_STORAGE_DIR", t.TempDir())
	t.Setenv("POSTGRES_DSN", "postgres://audit:pass@localhost:5432/audit?sslmode=disable")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("EMPLOYEE_NO_PATTERN", `^[A-Z][0-9]{5}$`)

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error for missing AUDIT_HMAC_SECRET")
	}
}
```

- [ ] **Step 4: Run config test and verify failure**

Run:

```bash
go test ./internal/config -run TestLoadFromEnv -v
```

Expected: FAIL because `LoadFromEnv` is not defined.

- [ ] **Step 5: Implement config loader**

Create `internal/config/config.go`:

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
)

type Config struct {
	ListenAddr        string
	NewAPIBaseURL     string
	AuditHMACSecret   string
	EvidenceStorageDir string
	PostgresDSN       string
	RedisAddr         string
	EmployeeNoPattern *regexp.Regexp
}

func LoadFromEnv() (Config, error) {
	pattern := getenv("EMPLOYEE_NO_PATTERN", `^[A-Za-z0-9_-]{2,64}$`)
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return Config{}, fmt.Errorf("invalid EMPLOYEE_NO_PATTERN: %w", err)
	}

	cfg := Config{
		ListenAddr:         getenv("AUDIT_GATEWAY_LISTEN_ADDR", ":8080"),
		NewAPIBaseURL:      os.Getenv("NEW_API_BASE_URL"),
		AuditHMACSecret:    os.Getenv("AUDIT_HMAC_SECRET"),
		EvidenceStorageDir: os.Getenv("EVIDENCE_STORAGE_DIR"),
		PostgresDSN:        os.Getenv("POSTGRES_DSN"),
		RedisAddr:          getenv("REDIS_ADDR", "localhost:6379"),
		EmployeeNoPattern:  compiled,
	}
	if cfg.NewAPIBaseURL == "" {
		return Config{}, errors.New("NEW_API_BASE_URL is required")
	}
	if cfg.AuditHMACSecret == "" {
		return Config{}, errors.New("AUDIT_HMAC_SECRET is required")
	}
	if len(cfg.AuditHMACSecret) < 32 {
		return Config{}, errors.New("AUDIT_HMAC_SECRET must be at least 32 characters")
	}
	if cfg.EvidenceStorageDir == "" {
		return Config{}, errors.New("EVIDENCE_STORAGE_DIR is required")
	}
	if cfg.PostgresDSN == "" {
		return Config{}, errors.New("POSTGRES_DSN is required")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
```

- [ ] **Step 6: Add main entrypoint**

Create `cmd/audit-gateway/main.go`:

```go
package main

import (
	"fmt"
	"log"

	"github.com/your-company/new-api-gateway/internal/config"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}
	fmt.Printf("audit gateway configured for %s on %s\n", cfg.NewAPIBaseURL, cfg.ListenAddr)
}
```

- [ ] **Step 7: Add local environment example**

Create `.env.example`:

```bash
AUDIT_GATEWAY_LISTEN_ADDR=:8080
NEW_API_BASE_URL=http://localhost:3000
AUDIT_HMAC_SECRET=replace-with-at-least-32-random-characters
EVIDENCE_STORAGE_DIR=./var/evidence
POSTGRES_DSN=postgres://audit:audit@localhost:5432/audit_gateway?sslmode=disable
REDIS_ADDR=localhost:6379
EMPLOYEE_NO_PATTERN=^[A-Z][0-9]{5}$
```

- [ ] **Step 8: Add Makefile commands**

Create `Makefile`:

```makefile
.PHONY: test run tidy

test:
	go test ./...

run:
	go run ./cmd/audit-gateway

tidy:
	go mod tidy
```

- [ ] **Step 9: Add local services compose file**

Create `deploy/docker-compose.yml`:

```yaml
services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: audit
      POSTGRES_PASSWORD: audit
      POSTGRES_DB: audit_gateway
    ports:
      - "5432:5432"
    volumes:
      - audit-postgres:/var/lib/postgresql/data
  redis:
    image: redis:7
    ports:
      - "6379:6379"
volumes:
  audit-postgres:
```

- [ ] **Step 10: Verify scaffold**

Run:

```bash
go test ./...
```

Expected: PASS for config tests.

- [ ] **Step 11: Commit scaffold**

Run:

```bash
git add go.mod go.sum cmd internal .env.example Makefile deploy/docker-compose.yml
git commit -m "feat: scaffold audit gateway service"
```

Expected: commit succeeds.

---

### Task 2: API Key Extraction and Fingerprinting

**Files:**
- Create: `internal/authkeys/extractor.go`
- Create: `internal/authkeys/extractor_test.go`
- Create: `internal/fingerprint/fingerprint.go`
- Create: `internal/fingerprint/fingerprint_test.go`

- [ ] **Step 1: Write API key extraction tests**

Create `internal/authkeys/extractor_test.go`:

```go
package authkeys

import (
	"net/http"
	"testing"
)

func TestExtractCanonicalKeyFromAuthorization(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-abc123-extra")

	result, ok := Extract(req)
	if !ok {
		t.Fatal("expected key")
	}
	if result.CanonicalKey != "abc123" {
		t.Fatalf("CanonicalKey = %q", result.CanonicalKey)
	}
	if result.Source != SourceAuthorization {
		t.Fatalf("Source = %q", result.Source)
	}
}

func TestExtractCanonicalKeyFromClaudeHeader(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", "sk-claude123")

	result, ok := Extract(req)
	if !ok || result.CanonicalKey != "claude123" {
		t.Fatalf("result = %#v ok=%v", result, ok)
	}
}

func TestExtractCanonicalKeyFromGeminiQuery(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1beta/models/gemini:generateContent?key=sk-gemini123", nil)

	result, ok := Extract(req)
	if !ok || result.CanonicalKey != "gemini123" {
		t.Fatalf("result = %#v ok=%v", result, ok)
	}
}

func TestExtractCanonicalKeyFromRealtimeProtocol(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/v1/realtime", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "realtime, openai-insecure-api-key.sk-real123, openai-beta.realtime-v1")

	result, ok := Extract(req)
	if !ok || result.CanonicalKey != "real123" {
		t.Fatalf("result = %#v ok=%v", result, ok)
	}
}

func TestExtractReturnsFalseWhenMissing(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, ok := Extract(req)
	if ok {
		t.Fatal("expected no key")
	}
}
```

- [ ] **Step 2: Run extraction tests and verify failure**

Run:

```bash
go test ./internal/authkeys -v
```

Expected: FAIL because package implementation is missing.

- [ ] **Step 3: Implement API key extractor**

Create `internal/authkeys/extractor.go`:

```go
package authkeys

import (
	"net/http"
	"strings"
)

type Source string

const (
	SourceAuthorization Source = "authorization"
	SourceAnthropic     Source = "x-api-key"
	SourceGeminiQuery   Source = "query:key"
	SourceGeminiHeader  Source = "x-goog-api-key"
	SourceMidjourney    Source = "mj-api-secret"
	SourceRealtime      Source = "sec-websocket-protocol"
)

type Result struct {
	CanonicalKey string
	Source       Source
}

func Extract(req *http.Request) (Result, bool) {
	if value := req.Header.Get("Authorization"); value != "" {
		return Result{CanonicalKey: canonicalize(value), Source: SourceAuthorization}, true
	}
	if value := req.Header.Get("x-api-key"); value != "" {
		return Result{CanonicalKey: canonicalize(value), Source: SourceAnthropic}, true
	}
	if value := req.URL.Query().Get("key"); value != "" {
		return Result{CanonicalKey: canonicalize(value), Source: SourceGeminiQuery}, true
	}
	if value := req.Header.Get("x-goog-api-key"); value != "" {
		return Result{CanonicalKey: canonicalize(value), Source: SourceGeminiHeader}, true
	}
	if value := req.Header.Get("mj-api-secret"); value != "" {
		return Result{CanonicalKey: canonicalize(value), Source: SourceMidjourney}, true
	}
	if value := req.Header.Get("Sec-WebSocket-Protocol"); value != "" {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "openai-insecure-api-key.") {
				return Result{CanonicalKey: canonicalize(strings.TrimPrefix(part, "openai-insecure-api-key.")), Source: SourceRealtime}, true
			}
		}
	}
	return Result{}, false
}

func canonicalize(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		value = strings.TrimSpace(value[7:])
	}
	value = strings.TrimPrefix(value, "sk-")
	parts := strings.Split(value, "-")
	if len(parts) > 0 {
		return parts[0]
	}
	return value
}
```

- [ ] **Step 4: Verify extraction tests pass**

Run:

```bash
go test ./internal/authkeys -v
```

Expected: PASS.

- [ ] **Step 5: Write fingerprint tests**

Create `internal/fingerprint/fingerprint_test.go`:

```go
package fingerprint

import "testing"

func TestComputeStableFingerprint(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	first := Compute("abc123", secret)
	second := Compute("abc123", secret)
	if first.Value != second.Value {
		t.Fatalf("fingerprints differ: %q != %q", first.Value, second.Value)
	}
	if first.Display == "" || first.Display[:5] != "tkfp_" {
		t.Fatalf("unexpected display %q", first.Display)
	}
}

func TestComputeChangesWithSecret(t *testing.T) {
	first := Compute("abc123", "0123456789abcdef0123456789abcdef")
	second := Compute("abc123", "abcdef0123456789abcdef0123456789")
	if first.Value == second.Value {
		t.Fatal("expected different fingerprints for different secrets")
	}
}
```

- [ ] **Step 6: Run fingerprint tests and verify failure**

Run:

```bash
go test ./internal/fingerprint -v
```

Expected: FAIL because `Compute` is not defined.

- [ ] **Step 7: Implement fingerprint package**

Create `internal/fingerprint/fingerprint.go`:

```go
package fingerprint

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

type Fingerprint struct {
	Value   string
	Display string
}

func Compute(canonicalKey, secret string) Fingerprint {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonicalKey))
	sum := mac.Sum(nil)
	value := hex.EncodeToString(sum)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum)
	display := "tkfp_" + strings.ToLower(encoded[:12])
	return Fingerprint{Value: value, Display: display}
}
```

- [ ] **Step 8: Verify all key tests pass**

Run:

```bash
go test ./internal/authkeys ./internal/fingerprint -v
```

Expected: PASS.

- [ ] **Step 9: Commit key handling**

Run:

```bash
git add internal/authkeys internal/fingerprint
git commit -m "feat: add API key extraction and fingerprinting"
```

Expected: commit succeeds.

---

### Task 3: Employee Number Validation and Identity Resolver

**Files:**
- Create: `internal/employee/employee.go`
- Create: `internal/employee/employee_test.go`
- Create: `internal/identity/stores.go`
- Create: `internal/identity/resolver.go`
- Create: `internal/identity/resolver_test.go`

- [ ] **Step 1: Write employee number tests**

Create `internal/employee/employee_test.go`:

```go
package employee

import (
	"regexp"
	"testing"
)

func TestNormalizeEmployeeNo(t *testing.T) {
	got := Normalize(" e12345 ")
	if got != "E12345" {
		t.Fatalf("Normalize returned %q", got)
	}
}

func TestValidateEmployeeNo(t *testing.T) {
	pattern := regexp.MustCompile(`^[A-Z][0-9]{5}$`)
	if err := Validate("E12345", pattern); err != nil {
		t.Fatalf("expected valid employee no: %v", err)
	}
	if err := Validate("ZhangSan", pattern); err == nil {
		t.Fatal("expected invalid employee no")
	}
}
```

- [ ] **Step 2: Implement employee helpers**

Create `internal/employee/employee.go`:

```go
package employee

import (
	"fmt"
	"regexp"
	"strings"
)

func Normalize(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func Validate(employeeNo string, pattern *regexp.Regexp) error {
	if employeeNo == "" {
		return fmt.Errorf("employee number is empty")
	}
	if !pattern.MatchString(employeeNo) {
		return fmt.Errorf("employee number %q does not match required pattern", employeeNo)
	}
	return nil
}
```

- [ ] **Step 3: Verify employee tests**

Run:

```bash
go test ./internal/employee -v
```

Expected: PASS.

- [ ] **Step 4: Define identity store interfaces**

Create `internal/identity/stores.go`:

```go
package identity

import "context"

type Cache interface {
	Get(ctx context.Context, fingerprint string) (Snapshot, bool, error)
	Set(ctx context.Context, snapshot Snapshot) error
}

type TokenLookup interface {
	FindByCanonicalKey(ctx context.Context, canonicalKey string) (NewAPIToken, error)
}

type NewAPIToken struct {
	TokenID            int
	TokenName          string
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

type Snapshot struct {
	TokenFingerprint     string
	FingerprintDisplay   string
	NewAPITokenID        int
	TokenNameRaw         string
	EmployeeNo           string
	TokenStatus          int
	TokenGroup           string
	ExpiredTime          int64
	AccessedTime         int64
	RemainQuota          int
	UsedQuota            int
	UnlimitedQuota       bool
	ModelLimitsEnabled   bool
	ModelLimits          string
	ResolutionStatus     string
	IdentityCacheStatus  string
}
```

- [ ] **Step 5: Write resolver tests**

Create `internal/identity/resolver_test.go`:

```go
package identity

import (
	"context"
	"regexp"
	"testing"
)

type fakeCache struct {
	value Snapshot
	ok    bool
}

func (f *fakeCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	return f.value, f.ok, nil
}

func (f *fakeCache) Set(ctx context.Context, snapshot Snapshot) error {
	f.value = snapshot
	f.ok = true
	return nil
}

type fakeLookup struct {
	token NewAPIToken
}

func (f fakeLookup) FindByCanonicalKey(ctx context.Context, canonicalKey string) (NewAPIToken, error) {
	return f.token, nil
}

func TestResolverUsesCacheHit(t *testing.T) {
	cache := &fakeCache{ok: true, value: Snapshot{TokenFingerprint: "fp", EmployeeNo: "E12345", ResolutionStatus: "resolved"}}
	resolver := Resolver{Cache: cache, Lookup: fakeLookup{}, EmployeeNoPattern: regexp.MustCompile(`^[A-Z][0-9]{5}$`)}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.EmployeeNo != "E12345" || got.IdentityCacheStatus != "redis_or_local_hit" {
		t.Fatalf("unexpected snapshot %#v", got)
	}
}

func TestResolverLookupConvertsTokenNameToEmployeeNo(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache: cache,
		Lookup: fakeLookup{token: NewAPIToken{TokenID: 12, TokenName: " e12345 ", TokenStatus: 1}},
		EmployeeNoPattern: regexp.MustCompile(`^[A-Z][0-9]{5}$`),
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.EmployeeNo != "E12345" {
		t.Fatalf("EmployeeNo = %q", got.EmployeeNo)
	}
	if got.ResolutionStatus != "resolved" {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
}

func TestResolverMarksInvalidEmployeeNo(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache: cache,
		Lookup: fakeLookup{token: NewAPIToken{TokenID: 12, TokenName: "alice", TokenStatus: 1}},
		EmployeeNoPattern: regexp.MustCompile(`^[A-Z][0-9]{5}$`),
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != "invalid_employee_no" {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
}
```

- [ ] **Step 6: Implement resolver**

Create `internal/identity/resolver.go`:

```go
package identity

import (
	"context"
	"regexp"

	"github.com/your-company/new-api-gateway/internal/employee"
)

type Resolver struct {
	Cache             Cache
	Lookup            TokenLookup
	EmployeeNoPattern *regexp.Regexp
}

func (r Resolver) Resolve(ctx context.Context, canonicalKey, fingerprintValue, fingerprintDisplay string) (Snapshot, error) {
	if r.Cache != nil {
		cached, ok, err := r.Cache.Get(ctx, fingerprintValue)
		if err != nil {
			return Snapshot{}, err
		}
		if ok {
			cached.IdentityCacheStatus = "redis_or_local_hit"
			return cached, nil
		}
	}

	token, err := r.Lookup.FindByCanonicalKey(ctx, canonicalKey)
	if err != nil {
		return Snapshot{
			TokenFingerprint:    fingerprintValue,
			FingerprintDisplay:  fingerprintDisplay,
			ResolutionStatus:    "db_error",
			IdentityCacheStatus: "miss",
		}, nil
	}

	employeeNo := employee.Normalize(token.TokenName)
	status := "resolved"
	if employeeNo == "" {
		status = "missing_employee_no"
	} else if err := employee.Validate(employeeNo, r.EmployeeNoPattern); err != nil {
		status = "invalid_employee_no"
	}

	snapshot := Snapshot{
		TokenFingerprint:     fingerprintValue,
		FingerprintDisplay:   fingerprintDisplay,
		NewAPITokenID:        token.TokenID,
		TokenNameRaw:         token.TokenName,
		EmployeeNo:           employeeNo,
		TokenStatus:          token.TokenStatus,
		TokenGroup:           token.TokenGroup,
		ExpiredTime:          token.ExpiredTime,
		AccessedTime:         token.AccessedTime,
		RemainQuota:          token.RemainQuota,
		UsedQuota:            token.UsedQuota,
		UnlimitedQuota:       token.UnlimitedQuota,
		ModelLimitsEnabled:   token.ModelLimitsEnabled,
		ModelLimits:          token.ModelLimits,
		ResolutionStatus:     status,
		IdentityCacheStatus:  "miss_db_lookup",
	}
	if r.Cache != nil {
		_ = r.Cache.Set(ctx, snapshot)
	}
	return snapshot, nil
}
```

- [ ] **Step 7: Verify identity tests**

Run:

```bash
go test ./internal/employee ./internal/identity -v
```

Expected: PASS.

- [ ] **Step 8: Commit identity model**

Run:

```bash
git add internal/employee internal/identity
git commit -m "feat: resolve token names to employee numbers"
```

Expected: commit succeeds.

---


### Task 4: Concrete Redis Cache and new-api Token Lookup

**Files:**
- Create: `internal/identity/redis_cache.go`
- Create: `internal/identity/postgres_token_lookup.go`
- Create: `internal/identity/redis_cache_test.go`
- Create: `internal/identity/postgres_token_lookup_test.go`

- [ ] **Step 1: Write Redis cache serialization test**

Create `internal/identity/redis_cache_test.go`:

```go
package identity

import (
	"encoding/json"
	"testing"
)

func TestSnapshotJSONRoundTrip(t *testing.T) {
	original := Snapshot{TokenFingerprint: "fp", FingerprintDisplay: "tkfp_abc", EmployeeNo: "E12345", ResolutionStatus: "resolved"}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.EmployeeNo != original.EmployeeNo {
		t.Fatalf("EmployeeNo = %q", decoded.EmployeeNo)
	}
}
```

- [ ] **Step 2: Implement Redis cache**

Create `internal/identity/redis_cache.go`:

```go
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	Client *redis.Client
	TTL    time.Duration
}

func (c RedisCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	data, err := c.Client.Get(ctx, "identity:"+fingerprint).Bytes()
	if errors.Is(err, redis.Nil) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, false, err
	}
	return snapshot, true, nil
}

func (c RedisCache) Set(ctx context.Context, snapshot Snapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return c.Client.Set(ctx, "identity:"+snapshot.TokenFingerprint, data, ttl).Err()
}
```

- [ ] **Step 3: Write token lookup query test**

Create `internal/identity/postgres_token_lookup_test.go`:

```go
package identity

import (
	"strings"
	"testing"
)

func TestNewAPITokenQueryUsesOnlyTokensTable(t *testing.T) {
	query := newAPITokenQuery()
	if !strings.Contains(query, "FROM tokens") {
		t.Fatalf("query does not read tokens table: %s", query)
	}
	if strings.Contains(query, "JOIN users") {
		t.Fatalf("query must not join users table: %s", query)
	}
	if !strings.Contains(query, `"group"`) {
		t.Fatalf("query must quote reserved group column for PostgreSQL: %s", query)
	}
}
```

- [ ] **Step 4: Implement new-api PostgreSQL token lookup**

Create `internal/identity/postgres_token_lookup.go`:

```go
package identity

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresTokenLookup struct {
	Pool *pgxpool.Pool
}

func (l PostgresTokenLookup) FindByCanonicalKey(ctx context.Context, canonicalKey string) (NewAPIToken, error) {
	var token NewAPIToken
	err := l.Pool.QueryRow(ctx, newAPITokenQuery(), canonicalKey).Scan(
		&token.TokenID,
		&token.TokenName,
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
	return token, err
}

func newAPITokenQuery() string {
	return `
SELECT
  id AS token_id,
  name AS token_name,
  status AS token_status,
  "group" AS token_group,
  expired_time,
  accessed_time,
  remain_quota,
  used_quota,
  unlimited_quota,
  model_limits_enabled,
  model_limits
FROM tokens
WHERE key = $1
LIMIT 1`
}
```

- [ ] **Step 5: Verify identity package**

Run:

```bash
go test ./internal/identity -v
```

Expected: PASS.

- [ ] **Step 6: Commit concrete identity stores**

Run:

```bash
git add internal/identity/redis_cache.go internal/identity/redis_cache_test.go internal/identity/postgres_token_lookup.go internal/identity/postgres_token_lookup_test.go
git commit -m "feat: add identity cache and token lookup stores"
```

Expected: commit succeeds.

---

### Task 5: Route Registry and Coverage Classification

**Files:**
- Create: `internal/routes/registry.go`
- Create: `internal/routes/registry_test.go`
- Create: `internal/alerts/coverage.go`

- [ ] **Step 1: Write route registry tests**

Create `internal/routes/registry_test.go`:

```go
package routes

import "testing"

func TestDefaultRegistryMatchesImageGeneration(t *testing.T) {
	entry, ok := DefaultRegistry().Match("POST", "/v1/images/generations")
	if !ok {
		t.Fatal("expected route match")
	}
	if entry.ProtocolFamily != "openai_images" {
		t.Fatalf("ProtocolFamily = %q", entry.ProtocolFamily)
	}
	if entry.CaptureMode != CaptureRawAndNormalized {
		t.Fatalf("CaptureMode = %q", entry.CaptureMode)
	}
}

func TestDefaultRegistryMatchesMidjourneyRawMinimal(t *testing.T) {
	entry, ok := DefaultRegistry().Match("POST", "/mj/submit/imagine")
	if !ok {
		t.Fatal("expected route match")
	}
	if entry.CaptureMode != CaptureRawAndMinimal {
		t.Fatalf("CaptureMode = %q", entry.CaptureMode)
	}
}

func TestDefaultRegistryUnknownRoute(t *testing.T) {
	_, ok := DefaultRegistry().Match("POST", "/unknown/path")
	if ok {
		t.Fatal("expected no route match")
	}
}
```

- [ ] **Step 2: Implement route registry**

Create `internal/routes/registry.go`:

```go
package routes

import "strings"

type CaptureMode string

const (
	CaptureRawAndNormalized CaptureMode = "raw_and_normalized"
	CaptureRawAndMinimal    CaptureMode = "raw_and_minimal"
	CaptureRawOnly          CaptureMode = "raw_only"
)

type Entry struct {
	Method              string
	PathPattern         string
	ProtocolFamily      string
	BodyKind            string
	CaptureMode         CaptureMode
	Normalizer          string
	MinimalExtractor    string
	UnsupportedAlertCode string
}

type Registry struct {
	entries []Entry
}

func DefaultRegistry() Registry {
	return Registry{entries: []Entry{
		{Method: "POST", PathPattern: "/v1/chat/completions", ProtocolFamily: "openai_chat", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_chat"},
		{Method: "POST", PathPattern: "/pg/chat/completions", ProtocolFamily: "openai_chat", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_chat"},
		{Method: "POST", PathPattern: "/v1/responses", ProtocolFamily: "openai_responses", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_responses"},
		{Method: "POST", PathPattern: "/v1/responses/compact", ProtocolFamily: "openai_responses", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_responses_compact"},
		{Method: "POST", PathPattern: "/v1/messages", ProtocolFamily: "claude_messages", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "claude_messages"},
		{Method: "POST", PathPattern: "/v1/completions", ProtocolFamily: "openai_completions", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_completions"},
		{Method: "POST", PathPattern: "/v1/embeddings", ProtocolFamily: "embeddings", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "embeddings"},
		{Method: "POST", PathPattern: "/v1/rerank", ProtocolFamily: "rerank", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "rerank"},
		{Method: "POST", PathPattern: "/v1/images/generations", ProtocolFamily: "openai_images", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_image_generation"},
		{Method: "POST", PathPattern: "/v1/images/edits", ProtocolFamily: "openai_images", BodyKind: "multipart_or_json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_image_edit"},
		{Method: "POST", PathPattern: "/v1/edits", ProtocolFamily: "openai_images", BodyKind: "multipart_or_json", CaptureMode: CaptureRawAndNormalized, Normalizer: "openai_edit"},
		{Method: "POST", PathPattern: "/v1/audio/transcriptions", ProtocolFamily: "openai_audio", BodyKind: "multipart", CaptureMode: CaptureRawAndNormalized, Normalizer: "audio_transcription"},
		{Method: "POST", PathPattern: "/v1/audio/translations", ProtocolFamily: "openai_audio", BodyKind: "multipart", CaptureMode: CaptureRawAndNormalized, Normalizer: "audio_translation"},
		{Method: "POST", PathPattern: "/v1/audio/speech", ProtocolFamily: "openai_audio", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "audio_speech"},
		{Method: "POST", PathPattern: "/v1beta/models/*", ProtocolFamily: "gemini", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "gemini_generate_content"},
		{Method: "POST", PathPattern: "/v1/models/*", ProtocolFamily: "gemini", BodyKind: "json", CaptureMode: CaptureRawAndNormalized, Normalizer: "gemini_generate_content"},
		{Method: "GET", PathPattern: "/v1/realtime", ProtocolFamily: "realtime", BodyKind: "websocket", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "realtime_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/mj/*", ProtocolFamily: "midjourney", BodyKind: "json", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/suno/*", ProtocolFamily: "suno", BodyKind: "json", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
		{Method: "POST", PathPattern: "/v1/videos*", ProtocolFamily: "video", BodyKind: "json_or_multipart", CaptureMode: CaptureRawAndMinimal, MinimalExtractor: "generic_task_minimal", UnsupportedAlertCode: "known_route_raw_first"},
	}}
}

func (r Registry) Match(method, path string) (Entry, bool) {
	for _, entry := range r.entries {
		if entry.Method != method {
			continue
		}
		if matchPath(entry.PathPattern, path) {
			return entry, true
		}
	}
	return Entry{}, false
}

func matchPath(pattern, path string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == path
}
```

- [ ] **Step 3: Add coverage alert model**

Create `internal/alerts/coverage.go`:

```go
package alerts

import "time"

type CoverageAlert struct {
	AlertCode          string
	Severity           string
	Method             string
	RoutePattern       string
	RawPath            string
	ContentType        string
	ProtocolFamily     string
	Message            string
	SampleTraceID      string
	FirstSeenAt        time.Time
	LastSeenAt         time.Time
}

func KnownRawFirst(method, routePattern, rawPath, protocolFamily, traceID string) CoverageAlert {
	now := time.Now().UTC()
	return CoverageAlert{
		AlertCode:      "known_route_raw_first",
		Severity:       "medium",
		Method:         method,
		RoutePattern:   routePattern,
		RawPath:        rawPath,
		ProtocolFamily: protocolFamily,
		Message:        "route is captured with raw evidence and minimal metadata; deep normalizer is not enabled",
		SampleTraceID:  traceID,
		FirstSeenAt:    now,
		LastSeenAt:     now,
	}
}
```

- [ ] **Step 4: Verify route tests**

Run:

```bash
go test ./internal/routes ./internal/alerts -v
```

Expected: PASS.

- [ ] **Step 5: Commit route registry**

Run:

```bash
git add internal/routes internal/alerts
git commit -m "feat: add route registry and coverage alert model"
```

Expected: commit succeeds.

---

### Task 6: Evidence Store and Trace Models

**Files:**
- Create: `internal/evidence/store.go`
- Create: `internal/evidence/store_test.go`
- Create: `internal/traces/model.go`

- [ ] **Step 1: Write evidence store test**

Create `internal/evidence/store_test.go`:

```go
package evidence

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestFilesystemStoreWritesObjectWithHash(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	obj, err := store.Put(context.Background(), PutRequest{
		TraceID:     "trace_123",
		ObjectType:  "request_body",
		ContentType: "application/json",
		Reader:      bytes.NewBufferString(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	if obj.SizeBytes == 0 || obj.SHA256 == "" || obj.ObjectRef == "" {
		t.Fatalf("invalid object metadata %#v", obj)
	}

	reader, err := store.Get(context.Background(), obj.ObjectRef)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	defer reader.Close()
	body, _ := io.ReadAll(reader)
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q", string(body))
	}
}
```

- [ ] **Step 2: Implement evidence store**

Create `internal/evidence/store.go`:

```go
package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type PutRequest struct {
	TraceID     string
	ObjectType  string
	ContentType string
	Reader      io.Reader
}

type Object struct {
	ObjectRef   string
	StorageBackend string
	ContentType string
	SizeBytes   int64
	SHA256      string
	CreatedAt   time.Time
}

type Store interface {
	Put(ctx context.Context, req PutRequest) (Object, error)
	Get(ctx context.Context, objectRef string) (io.ReadCloser, error)
}

type FilesystemStore struct {
	root string
}

func NewFilesystemStore(root string) FilesystemStore {
	return FilesystemStore{root: root}
}

func (s FilesystemStore) Put(ctx context.Context, req PutRequest) (Object, error) {
	now := time.Now().UTC()
	dir := filepath.Join(s.root, "raw", now.Format("2006"), now.Format("01"), now.Format("02"), req.TraceID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Object{}, err
	}
	name := fmt.Sprintf("%s.bin", req.ObjectType)
	path := filepath.Join(dir, name)
	file, err := os.Create(path)
	if err != nil {
		return Object{}, err
	}
	defer file.Close()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), req.Reader)
	if err != nil {
		return Object{}, err
	}
	ref, err := filepath.Rel(s.root, path)
	if err != nil {
		return Object{}, err
	}
	return Object{
		ObjectRef:      filepath.ToSlash(ref),
		StorageBackend: "filesystem",
		ContentType:    req.ContentType,
		SizeBytes:      written,
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		CreatedAt:      now,
	}, nil
}

func (s FilesystemStore) Get(ctx context.Context, objectRef string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(s.root, filepath.FromSlash(objectRef)))
}
```

- [ ] **Step 3: Add trace domain models**

Create `internal/traces/model.go`:

```go
package traces

import "time"

type Trace struct {
	TraceID                  string
	Method                   string
	Path                     string
	RoutePattern             string
	ProtocolFamily           string
	CaptureMode              string
	StatusCode               int
	UpstreamStatusCode       int
	Stream                   bool
	RequestStartedAt         time.Time
	ResponseFinishedAt       time.Time
	DurationMillis           int64
	RequestBodySize          int64
	ResponseBodySize         int64
	RequestBodySHA256        string
	ResponseBodySHA256       string
	RequestRawRef            string
	ResponseRawRef           string
	TokenFingerprint         string
	FingerprintDisplay       string
	NewAPITokenIDSnapshot    int
	TokenNameSnapshot        string
	EmployeeNoSnapshot       string
	IdentityResolutionStatus string
	IdentityCacheStatus      string
	ModelRequested           string
	AnalysisStatus           string
	CreatedAt                time.Time
}

type RawEvidenceObject struct {
	TraceID        string
	ObjectType     string
	ObjectRef      string
	StorageBackend string
	ContentType    string
	SizeBytes      int64
	SHA256         string
	CreatedAt      time.Time
}
```

- [ ] **Step 4: Verify evidence tests**

Run:

```bash
go test ./internal/evidence ./internal/traces -v
```

Expected: PASS.

- [ ] **Step 5: Commit evidence store**

Run:

```bash
git add internal/evidence internal/traces
git commit -m "feat: add evidence store and trace models"
```

Expected: commit succeeds.

---

### Task 7: Database Schema and Trace Repository

**Files:**
- Create: `migrations/0001_core_schema.sql`
- Create: `internal/traces/repository.go`
- Create: `internal/traces/repository_test.go`

- [ ] **Step 1: Create core schema migration**

Create `migrations/0001_core_schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS traces (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL UNIQUE,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    route_pattern TEXT NOT NULL,
    protocol_family TEXT NOT NULL,
    capture_mode TEXT NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    upstream_status_code INTEGER NOT NULL DEFAULT 0,
    stream BOOLEAN NOT NULL DEFAULT FALSE,
    request_started_at TIMESTAMPTZ NOT NULL,
    response_finished_at TIMESTAMPTZ,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    request_body_size BIGINT NOT NULL DEFAULT 0,
    response_body_size BIGINT NOT NULL DEFAULT 0,
    request_body_sha256 TEXT NOT NULL DEFAULT '',
    response_body_sha256 TEXT NOT NULL DEFAULT '',
    request_raw_ref TEXT NOT NULL DEFAULT '',
    response_raw_ref TEXT NOT NULL DEFAULT '',
    token_fingerprint TEXT NOT NULL DEFAULT '',
    fingerprint_display TEXT NOT NULL DEFAULT '',
    new_api_token_id_snapshot INTEGER NOT NULL DEFAULT 0,
    token_name_snapshot TEXT NOT NULL DEFAULT '',
    employee_no_snapshot TEXT NOT NULL DEFAULT '',
    identity_resolution_status TEXT NOT NULL DEFAULT '',
    identity_cache_status TEXT NOT NULL DEFAULT '',
    model_requested TEXT NOT NULL DEFAULT '',
    analysis_status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_traces_created_at ON traces(created_at);
CREATE INDEX IF NOT EXISTS idx_traces_employee_created ON traces(employee_no_snapshot, created_at);
CREATE INDEX IF NOT EXISTS idx_traces_token_created ON traces(token_fingerprint, created_at);
CREATE INDEX IF NOT EXISTS idx_traces_route_created ON traces(route_pattern, created_at);

CREATE TABLE IF NOT EXISTS raw_evidence_objects (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    object_type TEXT NOT NULL,
    object_ref TEXT NOT NULL,
    storage_backend TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    sha256 TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_raw_evidence_trace ON raw_evidence_objects(trace_id);

CREATE TABLE IF NOT EXISTS token_identity_cache (
    token_fingerprint TEXT PRIMARY KEY,
    fingerprint_display TEXT NOT NULL,
    new_api_token_id INTEGER NOT NULL DEFAULT 0,
    token_name_raw TEXT NOT NULL DEFAULT '',
    employee_no TEXT NOT NULL DEFAULT '',
    token_status INTEGER NOT NULL DEFAULT 0,
    token_group TEXT NOT NULL DEFAULT '',
    token_expired_time BIGINT NOT NULL DEFAULT 0,
    token_accessed_time BIGINT NOT NULL DEFAULT 0,
    remain_quota INTEGER NOT NULL DEFAULT 0,
    used_quota INTEGER NOT NULL DEFAULT 0,
    unlimited_quota BOOLEAN NOT NULL DEFAULT FALSE,
    model_limits_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    model_limits TEXT NOT NULL DEFAULT '',
    resolved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    refreshed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolution_error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_token_identity_employee ON token_identity_cache(employee_no);

CREATE TABLE IF NOT EXISTS coverage_alerts (
    id BIGSERIAL PRIMARY KEY,
    alert_id TEXT NOT NULL UNIQUE,
    alert_code TEXT NOT NULL,
    severity TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    method TEXT NOT NULL DEFAULT '',
    route_pattern TEXT NOT NULL DEFAULT '',
    raw_path TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',
    protocol_family TEXT NOT NULL DEFAULT '',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    occurrence_count BIGINT NOT NULL DEFAULT 1,
    sample_trace_ids TEXT[] NOT NULL DEFAULT '{}',
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- [ ] **Step 2: Write repository interface and implementation**

Create `internal/traces/repository.go`:

```go
package traces

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	InsertTrace(ctx context.Context, trace Trace) error
	InsertRawEvidence(ctx context.Context, object RawEvidenceObject) error
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) PostgresRepository {
	return PostgresRepository{pool: pool}
}

func (r PostgresRepository) InsertTrace(ctx context.Context, trace Trace) error {
	_, err := r.pool.Exec(ctx, `
INSERT INTO traces (
  trace_id, method, path, route_pattern, protocol_family, capture_mode,
  status_code, upstream_status_code, stream, request_started_at, response_finished_at,
  duration_ms, request_body_size, response_body_size, request_body_sha256, response_body_sha256,
  request_raw_ref, response_raw_ref, token_fingerprint, fingerprint_display,
  new_api_token_id_snapshot, token_name_snapshot, employee_no_snapshot,
  identity_resolution_status, identity_cache_status, model_requested, analysis_status, created_at
) VALUES (
  $1,$2,$3,$4,$5,$6,
  $7,$8,$9,$10,$11,
  $12,$13,$14,$15,$16,
  $17,$18,$19,$20,
  $21,$22,$23,
  $24,$25,$26,$27,$28
)`,
		trace.TraceID, trace.Method, trace.Path, trace.RoutePattern, trace.ProtocolFamily, trace.CaptureMode,
		trace.StatusCode, trace.UpstreamStatusCode, trace.Stream, trace.RequestStartedAt, trace.ResponseFinishedAt,
		trace.DurationMillis, trace.RequestBodySize, trace.ResponseBodySize, trace.RequestBodySHA256, trace.ResponseBodySHA256,
		trace.RequestRawRef, trace.ResponseRawRef, trace.TokenFingerprint, trace.FingerprintDisplay,
		trace.NewAPITokenIDSnapshot, trace.TokenNameSnapshot, trace.EmployeeNoSnapshot,
		trace.IdentityResolutionStatus, trace.IdentityCacheStatus, trace.ModelRequested, trace.AnalysisStatus, trace.CreatedAt,
	)
	return err
}

func (r PostgresRepository) InsertRawEvidence(ctx context.Context, object RawEvidenceObject) error {
	_, err := r.pool.Exec(ctx, `
INSERT INTO raw_evidence_objects (
  trace_id, object_type, object_ref, storage_backend, content_type, size_bytes, sha256, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		object.TraceID, object.ObjectType, object.ObjectRef, object.StorageBackend,
		object.ContentType, object.SizeBytes, object.SHA256, object.CreatedAt,
	)
	return err
}
```

- [ ] **Step 3: Add repository test using a fake repository contract**

Create `internal/traces/repository_test.go`:

```go
package traces

import (
	"context"
	"testing"
	"time"
)

type memoryRepository struct {
	traces  []Trace
	objects []RawEvidenceObject
}

func (m *memoryRepository) InsertTrace(ctx context.Context, trace Trace) error {
	m.traces = append(m.traces, trace)
	return nil
}

func (m *memoryRepository) InsertRawEvidence(ctx context.Context, object RawEvidenceObject) error {
	m.objects = append(m.objects, object)
	return nil
}

func TestRepositoryContractStoresTraceAndEvidence(t *testing.T) {
	repo := &memoryRepository{}
	trace := Trace{TraceID: "trace_1", Method: "POST", Path: "/v1/chat/completions", CreatedAt: time.Now().UTC()}
	object := RawEvidenceObject{TraceID: "trace_1", ObjectType: "request_body", ObjectRef: "raw/trace_1/request.body"}

	if err := repo.InsertTrace(context.Background(), trace); err != nil {
		t.Fatalf("InsertTrace error: %v", err)
	}
	if err := repo.InsertRawEvidence(context.Background(), object); err != nil {
		t.Fatalf("InsertRawEvidence error: %v", err)
	}
	if len(repo.traces) != 1 || len(repo.objects) != 1 {
		t.Fatalf("unexpected repo state %#v", repo)
	}
}
```

- [ ] **Step 4: Verify repository package**

Run:

```bash
go test ./internal/traces -v
```

Expected: PASS.

- [ ] **Step 5: Commit schema and repository**

Run:

```bash
git add migrations internal/traces
git commit -m "feat: add core trace schema and repository"
```

Expected: commit succeeds.

---

### Task 8: Reverse Proxy Core for JSON and Raw Bodies

**Files:**
- Create: `internal/gateway/proxy.go`
- Create: `internal/gateway/capture.go`
- Create: `internal/gateway/proxy_test.go`
- Modify: `cmd/audit-gateway/main.go`

- [ ] **Step 1: Write proxy test first**

Create `internal/gateway/proxy_test.go`:

```go
package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/identity"
	"github.com/your-company/new-api-gateway/internal/routes"
	"github.com/your-company/new-api-gateway/internal/traces"
)

type memoryTraceRepo struct {
	traces []traces.Trace
}

func (m *memoryTraceRepo) InsertTrace(ctx context.Context, trace traces.Trace) error {
	m.traces = append(m.traces, trace)
	return nil
}
func (m *memoryTraceRepo) InsertRawEvidence(ctx context.Context, object traces.RawEvidenceObject) error { return nil }

type fixedResolver struct{}

func (fixedResolver) Resolve(ctx context.Context, canonicalKey, fingerprintValue, fingerprintDisplay string) (identity.Snapshot, error) {
	return identity.Snapshot{
		TokenFingerprint: fingerprintValue,
		FingerprintDisplay: fingerprintDisplay,
		NewAPITokenID: 7,
		TokenNameRaw: "E12345",
		EmployeeNo: "E12345",
		ResolutionStatus: "resolved",
		IdentityCacheStatus: "test",
	}, nil
}

func TestProxyForwardsAndRecordsTrace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"model":"gpt-test","messages":[]}` {
			t.Fatalf("upstream body = %q", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl_test","usage":{"total_tokens":3}}`))
	}))
	defer upstream.Close()

	repo := &memoryTraceRepo{}
	handler := Handler{
		UpstreamBaseURL: upstream.URL,
		Registry: routes.DefaultRegistry(),
		EvidenceStore: evidence.NewFilesystemStore(t.TempDir()),
		TraceRepo: repo,
		IdentityResolver: fixedResolver{},
		AuditSecret: "0123456789abcdef0123456789abcdef",
		Now: func() time.Time { return time.Unix(1000, 0).UTC() },
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[]}`))
	req.Header.Set("Authorization", "Bearer sk-abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(repo.traces))
	}
	if repo.traces[0].EmployeeNoSnapshot != "E12345" {
		t.Fatalf("EmployeeNoSnapshot = %q", repo.traces[0].EmployeeNoSnapshot)
	}
}
```

- [ ] **Step 2: Implement capture helper**

Create `internal/gateway/capture.go`:

```go
package gateway

import (
	"bytes"
	"io"
	"net/http"
)

type CapturedRequest struct {
	BodyBytes   []byte
	ContentType string
	SizeBytes   int64
}

func captureRequestBody(req *http.Request) (CapturedRequest, error) {
	if req.Body == nil {
		return CapturedRequest{}, nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return CapturedRequest{}, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return CapturedRequest{BodyBytes: body, ContentType: req.Header.Get("Content-Type"), SizeBytes: int64(len(body))}, nil
}
```

- [ ] **Step 3: Implement proxy handler**

Create `internal/gateway/proxy.go`:

```go
package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/your-company/new-api-gateway/internal/authkeys"
	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/fingerprint"
	"github.com/your-company/new-api-gateway/internal/identity"
	"github.com/your-company/new-api-gateway/internal/ids"
	"github.com/your-company/new-api-gateway/internal/routes"
	"github.com/your-company/new-api-gateway/internal/traces"
)

type IdentityResolver interface {
	Resolve(ctx context.Context, canonicalKey, fingerprintValue, fingerprintDisplay string) (identity.Snapshot, error)
}

type Handler struct {
	UpstreamBaseURL string
	Registry routes.Registry
	EvidenceStore evidence.Store
	TraceRepo traces.Repository
	IdentityResolver IdentityResolver
	AuditSecret string
	Client *http.Client
	Now func() time.Time
}

func (h Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	now := h.now()
	traceID := ids.NewTraceID()
	entry, ok := h.Registry.Match(req.Method, req.URL.Path)
	if !ok {
		entry = routes.Entry{Method: req.Method, PathPattern: "unknown", ProtocolFamily: "unknown", CaptureMode: routes.CaptureRawOnly}
	}

	captured, err := captureRequestBody(req)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	keyResult, hasKey := authkeys.Extract(req)
	var snapshot identity.Snapshot
	if hasKey {
		fp := fingerprint.Compute(keyResult.CanonicalKey, h.AuditSecret)
		snapshot, _ = h.IdentityResolver.Resolve(req.Context(), keyResult.CanonicalKey, fp.Value, fp.Display)
	} else {
		snapshot = identity.Snapshot{ResolutionStatus: "extract_failed"}
	}

	reqObj, _ := h.EvidenceStore.Put(req.Context(), evidence.PutRequest{
		TraceID: traceID, ObjectType: "request_body", ContentType: captured.ContentType, Reader: bytes.NewReader(captured.BodyBytes),
	})

	upstreamReq, err := h.newUpstreamRequest(req, captured.BodyBytes)
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read upstream response", http.StatusBadGateway)
		return
	}
	respObj, _ := h.EvidenceStore.Put(req.Context(), evidence.PutRequest{
		TraceID: traceID, ObjectType: "response_body", ContentType: resp.Header.Get("Content-Type"), Reader: bytes.NewReader(respBody),
	})

	copyHeaders(w.Header(), resp.Header)
	w.Header().Set("x-audit-trace-id", traceID)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)

	finished := h.now()
	_ = h.TraceRepo.InsertTrace(req.Context(), traces.Trace{
		TraceID: traceID, Method: req.Method, Path: req.URL.Path, RoutePattern: entry.PathPattern,
		ProtocolFamily: entry.ProtocolFamily, CaptureMode: string(entry.CaptureMode), StatusCode: resp.StatusCode,
		UpstreamStatusCode: resp.StatusCode, RequestStartedAt: now, ResponseFinishedAt: finished,
		DurationMillis: finished.Sub(now).Milliseconds(), RequestBodySize: reqObj.SizeBytes, ResponseBodySize: respObj.SizeBytes,
		RequestBodySHA256: reqObj.SHA256, ResponseBodySHA256: respObj.SHA256, RequestRawRef: reqObj.ObjectRef, ResponseRawRef: respObj.ObjectRef,
		TokenFingerprint: snapshot.TokenFingerprint, FingerprintDisplay: snapshot.FingerprintDisplay,
		NewAPITokenIDSnapshot: snapshot.NewAPITokenID, TokenNameSnapshot: snapshot.TokenNameRaw, EmployeeNoSnapshot: snapshot.EmployeeNo,
		IdentityResolutionStatus: snapshot.ResolutionStatus, IdentityCacheStatus: snapshot.IdentityCacheStatus, AnalysisStatus: "pending", CreatedAt: now,
	})
}

func (h Handler) now() time.Time {
	if h.Now != nil { return h.Now() }
	return time.Now().UTC()
}

func (h Handler) newUpstreamRequest(original *http.Request, body []byte) (*http.Request, error) {
	base, err := url.Parse(h.UpstreamBaseURL)
	if err != nil { return nil, err }
	upstreamURL := *base
	upstreamURL.Path = original.URL.Path
	upstreamURL.RawQuery = original.URL.RawQuery
	upstreamReq, err := http.NewRequestWithContext(original.Context(), original.Method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil { return nil, err }
	upstreamReq.Header = original.Header.Clone()
	return upstreamReq, nil
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
```

- [ ] **Step 4: Add trace ID helper**

Create `internal/ids/ids.go`:

```go
package ids

import (
	"crypto/rand"
	"encoding/hex"
)

func NewTraceID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return "trace_" + hex.EncodeToString(buf)
}
```

- [ ] **Step 5: Verify proxy test**

Run:

```bash
go test ./internal/gateway -v
```

Expected: PASS.

- [ ] **Step 6: Wire main to start server with stub dependencies**

Modify `cmd/audit-gateway/main.go` so it constructs config and prints the next integration message until Task 9 wires real dependencies:

```go
package main

import (
	"fmt"
	"log"

	"github.com/your-company/new-api-gateway/internal/config"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}
	fmt.Printf("audit gateway core is configured for %s on %s; dependency wiring follows in the next task\n", cfg.NewAPIBaseURL, cfg.ListenAddr)
}
```

- [ ] **Step 7: Run full tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 8: Commit proxy core**

Run:

```bash
git add internal/gateway internal/ids cmd/audit-gateway/main.go
git commit -m "feat: add transparent proxy capture core"
```

Expected: commit succeeds.

---

### Task 9: Streaming, Multipart, and Degraded Capture Contracts

**Files:**
- Create: `internal/gateway/stream.go`
- Create: `internal/gateway/stream_test.go`
- Create: `internal/gateway/multipart.go`
- Create: `internal/gateway/multipart_test.go`

- [ ] **Step 1: Write SSE tee test**

Create `internal/gateway/stream_test.go`:

```go
package gateway

import (
	"bytes"
	"io"
	"testing"
)

func TestTeeStreamCopiesToClientAndCapture(t *testing.T) {
	client := &bytes.Buffer{}
	capture := &bytes.Buffer{}
	input := bytes.NewBufferString("data: one\n\ndata: two\n\n")
	written, err := teeStream(input, client, capture)
	if err != nil {
		t.Fatalf("teeStream error: %v", err)
	}
	if written == 0 {
		t.Fatal("expected bytes written")
	}
	if client.String() != capture.String() {
		t.Fatalf("client=%q capture=%q", client.String(), capture.String())
	}
}

func TestTeeStreamPropagatesReadErrorAfterCopy(t *testing.T) {
	client := &bytes.Buffer{}
	capture := &bytes.Buffer{}
	_, err := teeStream(errorReader{}, client, capture)
	if err == nil {
		t.Fatal("expected error")
	}
}

type errorReader struct{}
func (errorReader) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
```

- [ ] **Step 2: Implement stream tee helper**

Create `internal/gateway/stream.go`:

```go
package gateway

import "io"

func teeStream(src io.Reader, client io.Writer, capture io.Writer) (int64, error) {
	return io.Copy(io.MultiWriter(client, capture), src)
}
```

- [ ] **Step 3: Write multipart metadata test**

Create `internal/gateway/multipart_test.go`:

```go
package gateway

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"
)

func TestDetectMultipart(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "audio.wav")
	_, _ = part.Write([]byte("abc"))
	_ = writer.Close()

	req, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if !isMultipart(req) {
		t.Fatal("expected multipart")
	}
}
```

- [ ] **Step 4: Implement multipart detection**

Create `internal/gateway/multipart.go`:

```go
package gateway

import (
	"mime"
	"net/http"
	"strings"
)

func isMultipart(req *http.Request) bool {
	contentType := req.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.HasPrefix(contentType, "multipart/")
	}
	return strings.HasPrefix(mediaType, "multipart/")
}
```

- [ ] **Step 5: Verify streaming and multipart tests**

Run:

```bash
go test ./internal/gateway -run 'TestTeeStream|TestDetectMultipart' -v
```

Expected: PASS.

- [ ] **Step 6: Commit capture helpers**

Run:

```bash
git add internal/gateway/stream.go internal/gateway/stream_test.go internal/gateway/multipart.go internal/gateway/multipart_test.go
git commit -m "feat: add streaming and multipart capture helpers"
```

Expected: commit succeeds.

---

### Task 10: Job Envelope and Python Worker Contract

**Files:**
- Create: `internal/jobs/jobs.go`
- Create: `internal/jobs/jobs_test.go`
- Create: `workers/analysis_worker/pyproject.toml`
- Create: `workers/analysis_worker/main.py`
- Create: `workers/analysis_worker/contract_example.json`

- [ ] **Step 1: Write job envelope test**

Create `internal/jobs/jobs_test.go`:

```go
package jobs

import (
	"encoding/json"
	"testing"
)

func TestTraceCapturedJobJSON(t *testing.T) {
	job := TraceCapturedJob{TraceID: "trace_1", RoutePattern: "/v1/chat/completions", CaptureMode: "raw_and_normalized"}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if string(data) == "{}" {
		t.Fatal("unexpected empty JSON")
	}
}
```

- [ ] **Step 2: Implement Go job models**

Create `internal/jobs/jobs.go`:

```go
package jobs

import "context"

type TraceCapturedJob struct {
	Type           string `json:"type"`
	TraceID        string `json:"trace_id"`
	RoutePattern   string `json:"route_pattern"`
	ProtocolFamily string `json:"protocol_family"`
	CaptureMode    string `json:"capture_mode"`
	EmployeeNo      string `json:"employee_no"`
}

type Publisher interface {
	PublishTraceCaptured(ctx context.Context, job TraceCapturedJob) error
}

func NewTraceCaptured(traceID, routePattern, protocolFamily, captureMode, employeeNo string) TraceCapturedJob {
	return TraceCapturedJob{Type: "trace_captured", TraceID: traceID, RoutePattern: routePattern, ProtocolFamily: protocolFamily, CaptureMode: captureMode, EmployeeNo: employeeNo}
}
```

- [ ] **Step 3: Add Python worker uv project**

Create `workers/analysis_worker/pyproject.toml`:

```toml
[project]
name = "new-api-gateway-analysis-worker"
version = "0.1.0"
description = "Analysis worker contracts for the new-api audit gateway"
requires-python = ">=3.11"
dependencies = []

[tool.uv]
package = false
```

- [ ] **Step 4: Add Python worker contract sample**

Create `workers/analysis_worker/contract_example.json`:

```json
{
  "type": "trace_captured",
  "trace_id": "trace_example",
  "route_pattern": "/v1/chat/completions",
  "protocol_family": "openai_chat",
  "capture_mode": "raw_and_normalized",
  "employee_no": "E12345"
}
```

- [ ] **Step 5: Add Python worker skeleton**

Create `workers/analysis_worker/main.py`:

```python
import json
import sys
from dataclasses import dataclass


@dataclass(frozen=True)
class TraceCapturedJob:
    type: str
    trace_id: str
    route_pattern: str
    protocol_family: str
    capture_mode: str
    employee_no: str


def parse_job(line: str) -> TraceCapturedJob:
    data = json.loads(line)
    return TraceCapturedJob(**data)


def main() -> int:
    for line in sys.stdin:
        if not line.strip():
            continue
        job = parse_job(line)
        print(json.dumps({"accepted_trace_id": job.trace_id, "worker_status": "accepted"}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
```

- [ ] **Step 6: Verify Go and Python contracts with uv**

Run:

```bash
go test ./internal/jobs -v
(cd workers/analysis_worker && uv sync && uv run python main.py < contract_example.json)
```

Expected: Go tests PASS and Python prints `{"accepted_trace_id": "trace_example", "worker_status": "accepted"}`.

- [ ] **Step 7: Commit job contract**

Run:

```bash
git add internal/jobs workers/analysis_worker
git commit -m "feat: define analysis job contract"
```

Expected: commit succeeds.

---

### Task 11: Core Smoke Test and Documentation

**Files:**
- Create: `docs/development.md`
- Create: `scripts/smoke_proxy.sh`
- Modify: `Makefile`

- [ ] **Step 1: Add development documentation**

Create `docs/development.md`:

```markdown
# Development

## Local Services

Start PostgreSQL and Redis:

```bash
docker compose -f deploy/docker-compose.yml up -d
```

## Tests

```bash
make test
```

## Python Worker

The analysis worker uses uv for Python dependency management:

```bash
cd workers/analysis_worker
uv sync
uv run python main.py < contract_example.json
```

## Gateway Environment

Copy `.env.example` to `.env.local` and set `NEW_API_BASE_URL` to a running new-api instance.

The gateway must never log or persist plaintext API keys. Tests should assert that API-key handling only stores HMAC fingerprints and token metadata.
```

- [ ] **Step 2: Add smoke proxy script**

Create `scripts/smoke_proxy.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

: "${AUDIT_GATEWAY_URL:=http://localhost:8080}"
: "${NEW_API_KEY:?Set NEW_API_KEY to a new-api token for smoke testing}"

curl -sS "$AUDIT_GATEWAY_URL/v1/chat/completions" \
  -H "Authorization: Bearer $NEW_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}'
```

Run:

```bash
chmod +x scripts/smoke_proxy.sh
```

- [ ] **Step 3: Extend Makefile**

Modify `Makefile`:

```makefile
.PHONY: test run tidy smoke

test:
	go test ./...

run:
	go run ./cmd/audit-gateway

tidy:
	go mod tidy

smoke:
	./scripts/smoke_proxy.sh
```

- [ ] **Step 4: Run final tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Run formatting**

Run:

```bash
gofmt -w cmd internal
```

Expected: no output.

- [ ] **Step 6: Commit docs and smoke script**

Run:

```bash
git add docs/development.md scripts/smoke_proxy.sh Makefile
git commit -m "docs: add gateway core development guide"
```

Expected: commit succeeds.

---

## Self-Review Checklist

Spec coverage for this first implementation slice:

- Transparent independent gateway: Tasks 1, 7, 10.
- API-key extraction and HMAC fingerprinting: Task 2.
- `tokens.name` as `employee_no`: Task 3.
- Identity cache/lookup interfaces: Task 3; concrete Redis cache and new-api PostgreSQL lookup: Task 4.
- Route Registry and raw/minimal classification: Task 5.
- Raw evidence storage: Task 6.
- Trace metadata schema/repository: Task 7.
- JSON proxy capture: Task 8.
- SSE/multipart helper contracts: Task 9.
- Analysis job handoff: Task 10.
- Development verification: Task 11.

Out of scope for this plan and covered by separate plans:

- Admin UI and RBAC screens.
- Full Python normalizers and anomaly detectors.
- Usage aggregation dashboards.
- Coverage alert inbox UI.
- SSO/OIDC.
- Production object storage beyond the filesystem-backed development store.

Type consistency notes:

- Employee identity field is consistently `employee_no`.
- new-api users are not used.
- API-key persistent identifier is consistently `token_fingerprint` plus `fingerprint_display`.
- Route support uses `CaptureRawAndNormalized`, `CaptureRawAndMinimal`, and `CaptureRawOnly`.
