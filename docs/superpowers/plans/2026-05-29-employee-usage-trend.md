# Employee Usage Trend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing admin "用量" page into an employee-first token usage trend view with 1d/7d/30d ranges, dynamic model filters, split token chart series, and exact model summary data.

**Architecture:** Reuse `usage_aggregates`; do not add migrations or new capture/worker behavior. Add typed employee trend response models and one repository method, extend `/admin/api/usage` to include `employee_usage` only when `username` is present, and update the vanilla JS admin page to render a self-contained SVG chart plus model summary table.

**Tech Stack:** Go, pgx, net/http, PostgreSQL, vanilla JavaScript, inline SVG, existing admin CSS.

---

## File Structure

- `internal/admin/models.go`: add `EmployeeUsageFilter`, `EmployeeUsageTrend`, `UsageTokenSummary`, `UsageDailyPoint`, and `UsageModelSummary`.
- `internal/admin/repository.go`: add `EmployeeUsageTrend` repository method.
- `internal/admin/repository_test.go`: add focused SQL/scan tests for the employee trend repository method.
- `internal/admin/handlers.go`: extend `listUsage` with range parsing and conditional `employee_usage` response.
- `internal/admin/handlers_test.go`: add handler tests for default range, valid ranges, model forwarding, and compatibility without username.
- `internal/adminui/app.js`: replace the generic usage table rendering with employee search, range/model state, SVG chart rendering, and model summary table.
- `internal/adminui/app.css`: add compact styles for range/model tabs, chart layout, legend, and single-point chart states.
- `README.md`, `ARCHITECTURE.md`, `CLAUDE.md`: inspect after implementation and update only if they describe admin usage as total-only or generic table-only.

## Task 1: Add Employee Usage Models And Repository Tests

**Files:**
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/repository_test.go`

- [ ] **Step 1: Add failing repository tests**

Append this test to `internal/admin/repository_test.go` after `TestRepositoryListUsageAggregatesCapsLimitAndBindsFilters`:

```go
func TestRepositoryEmployeeUsageTrendBuildsBoundedDailyQueries(t *testing.T) {
	db := &recordingAdminDB{
		rowsQueue: []pgx.Rows{
			&scanRows{},
			&scanRows{},
			&scanRows{},
		},
	}
	repo := NewRepository(db)
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)

	_, err := repo.EmployeeUsageTrend(context.Background(), EmployeeUsageFilter{
		Username: "E10001",
		Range:    "30d",
		Model:    "gpt-5.2",
		Start:    start,
		End:      end,
	})

	if err != nil {
		t.Fatalf("EmployeeUsageTrend error: %v", err)
	}
	joined := strings.Join(db.queryLog, "\n")
	if strings.Count(joined, "FROM usage_aggregates") != 3 {
		t.Fatalf("expected 3 usage_aggregates queries, got:\n%s", joined)
	}
	for _, required := range []string{
		"bucket_size = 'day'",
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
	if got := db.queryArgsLog[1][3]; got != "gpt-5.2" {
		t.Fatalf("daily model arg = %#v, want gpt-5.2", got)
	}
	if got := db.queryArgsLog[2][3]; got != "gpt-5.2" {
		t.Fatalf("model summary arg = %#v, want gpt-5.2", got)
	}
}

func TestRepositoryEmployeeUsageTrendScansModelsDailyAndSummary(t *testing.T) {
	db := &recordingAdminDB{
		rowsQueue: []pgx.Rows{
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
		},
	}
	repo := NewRepository(db)

	trend, err := repo.EmployeeUsageTrend(context.Background(), EmployeeUsageFilter{
		Username: "E10001",
		Range:    "30d",
		Start:    time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
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
	if len(trend.Daily) != 1 || trend.Daily[0].PromptTokens != 1200 || trend.Daily[0].TotalTokens != 1540 {
		t.Fatalf("daily = %#v", trend.Daily)
	}
	if len(trend.ModelSummary) != 1 || trend.ModelSummary[0].Model != "gpt-5.2" {
		t.Fatalf("model summary = %#v", trend.ModelSummary)
	}
	if trend.Summary.RequestCount != 12 || trend.Summary.PromptTokens != 1200 || trend.Summary.TotalTokens != 1540 {
		t.Fatalf("summary = %#v", trend.Summary)
	}
}
```

Update `recordingAdminDB` in the same file so it can record multiple query calls:

```go
type recordingAdminDB struct {
	sql          string
	args         []any
	querySQL     string
	queryArgs    []any
	queryLog     []string
	queryArgsLog [][]any
	rows         pgx.Rows
	rowsQueue    []pgx.Rows
	rowQueue     []pgx.Row
}

func (db *recordingAdminDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.querySQL = sql
	db.queryArgs = args
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
```

- [ ] **Step 2: Run repository tests and verify failure**

Run:

```bash
go test ./internal/admin/ -run 'TestRepositoryEmployeeUsageTrend|TestRepositoryListUsageAggregatesCapsLimitAndBindsFilters' -count=1
```

Expected: FAIL because `EmployeeUsageFilter` and `EmployeeUsageTrend` do not exist.

- [ ] **Step 3: Add employee usage model types**

In `internal/admin/models.go`, add these types after `UsageBucket`:

```go
type EmployeeUsageFilter struct {
	Username string
	Range    string
	Model    string
	Start    time.Time
	End      time.Time
}

type UsageTokenSummary struct {
	RequestCount     int64 `json:"request_count"`
	SuccessCount     int64 `json:"success_count"`
	ErrorCount       int64 `json:"error_count"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CachedTokens     int64 `json:"cached_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type UsageDailyPoint struct {
	BucketStart      string `json:"bucket_start"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	ErrorCount       int64  `json:"error_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

type UsageModelSummary struct {
	Model            string `json:"model"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	ErrorCount       int64  `json:"error_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

type EmployeeUsageTrend struct {
	Username      string              `json:"username"`
	Range         string              `json:"range"`
	SelectedModel string              `json:"selected_model"`
	Models        []string            `json:"models"`
	Summary       UsageTokenSummary   `json:"summary"`
	Daily         []UsageDailyPoint   `json:"daily"`
	ModelSummary  []UsageModelSummary `json:"model_summary"`
}
```

- [ ] **Step 4: Implement repository method**

In `internal/admin/repository.go`, add this method after `ListUsageAggregates`:

```go
func (r Repository) EmployeeUsageTrend(ctx context.Context, filter EmployeeUsageFilter) (EmployeeUsageTrend, error) {
	if r.db == nil {
		return EmployeeUsageTrend{}, ErrAdminDBRequired
	}
	trend := EmployeeUsageTrend{
		Username:      filter.Username,
		Range:         filter.Range,
		SelectedModel: filter.Model,
		Models:        []string{},
		Daily:         []UsageDailyPoint{},
		ModelSummary:  []UsageModelSummary{},
	}
	if strings.TrimSpace(filter.Username) == "" {
		return trend, nil
	}

	modelRows, err := r.db.Query(ctx, `
SELECT DISTINCT model
FROM usage_aggregates
WHERE username = $1
  AND bucket_start >= $2
  AND bucket_start < $3
  AND bucket_size = 'day'
  AND model <> ''
ORDER BY model ASC`, filter.Username, filter.Start, filter.End)
	if err != nil {
		return EmployeeUsageTrend{}, err
	}
	defer modelRows.Close()
	for modelRows.Next() {
		var model string
		if err := modelRows.Scan(&model); err != nil {
			return EmployeeUsageTrend{}, err
		}
		trend.Models = append(trend.Models, model)
	}
	if err := modelRows.Err(); err != nil {
		return EmployeeUsageTrend{}, err
	}

	dailyWhere := `
WHERE username = $1
  AND bucket_start >= $2
  AND bucket_start < $3
  AND bucket_size = 'day'`
	dailyArgs := []any{filter.Username, filter.Start, filter.End}
	if strings.TrimSpace(filter.Model) != "" {
		dailyArgs = append(dailyArgs, filter.Model)
		dailyWhere += fmt.Sprintf("\n  AND model = $%d", len(dailyArgs))
	}
	dailyQuery := fmt.Sprintf(`
SELECT bucket_start::text,
       COALESCE(SUM(request_count), 0),
       COALESCE(SUM(success_count), 0),
       COALESCE(SUM(error_count), 0),
       COALESCE(SUM(prompt_tokens), 0),
       COALESCE(SUM(completion_tokens), 0),
       COALESCE(SUM(cached_tokens), 0),
       COALESCE(SUM(total_tokens), 0)
FROM usage_aggregates
%s
GROUP BY bucket_start
ORDER BY bucket_start ASC`, dailyWhere)
	dailyRows, err := r.db.Query(ctx, dailyQuery, dailyArgs...)
	if err != nil {
		return EmployeeUsageTrend{}, err
	}
	defer dailyRows.Close()
	for dailyRows.Next() {
		var point UsageDailyPoint
		if err := dailyRows.Scan(
			&point.BucketStart,
			&point.RequestCount,
			&point.SuccessCount,
			&point.ErrorCount,
			&point.PromptTokens,
			&point.CompletionTokens,
			&point.CachedTokens,
			&point.TotalTokens,
		); err != nil {
			return EmployeeUsageTrend{}, err
		}
		trend.Daily = append(trend.Daily, point)
		trend.Summary.RequestCount += point.RequestCount
		trend.Summary.SuccessCount += point.SuccessCount
		trend.Summary.ErrorCount += point.ErrorCount
		trend.Summary.PromptTokens += point.PromptTokens
		trend.Summary.CompletionTokens += point.CompletionTokens
		trend.Summary.CachedTokens += point.CachedTokens
		trend.Summary.TotalTokens += point.TotalTokens
	}
	if err := dailyRows.Err(); err != nil {
		return EmployeeUsageTrend{}, err
	}

	modelWhere := dailyWhere
	modelArgs := append([]any(nil), dailyArgs...)
	modelQuery := fmt.Sprintf(`
SELECT model,
       COALESCE(SUM(request_count), 0),
       COALESCE(SUM(success_count), 0),
       COALESCE(SUM(error_count), 0),
       COALESCE(SUM(prompt_tokens), 0),
       COALESCE(SUM(completion_tokens), 0),
       COALESCE(SUM(cached_tokens), 0),
       COALESCE(SUM(total_tokens), 0)
FROM usage_aggregates
%s
GROUP BY model
ORDER BY SUM(total_tokens) DESC, model ASC`, modelWhere)
	summaryRows, err := r.db.Query(ctx, modelQuery, modelArgs...)
	if err != nil {
		return EmployeeUsageTrend{}, err
	}
	defer summaryRows.Close()
	for summaryRows.Next() {
		var item UsageModelSummary
		if err := summaryRows.Scan(
			&item.Model,
			&item.RequestCount,
			&item.SuccessCount,
			&item.ErrorCount,
			&item.PromptTokens,
			&item.CompletionTokens,
			&item.CachedTokens,
			&item.TotalTokens,
		); err != nil {
			return EmployeeUsageTrend{}, err
		}
		trend.ModelSummary = append(trend.ModelSummary, item)
	}
	return trend, summaryRows.Err()
}
```

- [ ] **Step 5: Run repository tests and verify pass**

Run:

```bash
go test ./internal/admin/ -run 'TestRepositoryEmployeeUsageTrend|TestRepositoryListUsageAggregatesCapsLimitAndBindsFilters' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit repository slice**

Run:

```bash
git add internal/admin/models.go internal/admin/repository.go internal/admin/repository_test.go
git commit -m "feat(admin): add employee usage trend query"
```

## Task 2: Extend Usage Handler Response

**Files:**
- Modify: `internal/admin/handlers.go`
- Modify: `internal/admin/handlers_test.go`

- [ ] **Step 1: Add failing handler tests**

Add these tests near `TestOverviewRequiresAggregatePermission` in `internal/admin/handlers_test.go`:

```go
func TestUsageWithUsernameReturnsEmployeeUsage(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	handler.auth.Now = func() time.Time { return now }
	req := httptest.NewRequest(http.MethodGet, "/admin/api/usage?username=E10001&range=bad&model=gpt-5.2", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if db.employeeUsageFilter.Username != "E10001" || db.employeeUsageFilter.Model != "gpt-5.2" {
		t.Fatalf("filter = %#v", db.employeeUsageFilter)
	}
	if !db.employeeUsageFilter.End.Equal(now) {
		t.Fatalf("end = %s, want %s", db.employeeUsageFilter.End, now)
	}
	if !db.employeeUsageFilter.Start.Equal(now.AddDate(0, 0, -30)) {
		t.Fatalf("start = %s, want %s", db.employeeUsageFilter.Start, now.AddDate(0, 0, -30))
	}
	var body struct {
		EmployeeUsage EmployeeUsageTrend `json:"employee_usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.EmployeeUsage.Username != "E10001" || body.EmployeeUsage.Summary.TotalTokens != 15 {
		t.Fatalf("employee_usage = %#v", body.EmployeeUsage)
	}
	if body.EmployeeUsage.Range != "30d" || body.EmployeeUsage.SelectedModel != "gpt-5.2" {
		t.Fatalf("employee_usage range/model = %#v", body.EmployeeUsage)
	}
}

func TestUsageWithoutUsernameKeepsGenericResponse(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/usage", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if db.employeeUsageCalled {
		t.Fatal("employee usage trend was called without username")
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["usage"]; !ok {
		t.Fatalf("body missing usage: %s", rec.Body.String())
	}
	if _, ok := body["employee_usage"]; ok {
		t.Fatalf("body unexpectedly included employee_usage: %s", rec.Body.String())
	}
}
```

Add fields to `memoryAdminDB`:

```go
employeeUsageFilter EmployeeUsageFilter
employeeUsageCalled bool
```

Replace `memoryAdminDB.Query` with this deterministic fake-row implementation:

```go
func (m *memoryAdminDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "SELECT DISTINCT model") && strings.Contains(sql, "FROM usage_aggregates") {
		m.employeeUsageCalled = true
		m.employeeUsageFilter = EmployeeUsageFilter{
			Username: args[0].(string),
			Start:    args[1].(time.Time),
			End:      args[2].(time.Time),
		}
		return &scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				return nil
			},
			func(dest ...any) error {
				*(dest[0].(*string)) = "claude-4-sonnet"
				return nil
			},
		}}, nil
	}
	if strings.Contains(sql, "GROUP BY bucket_start") && strings.Contains(sql, "FROM usage_aggregates") {
		if len(args) > 3 {
			m.employeeUsageFilter.Model = args[3].(string)
		}
		return &scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "2026-05-29 00:00:00+00"
				*(dest[1].(*int64)) = int64(2)
				*(dest[2].(*int64)) = int64(2)
				*(dest[3].(*int64)) = int64(0)
				*(dest[4].(*int64)) = int64(10)
				*(dest[5].(*int64)) = int64(5)
				*(dest[6].(*int64)) = int64(3)
				*(dest[7].(*int64)) = int64(15)
				return nil
			},
		}}, nil
	}
	if strings.Contains(sql, "GROUP BY model") && strings.Contains(sql, "FROM usage_aggregates") {
		return &scanRows{scans: []func(dest ...any) error{
			func(dest ...any) error {
				*(dest[0].(*string)) = "gpt-5.2"
				*(dest[1].(*int64)) = int64(2)
				*(dest[2].(*int64)) = int64(2)
				*(dest[3].(*int64)) = int64(0)
				*(dest[4].(*int64)) = int64(10)
				*(dest[5].(*int64)) = int64(5)
				*(dest[6].(*int64)) = int64(3)
				*(dest[7].(*int64)) = int64(15)
				return nil
			},
		}}, nil
	}
	return &fakeRows{}, nil
}
```

- [ ] **Step 2: Run handler tests and verify failure**

Run:

```bash
go test ./internal/admin/ -run 'TestUsageWithUsernameReturnsEmployeeUsage|TestUsageWithoutUsernameKeepsGenericResponse' -count=1
```

Expected: FAIL because `listUsage` does not emit `employee_usage`.

- [ ] **Step 3: Add range parsing helper**

In `internal/admin/handlers.go`, add this helper near `listUsage`:

```go
func usageRangeWindow(value string, now time.Time) (string, time.Time, time.Time) {
	switch strings.TrimSpace(value) {
	case "1d":
		return "1d", now.AddDate(0, 0, -1), now
	case "7d":
		return "7d", now.AddDate(0, 0, -7), now
	default:
		return "30d", now.AddDate(0, 0, -30), now
	}
}
```

- [ ] **Step 4: Extend `listUsage`**

Replace `listUsage` in `internal/admin/handlers.go` with:

```go
func (h Handler) listUsage(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	filter := UsageFilter{
		Username:         username,
		TokenFingerprint: r.URL.Query().Get("token_fingerprint"),
		Model:            r.URL.Query().Get("model"),
		RoutePattern:     r.URL.Query().Get("route_pattern"),
		BucketSize:       r.URL.Query().Get("bucket_size"),
		Limit:            100,
	}
	items, err := h.repo.ListUsageAggregates(r.Context(), filter)
	if err != nil {
		http.Error(w, "failed to list usage", http.StatusInternalServerError)
		return
	}
	response := map[string]any{"usage": items}
	if username != "" {
		rangeValue, start, end := usageRangeWindow(r.URL.Query().Get("range"), h.auth.now())
		trend, err := h.repo.EmployeeUsageTrend(r.Context(), EmployeeUsageFilter{
			Username: username,
			Range:    rangeValue,
			Model:    strings.TrimSpace(r.URL.Query().Get("model")),
			Start:    start,
			End:      end,
		})
		if err != nil {
			http.Error(w, "failed to load employee usage", http.StatusInternalServerError)
			return
		}
		response["employee_usage"] = trend
	}
	writeJSON(w, http.StatusOK, response)
}
```

- [ ] **Step 5: Run handler tests and verify pass**

Run:

```bash
go test ./internal/admin/ -run 'TestUsageWithUsernameReturnsEmployeeUsage|TestUsageWithoutUsernameKeepsGenericResponse' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit handler slice**

Run:

```bash
git add internal/admin/handlers.go internal/admin/handlers_test.go
git commit -m "feat(admin): return employee usage trend"
```

## Task 3: Render Employee Usage UI

**Files:**
- Modify: `internal/adminui/app.js`
- Modify: `internal/adminui/app.css`

- [ ] **Step 1: Add UI state helpers**

In `internal/adminui/app.js`, change the state declaration to:

```js
const state = {
  user: null,
  view: "overview",
  error: "",
  usage: {
    username: "",
    range: "30d",
    model: "",
  },
};
```

Add these helpers near `formatNumber`:

```js
function compactNumber(value) {
  const number = Number(value || 0);
  if (number >= 1_000_000) return `${(number / 1_000_000).toFixed(1)}M`;
  if (number >= 1_000) return `${(number / 1_000).toFixed(1)}K`;
  return formatNumber(number);
}

function queryString(params) {
  const query = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && String(value).trim() !== "") {
      query.set(key, value);
    }
  });
  return query.toString();
}

function tokenSummaryValue(summary, key) {
  return Number((summary || {})[key] || 0);
}
```

- [ ] **Step 2: Update usage loading**

In `loadView`, replace:

```js
const body = await api("/usage?bucket_size=hour");
renderUsage(body);
```

with:

```js
await loadUsage();
```

Add:

```js
async function loadUsage() {
  const username = String(state.usage.username || "").trim();
  if (!username) {
    renderUsage({});
    return;
  }
  const params = queryString({
    username,
    range: state.usage.range || "30d",
    model: state.usage.model || "",
    bucket_size: "day",
  });
  const body = await api(`/usage?${params}`);
  renderUsage(body);
}
```

- [ ] **Step 3: Replace `renderUsage`**

Replace `renderUsage` with this implementation:

```js
function renderUsage(body = {}) {
  const trend = body.employee_usage || null;
  const summary = trend ? trend.summary || {} : {};
  const username = trend?.username || state.usage.username || "";
  const range = trend?.range || state.usage.range || "30d";
  const selectedModel = trend?.selected_model || state.usage.model || "";
  const models = arrayValue(trend?.models);
  const daily = arrayValue(trend?.daily);
  const modelSummary = arrayValue(trend?.model_summary);
  const searchPanel = `
    <section class="panel">
      <form id="usage-search-form" class="employee-search">
        <div class="field">
          <label for="usage-employee">员工</label>
          <input id="usage-employee" name="username" value="${escapeHTML(username)}" autocomplete="off" placeholder="E10001">
        </div>
        <button class="primary" type="submit">查看</button>
      </form>
    </section>
  `;
  if (!username || !trend) {
    renderShell(page("用量", `${searchPanel}<section class="panel muted">选择员工后查看最近 30 天 token 与 model 使用趋势。</section>`));
    bindUsageSearch();
    return;
  }
  const cards = [
    ["请求数", formatNumber(summary.request_count), `成功 ${formatNumber(summary.success_count)}，错误 ${formatNumber(summary.error_count)}`],
    ["Total Tokens", compactNumber(summary.total_tokens), `${range} 合计`],
    ["Input / Output", `${compactNumber(summary.prompt_tokens)} / ${compactNumber(summary.completion_tokens)}`, "prompt / completion"],
    ["Cache", compactNumber(summary.cached_tokens), "cached input tokens"],
  ]
    .map(([label, value, hint]) => `
      <article class="metric">
        <div class="label">${escapeHTML(label)}</div>
        <div class="value">${escapeHTML(value)}</div>
        <div class="hint">${escapeHTML(hint)}</div>
      </article>
    `)
    .join("");
  const rangeButtons = ["1d", "7d", "30d"].map((item) => `
    <button type="button" data-usage-range="${escapeHTML(item)}" class="${item === range ? "active" : ""}">${escapeHTML(item)}</button>
  `).join("");
  const modelButtons = [`<button type="button" data-usage-model="" class="${selectedModel === "" ? "active" : ""}">全部</button>`]
    .concat(models.map((model) => `
      <button type="button" data-usage-model="${escapeHTML(model)}" class="${model === selectedModel ? "active" : ""}">${escapeHTML(model)}</button>
    `))
    .join("");
  const rows = modelSummary.map((item) => [
    item.model || "unknown",
    formatNumber(item.request_count),
    formatNumber(item.prompt_tokens),
    formatNumber(item.completion_tokens),
    formatNumber(item.cached_tokens),
    formatNumber(item.total_tokens),
  ]);
  renderShell(page(
    "用量",
    `
      ${searchPanel}
      <section class="cards">${cards}</section>
      <section class="panel">
        <div class="chart-head">
          <div>
            <h2>${escapeHTML(username)} 最近 ${escapeHTML(range)} 用量趋势</h2>
            <div class="muted">按天聚合；蓝色实线为 total_tokens，虚线分别为 input/output/cache。</div>
            <div class="range-tabs">${rangeButtons}</div>
          </div>
          <div class="model-tabs">${modelButtons}</div>
        </div>
        ${usageChart(daily)}
      </section>
      <section class="panel">
        <h2>Model 汇总</h2>
        ${table(["Model", "请求数", "Input", "Output", "Cache", "Total"], rows)}
      </section>
    `,
  ));
  bindUsageSearch();
  bindUsageControls();
}
```

- [ ] **Step 4: Add chart and event binding helpers**

Add these functions after `renderUsage`:

```js
function bindUsageSearch() {
  const form = document.querySelector("#usage-search-form");
  if (!form) return;
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const formData = new FormData(event.currentTarget);
    state.usage.username = String(formData.get("username") || "").trim();
    state.usage.model = "";
    renderShell(`<section class="loading-panel">正在加载用量...</section>`);
    await loadUsage();
  });
}

function bindUsageControls() {
  document.querySelectorAll("[data-usage-range]").forEach((button) => {
    button.addEventListener("click", async () => {
      state.usage.range = button.dataset.usageRange || "30d";
      renderShell(`<section class="loading-panel">正在加载用量...</section>`);
      await loadUsage();
    });
  });
  document.querySelectorAll("[data-usage-model]").forEach((button) => {
    button.addEventListener("click", async () => {
      state.usage.model = button.dataset.usageModel || "";
      renderShell(`<section class="loading-panel">正在加载用量...</section>`);
      await loadUsage();
    });
  });
}

function usageChart(points) {
  const width = 1120;
  const height = 430;
  const left = 72;
  const right = 32;
  const top = 48;
  const bottom = 86;
  const plotWidth = width - left - right;
  const plotHeight = height - top - bottom;
  const safePoints = arrayValue(points);
  if (!safePoints.length) {
    return `<div class="empty-chart muted">暂无用量数据。</div>`;
  }
  const maxValue = Math.max(1, ...safePoints.flatMap((point) => [
    Number(point.total_tokens || 0),
    Number(point.prompt_tokens || 0),
    Number(point.completion_tokens || 0),
    Number(point.cached_tokens || 0),
  ]));
  const x = (index) => safePoints.length === 1 ? left + plotWidth / 2 : left + (plotWidth * index) / (safePoints.length - 1);
  const y = (value) => top + plotHeight - (plotHeight * Number(value || 0)) / maxValue;
  const path = (key) => safePoints.map((point, index) => `${index === 0 ? "M" : "L"}${x(index).toFixed(1)},${y(point[key]).toFixed(1)}`).join(" ");
  const dots = safePoints.length === 1
    ? ["total_tokens", "prompt_tokens", "completion_tokens", "cached_tokens"].map((key) => `<circle class="chart-dot" cx="${x(0).toFixed(1)}" cy="${y(safePoints[0][key]).toFixed(1)}" r="5"></circle>`).join("")
    : "";
  const label = (index) => {
    const raw = String(safePoints[index]?.bucket_start || "");
    const match = raw.match(/(\d{4})-(\d{2})-(\d{2})/);
    return match ? `${match[2]}-${match[3]}` : raw.slice(0, 5);
  };
  const lastIndex = safePoints.length - 1;
  const midIndex = Math.floor(lastIndex / 2);
  return `
    <div class="chart-wrap">
      <svg viewBox="0 0 ${width} ${height}" role="img" aria-label="token usage chart">
        <line class="grid" x1="${left}" y1="${top}" x2="${width - right}" y2="${top}"></line>
        <line class="grid" x1="${left}" y1="${top + plotHeight / 3}" x2="${width - right}" y2="${top + plotHeight / 3}"></line>
        <line class="grid" x1="${left}" y1="${top + (plotHeight * 2) / 3}" x2="${width - right}" y2="${top + (plotHeight * 2) / 3}"></line>
        <line class="axis" x1="${left}" y1="${top + plotHeight}" x2="${width - right}" y2="${top + plotHeight}"></line>
        <line class="axis" x1="${left}" y1="${top}" x2="${left}" y2="${top + plotHeight}"></line>
        <text x="20" y="${top + 4}" fill="#667085" font-size="12">${escapeHTML(compactNumber(maxValue))}</text>
        <text x="28" y="${top + plotHeight / 3 + 4}" fill="#667085" font-size="12">${escapeHTML(compactNumber(maxValue * 2 / 3))}</text>
        <text x="28" y="${top + (plotHeight * 2) / 3 + 4}" fill="#667085" font-size="12">${escapeHTML(compactNumber(maxValue / 3))}</text>
        <text x="39" y="${top + plotHeight + 4}" fill="#667085" font-size="12">0</text>
        <path class="series-total" d="${path("total_tokens")}"></path>
        <path class="series-input" d="${path("prompt_tokens")}"></path>
        <path class="series-output" d="${path("completion_tokens")}"></path>
        <path class="series-cache" d="${path("cached_tokens")}"></path>
        ${dots}
        <text x="${left}" y="${height - 36}" fill="#667085" font-size="12">${escapeHTML(label(0))}</text>
        <text x="${x(midIndex).toFixed(1)}" y="${height - 36}" fill="#667085" font-size="12">${escapeHTML(label(midIndex))}</text>
        <text x="${(width - right - 42).toFixed(1)}" y="${height - 36}" fill="#667085" font-size="12">${escapeHTML(label(lastIndex))}</text>
      </svg>
    </div>
    <div class="legend">
      <span><i class="swatch"></i>Total Tokens</span>
      <span><i class="swatch input"></i>Input Tokens</span>
      <span><i class="swatch output"></i>Output Tokens</span>
      <span><i class="swatch cache"></i>Cache Tokens</span>
    </div>
  `;
}
```

- [ ] **Step 5: Add CSS**

Append these styles to `internal/adminui/app.css` before the media query:

```css
.employee-search {
  display: grid;
  grid-template-columns: minmax(220px, 360px) auto;
  gap: 12px;
  align-items: end;
}

.chart-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 12px;
}

.range-tabs,
.model-tabs,
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.range-tabs {
  margin-top: 10px;
}

.model-tabs {
  justify-content: flex-end;
}

.range-tabs button,
.model-tabs button {
  border-radius: 999px;
  padding: 7px 11px;
}

.range-tabs button.active,
.model-tabs button.active {
  border-color: #93c5fd;
  background: #eff6ff;
  color: var(--accent-strong);
}

.chart-wrap {
  min-width: 720px;
}

.chart-wrap svg {
  display: block;
  width: 100%;
  height: auto;
}

.axis {
  stroke: #98a2b3;
  stroke-width: 1;
}

.grid {
  stroke: #e4e7ec;
  stroke-width: 1;
}

.series-total,
.series-input,
.series-output,
.series-cache {
  fill: none;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.series-total {
  stroke: var(--accent);
  stroke-width: 4;
}

.series-input {
  stroke: #079455;
  stroke-width: 3;
  stroke-dasharray: 8 7;
}

.series-output {
  stroke: #b54708;
  stroke-width: 3;
  stroke-dasharray: 8 7;
}

.series-cache {
  stroke: #7c3aed;
  stroke-width: 3;
  stroke-dasharray: 4 8;
}

.chart-dot {
  fill: #ffffff;
  stroke: var(--accent);
  stroke-width: 3;
}

.legend {
  color: var(--muted);
  font-size: 13px;
  margin-top: 10px;
}

.legend span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.swatch {
  width: 18px;
  height: 3px;
  border-radius: 999px;
  background: var(--accent);
}

.swatch.input {
  background: #079455;
}

.swatch.output {
  background: #b54708;
}

.swatch.cache {
  background: #7c3aed;
}

.empty-chart {
  min-height: 280px;
  display: grid;
  place-items: center;
  border: 1px dashed var(--line);
  border-radius: 8px;
}
```

Inside the existing `@media (max-width: 820px)` block, add:

```css
.employee-search,
.chart-head {
  grid-template-columns: 1fr;
  flex-direction: column;
}

.model-tabs {
  justify-content: flex-start;
}
```

- [ ] **Step 6: Run a syntax smoke check**

Run:

```bash
node --check internal/adminui/app.js
```

Expected: PASS with no output.

- [ ] **Step 7: Commit UI slice**

Run:

```bash
git add internal/adminui/app.js internal/adminui/app.css
git commit -m "feat(adminui): show employee usage trend"
```

## Task 4: Verify, Inspect Docs, And Finalize

**Files:**
- Inspect: `README.md`
- Inspect: `ARCHITECTURE.md`
- Inspect: `CLAUDE.md`
- Modify the inspected docs only when stale admin usage wording is found.

- [ ] **Step 1: Run focused admin tests**

Run:

```bash
go test ./internal/admin/ -count=1
```

Expected: PASS.

- [ ] **Step 2: Run frontend syntax check**

Run:

```bash
node --check internal/adminui/app.js
```

Expected: PASS with no output.

- [ ] **Step 3: Run full test suite**

Run:

```bash
make test
```

Expected: PASS.

- [ ] **Step 4: Start local gateway for browser verification**

Run:

```bash
make run
```

Expected: gateway starts and serves `/admin/`. If the command fails because required local services are unavailable, record the failure and verify the static UI shape by opening the admin asset through the available dev path or explain the blocker.

- [ ] **Step 5: Browser verify the usage UI**

Open `/admin/` in the in-app browser. Log in using the local credentials configured for the dev database. Navigate to "用量". Verify:

- employee input appears;
- empty employee state is readable;
- entering an employee calls `/admin/api/usage?username=...&range=30d&bucket_size=day`;
- `1d`, `7d`, and `30d` controls update the active range and API query;
- model buttons come from the API response and update the active model;
- chart legend shows Total, Input, Output, and Cache;
- Model summary table shows Input, Output, Cache, and Total columns.

- [ ] **Step 6: Inspect docs for stale descriptions**

Run:

```bash
rg -n "usage|用量|token|Token|admin|管理" README.md ARCHITECTURE.md CLAUDE.md
```

If docs describe admin usage as total-only or table-only, update the relevant paragraph. If no stale wording exists, do not edit docs.

- [ ] **Step 7: Commit docs update when docs changed**

If docs changed, run:

```bash
git add README.md ARCHITECTURE.md CLAUDE.md
git commit -m "docs: update employee usage analytics"
```

If no docs changed, skip this commit.

- [ ] **Step 8: Report final status**

Collect:

```bash
git status --short
git log --oneline -5
```

Report implemented behavior, test commands and results, browser verification result or blocker, and any docs decision.

## Self-Review

- Spec coverage: tasks cover employee search, 1d/7d/30d range, dynamic model list, split token chart series, exact model summary table, compatibility without username, no migration, and docs inspection.
- Placeholder scan: no red-flag placeholder steps remain.
- Type consistency: plan consistently uses `EmployeeUsageFilter`, `EmployeeUsageTrend`, `UsageTokenSummary`, `UsageDailyPoint`, and `UsageModelSummary`; JSON fields match the approved spec.
