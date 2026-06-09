# Usage Page Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the admin `用量` page so it shows useful 30-day global usage data by default, supports fuzzy employee search with in-page detail expansion, and fixes the `1d / 7d / 30d` trend range behavior.

**Architecture:** Keep the existing `/admin/api/usage` entrypoint, but expand it to always return a new `global_usage` payload and optionally return `employee_usage` when an employee is selected. Add one focused employee-search endpoint, move usage-page-specific rendering into a small browser helper that can be unit-tested with Node, and let `app.js` focus on data loading, state transitions, chart binding, and event wiring.

**Tech Stack:** Go `net/http` + `pgx`, embedded admin UI assets via Go `embed`, vanilla browser JavaScript, Node built-in `node:test`, Go unit tests with `go test`.

---

## File Structure

- Modify: `internal/admin/models.go`
  Purpose: add global-usage, employee-search, and trend-point structs that match the new API contract.
- Modify: `internal/admin/repository.go`
  Purpose: add repository queries for 30-day global usage, fuzzy employee search, and padded hourly/daily employee trend points.
- Modify: `internal/admin/repository_test.go`
  Purpose: lock the SQL shape and time-axis padding behavior before implementation.
- Modify: `internal/admin/handlers.go`
  Purpose: always return `global_usage` from `/admin/api/usage`, add range-to-bucket selection for `employee_usage`, and register a dedicated fuzzy employee search endpoint.
- Modify: `internal/admin/handlers_test.go`
  Purpose: cover the new handler JSON payloads and update the in-memory fake database for the extra repository calls.
- Create: `internal/adminui/usage_page.js`
  Purpose: pure helper for usage-page HTML generation, search suggestion rendering, and sparse-range copy; shared by browser code and Node tests.
- Create: `internal/adminui/usage_page.test.js`
  Purpose: Node coverage for the default global view, fuzzy search result rendering, and expanded employee detail copy.
- Modify: `internal/adminui/index.html`
  Purpose: load the new usage-page helper before `app.js`.
- Modify: `internal/adminui/static.go`
  Purpose: embed `usage_page.js` into the Go binary.
- Modify: `internal/adminui/app.js`
  Purpose: replace the current username-gated empty state with the new global/default flow, search suggestion loading, in-page employee detail expansion, and range-specific chart rendering.
- Modify: `internal/adminui/app.css`
  Purpose: add layout and interaction styles for the global summary grid, employee leaderboard, fuzzy search suggestions, and detail panel.
- Review only: `README.md`, `ARCHITECTURE.md`, `CLAUDE.md`
  Purpose: verify that no docs still describe the old “must enter username first” behavior; only edit if text is stale.

## Task 1: Create the Required Worktree Before Touching Product Code

**Files:**
- Create: `.worktrees/usage-page-redesign` (git worktree directory)

- [ ] **Step 1: Create the dedicated worktree from `main`**

Run:

```bash
git worktree add -b codex/usage-page-redesign .worktrees/usage-page-redesign HEAD
```

Expected: Git prints `Preparing worktree` and checks out a new branch named `codex/usage-page-redesign`.

- [ ] **Step 2: Move into the worktree and verify the branch**

Run:

```bash
cd .worktrees/usage-page-redesign
git branch --show-current
```

Expected: output is exactly:

```text
codex/usage-page-redesign
```

- [ ] **Step 3: Verify the worktree starts clean enough for implementation**

Run:

```bash
git status --short
```

Expected: no tracked product files are modified before implementation begins.

## Task 2: Lock the Repository Contract with Failing Go Tests

**Files:**
- Modify: `internal/admin/repository_test.go`
- Test: `internal/admin/repository_test.go`

- [ ] **Step 1: Add failing repository tests for hourly padding, global usage, and fuzzy employee search**

Append these tests to `internal/admin/repository_test.go`:

```go
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
```

- [ ] **Step 2: Run the focused repository tests and verify they fail**

Run:

```bash
go test ./internal/admin -run 'TestRepository(EmployeeUsageTrendPadsHourlyPointsFor1D|GlobalUsageSummaryBuildsTopEmployeeAndModelLists|SearchUsageEmployeesMatchesUsernameAndDisplayName)' -count=1
```

Expected: FAIL to compile because `Points`, `BucketSize`, `ExpectedBuckets`, `GlobalUsageSummary`, `SearchUsageEmployees`, and related types do not exist yet.

## Task 3: Lock the Handler Contract with Failing Go Tests

**Files:**
- Modify: `internal/admin/handlers_test.go`
- Test: `internal/admin/handlers_test.go`

- [ ] **Step 1: Add failing handler tests for default global usage, fuzzy search, and hourly `1d` selection**

Append these tests to `internal/admin/handlers_test.go`:

```go
func TestUsageWithoutUsernameReturnsGlobalUsage(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/usage", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		GlobalUsage GlobalUsageSummary `json:"global_usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.GlobalUsage.TotalTokens == 0 {
		t.Fatalf("global_usage not populated: %s", rec.Body.String())
	}
	if db.employeeUsageCalled {
		t.Fatal("employee usage should stay idle when username is absent")
	}
}

func TestUsageWith1DRangeRequestsHourlyEmployeeUsage(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	handler.auth.Now = func() time.Time { return now }
	req := httptest.NewRequest(http.MethodGet, "/admin/api/usage?username=roy.zhang&range=1d", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if db.employeeUsageFilter.BucketSize != "hour" {
		t.Fatalf("BucketSize=%q, want hour", db.employeeUsageFilter.BucketSize)
	}
	if db.employeeUsageFilter.ExpectedBuckets != 24 {
		t.Fatalf("ExpectedBuckets=%d, want 24", db.employeeUsageFilter.ExpectedBuckets)
	}
}

func TestUsageEmployeesSearchReturnsFuzzyCandidates(t *testing.T) {
	handler, _, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/usage-employees?q=roy", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Employees []UsageEmployeeSearchResult `json:"employees"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Employees) == 0 || body.Employees[0].Username == "" {
		t.Fatalf("employees=%#v", body.Employees)
	}
}
```

- [ ] **Step 2: Run the focused handler tests and verify they fail**

Run:

```bash
go test ./internal/admin -run 'TestUsage(WithoutUsernameReturnsGlobalUsage|With1DRangeRequestsHourlyEmployeeUsage|EmployeesSearchReturnsFuzzyCandidates)' -count=1
```

Expected: FAIL because the handler does not yet return `global_usage`, does not route `/admin/api/usage-employees`, and does not set hourly range metadata.

## Task 4: Implement the Backend Contract and Make the Go Tests Pass

**Files:**
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/repository.go`
- Modify: `internal/admin/repository_test.go`
- Modify: `internal/admin/handlers.go`
- Modify: `internal/admin/handlers_test.go`
- Test: `internal/admin/repository_test.go`
- Test: `internal/admin/handlers_test.go`

- [ ] **Step 1: Add the new usage API models**

In `internal/admin/models.go`, replace the current employee-trend-only shapes with explicit global, search, and point models:

```go
type UsageEmployeeSearchFilter struct {
	Query string
	Limit int
}

type UsageEmployeeSearchResult struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Department  string `json:"department"`
	LastSeenAt  string `json:"last_seen_at"`
}

type GlobalUsageEmployee struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Department  string `json:"department"`
	TotalTokens int64  `json:"total_tokens"`
	RequestCount int64 `json:"request_count"`
	LastSeenAt  string `json:"last_seen_at"`
}

type GlobalUsageSummary struct {
	Window          string              `json:"window"`
	TotalTokens     int64               `json:"total_tokens"`
	ActiveEmployees int64               `json:"active_employees"`
	RequestCount    int64               `json:"request_count"`
	ActiveModels    int64               `json:"active_models"`
	TopEmployees    []GlobalUsageEmployee `json:"top_employees"`
	TopModels       []UsageModelSummary `json:"top_models"`
}

type UsageTrendPoint struct {
	BucketStart      string `json:"bucket_start"`
	BucketSize       string `json:"bucket_size"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	ErrorCount       int64  `json:"error_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

type EmployeeUsageFilter struct {
	Username        string
	Range           string
	Model           string
	Start           time.Time
	End             time.Time
	BucketSize      string
	ExpectedBuckets int
}

type EmployeeUsageTrend struct {
	Username            string              `json:"username"`
	Range               string              `json:"range"`
	BucketSize          string              `json:"bucket_size"`
	ExpectedBucketCount int                 `json:"expected_bucket_count"`
	ActiveBucketCount   int                 `json:"active_bucket_count"`
	SelectedModel       string              `json:"selected_model"`
	Models              []string            `json:"models"`
	Summary             UsageTokenSummary   `json:"summary"`
	Points              []UsageTrendPoint   `json:"points"`
	ModelSummary        []UsageModelSummary `json:"model_summary"`
}
```

- [ ] **Step 2: Implement repository queries for global usage, fuzzy employee search, and padded points**

In `internal/admin/repository.go`, add these three pieces:

```go
func (r Repository) GlobalUsageSummary(ctx context.Context, now time.Time) (GlobalUsageSummary, error) {
	start := now.UTC().AddDate(0, 0, -30)
	var summary GlobalUsageSummary
	summary.Window = "30d"
	err := r.db.QueryRow(ctx, `
SELECT
  COALESCE(SUM(total_tokens), 0),
  COUNT(DISTINCT username),
  COALESCE(SUM(request_count), 0),
  COUNT(DISTINCT model)
FROM usage_aggregates
WHERE bucket_size = 'day'
  AND bucket_start >= $1
  AND bucket_start < $2`, start, now.UTC()).Scan(
		&summary.TotalTokens,
		&summary.ActiveEmployees,
		&summary.RequestCount,
		&summary.ActiveModels,
	)
	if err != nil {
		return summary, err
	}
	rows, err := r.db.Query(ctx, `
SELECT username, COALESCE(MAX(token_name_snapshot), ''), COALESCE(MAX(route_pattern), ''), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(request_count), 0), MAX(bucket_start)::text
FROM usage_aggregates
WHERE bucket_size = 'day'
  AND bucket_start >= $1
  AND bucket_start < $2
  AND username <> ''
GROUP BY username
ORDER BY SUM(total_tokens) DESC, username ASC
LIMIT 10`, start, now.UTC())
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	for rows.Next() {
		var item GlobalUsageEmployee
		if err := rows.Scan(&item.Username, &item.DisplayName, &item.Department, &item.TotalTokens, &item.RequestCount, &item.LastSeenAt); err != nil {
			return summary, err
		}
		summary.TopEmployees = append(summary.TopEmployees, item)
	}
	modelRows, err := r.db.Query(ctx, `
SELECT model, COALESCE(SUM(request_count), 0), COALESCE(SUM(success_count), 0), COALESCE(SUM(error_count), 0), COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(completion_tokens), 0), COALESCE(SUM(cached_tokens), 0), COALESCE(SUM(total_tokens), 0)
FROM usage_aggregates
WHERE bucket_size = 'day'
  AND bucket_start >= $1
  AND bucket_start < $2
GROUP BY model
ORDER BY SUM(total_tokens) DESC, model ASC
LIMIT 10`, start, now.UTC())
	if err != nil {
		return summary, err
	}
	defer modelRows.Close()
	for modelRows.Next() {
		var item UsageModelSummary
		if err := modelRows.Scan(&item.Model, &item.RequestCount, &item.SuccessCount, &item.ErrorCount, &item.PromptTokens, &item.CompletionTokens, &item.CachedTokens, &item.TotalTokens); err != nil {
			return summary, err
		}
		summary.TopModels = append(summary.TopModels, item)
	}
	return summary, nil
}

func (r Repository) SearchUsageEmployees(ctx context.Context, filter UsageEmployeeSearchFilter) ([]UsageEmployeeSearchResult, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 8 {
		limit = 8
	}
	query := "%" + strings.TrimSpace(filter.Query) + "%"
	rows, err := r.db.Query(ctx, `
SELECT c.username, COALESCE(s.display_name, ''), COALESCE(s.department, c.department), MAX(c.last_seen_at)::text
FROM token_identity_cache c
LEFT JOIN audit_subjects s ON s.username = c.username
WHERE c.username ILIKE $1 OR COALESCE(s.display_name, '') ILIKE $1
GROUP BY c.username, COALESCE(s.display_name, ''), COALESCE(s.department, c.department)
ORDER BY MAX(c.last_seen_at) DESC, c.username ASC
LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []UsageEmployeeSearchResult{}
	for rows.Next() {
		var item UsageEmployeeSearchResult
		if err := rows.Scan(&item.Username, &item.DisplayName, &item.Department, &item.LastSeenAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func fillUsageTrendPoints(start time.Time, expectedBuckets int, bucketSize string, raw []UsageTrendPoint) ([]UsageTrendPoint, int) {
	step := 24 * time.Hour
	layout := time.RFC3339
	if bucketSize == "hour" {
		step = time.Hour
	}
	byBucket := make(map[string]UsageTrendPoint, len(raw))
	active := 0
	for _, point := range raw {
		byBucket[point.BucketStart] = point
		if point.TotalTokens > 0 || point.RequestCount > 0 {
			active++
		}
	}
	points := make([]UsageTrendPoint, 0, expectedBuckets)
	for i := 0; i < expectedBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * step).UTC().Format(layout)
		point, ok := byBucket[bucketStart]
		if !ok {
			point = UsageTrendPoint{BucketStart: bucketStart, BucketSize: bucketSize}
		}
		points = append(points, point)
	}
	return points, active
}
```

Then update `EmployeeUsageTrend` to:

```go
- query `bucket_size = $4` instead of hard-coding `day`
- scan raw rows into `[]UsageTrendPoint`
- call `fillUsageTrendPoints(filter.Start, filter.ExpectedBuckets, filter.BucketSize, rawPoints)`
- set `trend.Points`, `trend.BucketSize`, `trend.ExpectedBucketCount`, and `trend.ActiveBucketCount`
```

- [ ] **Step 3: Update handler routing and JSON shape**

In `internal/admin/handlers.go`:

```go
func (h Handler) routes() *http.ServeMux {
	mux := http.NewServeMux()
	// ...
	mux.Handle("GET /admin/api/usage", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listUsage))))
	mux.Handle("GET /admin/api/usage-employees", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listUsageEmployees))))
	// ...
}

func usageTrendWindow(value string, now time.Time) (string, time.Time, time.Time, string, int) {
	switch strings.TrimSpace(value) {
	case "1d":
		return "1d", now.Add(-24 * time.Hour), now, "hour", 24
	case "7d":
		end := now.UTC().Truncate(24 * time.Hour).AddDate(0, 0, 1)
		return "7d", end.AddDate(0, 0, -7), end, "day", 7
	default:
		end := now.UTC().Truncate(24 * time.Hour).AddDate(0, 0, 1)
		return "30d", end.AddDate(0, 0, -30), end, "day", 30
	}
}

func (h Handler) listUsage(w http.ResponseWriter, r *http.Request) {
	now := h.auth.now()
	globalUsage, err := h.repo.GlobalUsageSummary(r.Context(), now)
	if err != nil {
		http.Error(w, "failed to load global usage", http.StatusInternalServerError)
		return
	}
	response := map[string]any{"global_usage": globalUsage}
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username != "" {
		rangeValue, start, end, bucketSize, expectedBuckets := usageTrendWindow(r.URL.Query().Get("range"), now)
		trend, err := h.repo.EmployeeUsageTrend(r.Context(), EmployeeUsageFilter{
			Username:        username,
			Range:           rangeValue,
			Model:           strings.TrimSpace(r.URL.Query().Get("model")),
			Start:           start,
			End:             end,
			BucketSize:      bucketSize,
			ExpectedBuckets: expectedBuckets,
		})
		if err != nil {
			http.Error(w, "failed to load employee usage", http.StatusInternalServerError)
			return
		}
		response["employee_usage"] = trend
	}
	writeJSON(w, http.StatusOK, response)
}

func (h Handler) listUsageEmployees(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.SearchUsageEmployees(r.Context(), UsageEmployeeSearchFilter{
		Query: strings.TrimSpace(r.URL.Query().Get("q")),
		Limit: 8,
	})
	if err != nil {
		http.Error(w, "failed to search employees", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"employees": items})
}
```

- [ ] **Step 4: Extend the in-memory test database to answer the new queries**

In `internal/admin/handlers_test.go`, update `memoryAdminDB.Query` so it can satisfy:

```go
- the `GlobalUsageSummary` aggregate query
- the `TopEmployees` query
- the `TopModels` query
- the `usage-employees` fuzzy search query
- the revised `EmployeeUsageTrend` hourly/day query branches
```

Use concrete scan outputs like:

```go
if strings.Contains(sql, "GROUP BY c.username") && strings.Contains(sql, "ILIKE") {
	return &scanRows{scans: []func(dest ...any) error{
		func(dest ...any) error {
			*(dest[0].(*string)) = "roy.zhang"
			*(dest[1].(*string)) = "Roy Zhang"
			*(dest[2].(*string)) = "Platform"
			*(dest[3].(*string)) = "2026-06-05 08:00:00+00"
			return nil
		},
	}}, nil
}
```

- [ ] **Step 5: Run the backend tests and make sure they pass**

Run:

```bash
go test ./internal/admin -run 'Test(RepositoryEmployeeUsageTrendPadsHourlyPointsFor1D|RepositoryGlobalUsageSummaryBuildsTopEmployeeAndModelLists|RepositorySearchUsageEmployeesMatchesUsernameAndDisplayName|UsageWithoutUsernameReturnsGlobalUsage|UsageWith1DRangeRequestsHourlyEmployeeUsage|UsageEmployeesSearchReturnsFuzzyCandidates)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the backend contract**

Run:

```bash
git add internal/admin/models.go internal/admin/repository.go internal/admin/repository_test.go internal/admin/handlers.go internal/admin/handlers_test.go
git commit -m "feat(admin): add usage landing and employee search api"
```

Expected: Git creates one commit containing only the admin backend contract changes.

## Task 5: Lock the Usage-Page Browser Helper with Failing Node Tests

**Files:**
- Create: `internal/adminui/usage_page.test.js`
- Test: `internal/adminui/usage_page.test.js`

- [ ] **Step 1: Create failing Node tests for the default view, fuzzy results, and detail hint**

Create `internal/adminui/usage_page.test.js` with:

```js
const test = require("node:test");
const assert = require("node:assert/strict");

const {
  renderUsagePage,
  formatActiveBucketHint,
} = require("./usage_page.js");

test("renderUsagePage shows global usage before an employee is selected", () => {
  const html = renderUsagePage({
    global_usage: {
      total_tokens: 18420,
      active_employees: 17,
      request_count: 42,
      active_models: 6,
      top_employees: [
        { username: "roy.zhang", display_name: "Roy Zhang", department: "Platform", total_tokens: 9000, request_count: 12, last_seen_at: "2026-06-05 08:00:00+00" },
      ],
      top_models: [
        { model: "gpt-5.2", total_tokens: 12000, request_count: 21, success_count: 21, error_count: 0, prompt_tokens: 0, completion_tokens: 0, cached_tokens: 0 },
      ],
    },
    employee_usage: null,
    usageState: { searchQuery: "", searchResults: [], searchError: "", selectedEmployee: "" },
  });

  assert.match(html, /搜索员工/);
  assert.match(html, /Top 员工榜/);
  assert.match(html, /Roy Zhang/);
  assert.doesNotMatch(html, /当前查看：/);
});

test("renderUsagePage renders fuzzy search suggestions and expanded detail panel", () => {
  const html = renderUsagePage({
    global_usage: {
      total_tokens: 18420,
      active_employees: 17,
      request_count: 42,
      active_models: 6,
      top_employees: [],
      top_models: [],
    },
    employee_usage: {
      username: "roy.zhang",
      range: "1d",
      bucket_size: "hour",
      active_bucket_count: 1,
      expected_bucket_count: 24,
      selected_model: "",
      models: ["gpt-5.2"],
      summary: { request_count: 2, prompt_tokens: 100, completion_tokens: 40, cached_tokens: 0, total_tokens: 140 },
      points: [{ bucket_start: "2026-06-04T15:00:00Z", bucket_size: "hour", request_count: 2, success_count: 2, error_count: 0, prompt_tokens: 100, completion_tokens: 40, cached_tokens: 0, total_tokens: 140 }],
      model_summary: [{ model: "gpt-5.2", request_count: 2, success_count: 2, error_count: 0, prompt_tokens: 100, completion_tokens: 40, cached_tokens: 0, total_tokens: 140 }],
    },
    usageState: {
      searchQuery: "roy",
      searchResults: [{ username: "roy.zhang", display_name: "Roy Zhang", department: "Platform", last_seen_at: "2026-06-05 08:00:00+00" }],
      searchError: "",
      selectedEmployee: "roy.zhang",
    },
  });

  assert.match(html, /搜索建议/);
  assert.match(html, /当前查看：roy\.zhang/);
  assert.match(html, /仅 1 个时间桶有实际流量/);
  assert.match(html, /收起详情/);
});

test("formatActiveBucketHint matches sparse hourly and daily copy", () => {
  assert.equal(formatActiveBucketHint("1d", 1, 24), "当前范围内仅 1 个时间桶有实际流量");
  assert.equal(formatActiveBucketHint("30d", 2, 30), "当前范围内仅 2 个时间桶有实际流量");
  assert.equal(formatActiveBucketHint("30d", 30, 30), "");
});
```

- [ ] **Step 2: Run the Node tests and verify they fail**

Run:

```bash
node --test internal/adminui/usage_page.test.js
```

Expected: FAIL because `internal/adminui/usage_page.js` does not exist yet.

## Task 6: Implement the Usage-Page Helper and Wire It into the Embedded UI

**Files:**
- Create: `internal/adminui/usage_page.js`
- Modify: `internal/adminui/index.html`
- Modify: `internal/adminui/static.go`
- Test: `internal/adminui/usage_page.test.js`

- [ ] **Step 1: Create the shared helper module**

Create `internal/adminui/usage_page.js` with this UMD-style export so both Node tests and the browser can consume it:

```js
(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
  }
  root.UsagePage = api;
})(typeof globalThis !== "undefined" ? globalThis : window, function () {
  function escapeHTML(value) {
    return String(value ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#39;");
  }

  function compactNumber(value) {
    return Number(value || 0).toLocaleString();
  }

  function formatActiveBucketHint(range, activeCount, expectedCount) {
    if (!expectedCount || activeCount >= expectedCount) return "";
    return `当前范围内仅 ${activeCount} 个时间桶有实际流量`;
  }

  function renderSearchResults(results, selectedEmployee) {
    if (!Array.isArray(results) || results.length === 0) return "";
    return `
      <div class="usage-search-results">
        <div class="label">搜索建议</div>
        ${results.map((item) => `
          <button type="button" data-usage-select="${escapeHTML(item.username)}" class="${item.username === selectedEmployee ? "active" : ""}">
            <strong>${escapeHTML(item.display_name || item.username)}</strong>
            <span>${escapeHTML(item.username)}</span>
            <span>${escapeHTML(item.department || "")}</span>
          </button>
        `).join("")}
      </div>
    `;
  }

  function renderTopEmployees(items) {
    const rows = Array.isArray(items) ? items : [];
    if (rows.length === 0) {
      return `<div class="muted">最近 30 天暂无员工用量数据。</div>`;
    }
    return `
      <div class="usage-top-employees">
        ${rows.map((item) => `
          <button type="button" class="usage-top-employee" data-usage-top-employee="${escapeHTML(item.username)}">
            <strong>${escapeHTML(item.display_name || item.username)}</strong>
            <span>${escapeHTML(item.username)}</span>
            <span>${compactNumber(item.total_tokens)} tokens</span>
          </button>
        `).join("")}
      </div>
    `;
  }

  function renderTopModels(items) {
    const rows = Array.isArray(items) ? items : [];
    if (rows.length === 0) {
      return `<div class="muted">最近 30 天暂无模型用量数据。</div>`;
    }
    return `
      <table>
        <thead><tr><th>Model</th><th>请求数</th><th>Total</th></tr></thead>
        <tbody>
          ${rows.map((item) => `
            <tr>
              <td>${escapeHTML(item.model || "unknown")}</td>
              <td>${compactNumber(item.request_count)}</td>
              <td>${compactNumber(item.total_tokens)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    `;
  }

  function renderUsagePage(payload) {
    const globalUsage = payload.global_usage || {};
    const employeeUsage = payload.employee_usage || null;
    const usageState = payload.usageState || {};
    const hint = employeeUsage
      ? formatActiveBucketHint(employeeUsage.range, employeeUsage.active_bucket_count, employeeUsage.expected_bucket_count)
      : "";
    return `
      <section class="panel">
        <div class="field">
          <label for="usage-search-input">搜索员工</label>
          <input id="usage-search-input" data-usage-search-input value="${escapeHTML(usageState.searchQuery || "")}" placeholder="输入用户名或显示名">
        </div>
        ${usageState.searchError ? `<div class="error-inline">${escapeHTML(usageState.searchError)}</div>` : ""}
        ${renderSearchResults(usageState.searchResults || [], usageState.selectedEmployee || "")}
      </section>
      <section class="cards usage-summary">
        <article class="metric"><div class="label">30d 总 Token</div><div class="value">${compactNumber(globalUsage.total_tokens)}</div></article>
        <article class="metric"><div class="label">活跃员工</div><div class="value">${compactNumber(globalUsage.active_employees)}</div></article>
        <article class="metric"><div class="label">请求数</div><div class="value">${compactNumber(globalUsage.request_count)}</div></article>
        <article class="metric"><div class="label">活跃模型</div><div class="value">${compactNumber(globalUsage.active_models)}</div></article>
      </section>
      <section class="panel"><h2>Top 员工榜</h2>${renderTopEmployees(globalUsage.top_employees)}</section>
      <section class="panel"><h2>Top Models</h2>${renderTopModels(globalUsage.top_models)}</section>
      ${employeeUsage ? `<section class="panel usage-detail"><div class="detail-head"><h2>当前查看：${escapeHTML(employeeUsage.username)}</h2><button type="button" data-usage-clear>收起详情</button></div>${hint ? `<div class="muted">${escapeHTML(hint)}</div>` : ""}</section>` : ""}
    `;
  }

  return { renderUsagePage, formatActiveBucketHint };
});
```

- [ ] **Step 2: Load and embed the helper asset**

In `internal/adminui/index.html`, insert the helper before `app.js`:

```html
<script src="/admin/vendor/chartjs/chart.umd.min.js" defer></script>
<script src="/admin/usage_page.js" defer></script>
<script src="/admin/app.js" defer></script>
```

In `internal/adminui/static.go`, expand the embed directive:

```go
//go:embed index.html app.css app.js usage_page.js vendor/chartjs/chart.umd.min.js vendor/chartjs/LICENSE.md
var assets embed.FS
```

- [ ] **Step 3: Run the Node tests and make sure they pass**

Run:

```bash
node --test internal/adminui/usage_page.test.js
```

Expected: PASS.

- [ ] **Step 4: Commit the helper layer**

Run:

```bash
git add internal/adminui/usage_page.js internal/adminui/usage_page.test.js internal/adminui/index.html internal/adminui/static.go
git commit -m "feat(adminui): add usage page helper module"
```

Expected: Git creates a commit containing only the helper module, its tests, and the embed wiring.

## Task 7: Integrate the New Usage Flow into `app.js` and Style It

**Files:**
- Modify: `internal/adminui/app.js`
- Modify: `internal/adminui/app.css`
- Test: `internal/adminui/usage_page.test.js`
- Test: `internal/admin/handlers_test.go`

- [ ] **Step 1: Expand usage-page state and always load global usage**

In `internal/adminui/app.js`, replace the current usage state and loader setup with:

```js
usage: {
  selectedEmployee: "",
  searchQuery: "",
  searchResults: [],
  searchError: "",
  range: "30d",
  model: "",
  body: null,
},
```

Add a dedicated search sequence counter near `usageRequestSeq`:

```js
let usageRequestSeq = 0;
let usageSearchSeq = 0;
```

Update `loadUsage()` so it always requests `/usage`, and only includes employee-specific params when `selectedEmployee` is set:

```js
async function loadUsage() {
  const requestSeq = ++usageRequestSeq;
  const params = queryString({
    username: state.usage.selectedEmployee || "",
    range: state.usage.range || "30d",
    model: state.usage.model || "",
  });
  const body = await api(`/usage?${params}`);
  if (requestSeq !== usageRequestSeq) return;
  state.usage.body = body;
  renderUsage(body);
}
```

- [ ] **Step 2: Add fuzzy search suggestion loading and shared selection behavior**

In `internal/adminui/app.js`, add:

```js
async function loadUsageSearchResults(query) {
  const requestSeq = ++usageSearchSeq;
  const trimmed = String(query || "").trim();
  state.usage.searchQuery = trimmed;
  state.usage.searchError = "";
  if (!trimmed) {
    state.usage.searchResults = [];
    renderUsage(state.usage.body || {});
    return;
  }
  try {
    const body = await api(`/usage-employees?${queryString({ q: trimmed })}`);
    if (requestSeq !== usageSearchSeq) return;
    state.usage.searchResults = arrayValue(body.employees);
    renderUsage(state.usage.body || {});
  } catch (error) {
    if (requestSeq !== usageSearchSeq) return;
    state.usage.searchError = error.message || "搜索失败，请重试";
    state.usage.searchResults = [];
    renderUsage(state.usage.body || {});
  }
}

function selectUsageEmployee(username) {
  state.usage.selectedEmployee = username;
  state.usage.model = "";
  return reloadUsageView();
}
```

Then update `bindUsageSearch()` so input uses a small debounce and result selection uses the same code path as leaderboard clicks.

- [ ] **Step 3: Replace the old empty-state renderer with the helper-driven layout**

In `renderUsage(body = {})`, stop returning early when `username` is empty. Instead:

```js
function renderUsage(body = {}) {
  state.usage.body = body;
  const html = window.UsagePage.renderUsagePage({
    global_usage: body.global_usage || {},
    employee_usage: body.employee_usage || null,
    usageState: state.usage,
  });
  renderShell(page("用量", html));
  bindUsageSearch();
  bindUsageGlobalInteractions();
  bindUsageControls();
  renderEmployeeUsageChart(arrayValue(body.employee_usage?.points));
}
```

Create `bindUsageGlobalInteractions()` so these controls work:

```js
function bindUsageGlobalInteractions() {
  document.querySelectorAll("[data-usage-select], [data-usage-top-employee]").forEach((button) => {
    button.addEventListener("click", async () => {
      await selectUsageEmployee(button.dataset.usageSelect || button.dataset.usageTopEmployee || "");
    });
  });
  const clearButton = document.querySelector("[data-usage-clear]");
  if (clearButton) {
    clearButton.addEventListener("click", async () => {
      state.usage.selectedEmployee = "";
      state.usage.model = "";
      await reloadUsageView();
    });
  }
}
```

- [ ] **Step 4: Update chart rendering to consume `points` and hour/day labels**

Still in `internal/adminui/app.js`, change the employee trend pipeline from `daily` to `points`:

```js
function usageChart(points) {
  const items = arrayValue(points).map((item) => ({
    label: state.usage.range === "1d" ? formatTime(item.bucket_start || "") : String(item.bucket_start || ""),
    total: finiteNumber(item.total_tokens),
    input: finiteNumber(item.prompt_tokens),
    output: finiteNumber(item.completion_tokens),
    cache: finiteNumber(item.cached_tokens),
  }));
  // keep the existing chart shell and empty-state rules
}

function renderEmployeeUsageChart(points) {
  const items = arrayValue(points).map((item) => ({
    label: state.usage.range === "1d" ? formatTime(item.bucket_start || "") : String(item.bucket_start || ""),
    total: finiteNumber(item.total_tokens),
    input: finiteNumber(item.prompt_tokens),
    output: finiteNumber(item.completion_tokens),
    cache: finiteNumber(item.cached_tokens),
  }));
  // keep the existing line chart registration
}
```

- [ ] **Step 5: Add the new layout and interaction styles**

In `internal/adminui/app.css`, add styles for the new usage page structure:

```css
.usage-search-results {
  display: grid;
  gap: 8px;
  margin-top: 12px;
}

.usage-search-results button,
.usage-top-employee {
  width: 100%;
  text-align: left;
  border: 1px solid var(--line);
  border-radius: 12px;
  background: #fff;
  padding: 12px 14px;
}

.usage-detail {
  border-color: #bfdbfe;
  box-shadow: 0 12px 30px rgba(37, 99, 235, 0.08);
}

.detail-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.error-inline {
  color: #b42318;
  margin-top: 8px;
}
```

- [ ] **Step 6: Run the admin backend and Node tests together**

Run:

```bash
node --test internal/adminui/usage_page.test.js
go test ./internal/admin -count=1
```

Expected: both commands PASS.

- [ ] **Step 7: Commit the UI integration**

Run:

```bash
git add internal/adminui/app.js internal/adminui/app.css
git commit -m "feat(adminui): redesign usage page flow"
```

Expected: Git creates one commit containing the usage page orchestration and styles.

## Task 8: Review Documentation and Do Final Verification

**Files:**
- Review only: `README.md`
- Review only: `ARCHITECTURE.md`
- Review only: `CLAUDE.md`

- [ ] **Step 1: Search the docs for stale usage-page wording**

Run:

```bash
rg -n "用量|usage" README.md ARCHITECTURE.md CLAUDE.md
```

Expected: if no line claims the page requires entering a username first, make no doc edits. If stale wording exists, update only the affected lines.

- [ ] **Step 2: Run the highest-signal verification commands**

Run:

```bash
node --test internal/adminui/usage_page.test.js
go test ./internal/admin -count=1
go test ./internal/adminui/... -count=1
```

Expected: the Node tests pass, the admin package tests pass, and the static asset package compiles cleanly.

- [ ] **Step 3: Check the final worktree diff**

Run:

```bash
git status --short
```

Expected: only the intended usage-page files are modified; unrelated docs or product files are untouched.

- [ ] **Step 4: Create the final implementation commit**

Run:

```bash
git add internal/admin README.md ARCHITECTURE.md CLAUDE.md
git commit -m "feat(admin): redesign usage page"
```

Expected: Git creates the final feature commit, or reports `nothing to commit` if the earlier task-level commits already captured the full change set.
