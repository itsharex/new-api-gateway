# Trace Pagination Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add stable, 50-items-per-page pagination to the admin Trace list and preserve the current page when navigating into trace detail and back.

**Architecture:** Extend the existing `GET /admin/api/traces` endpoint instead of creating a new API. The repository will return both the current page of trace rows and pagination metadata, the handler will parse `page` and serialize the new shape, and the bundled admin UI will keep the current page in local state and render a traditional page bar.

**Tech Stack:** Go 1.x, PostgreSQL via pgx, vanilla admin UI JavaScript/CSS, embedded static assets.

---

## File Structure

- `internal/admin/models.go`: define `TracePagination` and `TraceListResult`, and add `Page` to `TraceFilter`.
- `internal/admin/repository.go`: compute trace counts, clamp page numbers, apply stable ordering, and return `TraceListResult` from `ListTraces`.
- `internal/admin/repository_test.go`: cover repository pagination metadata, bounded queries, stable ordering, and out-of-range page clamping.
- `internal/admin/handlers.go`: parse `page` from the query string and return `traces + pagination` JSON from `/admin/api/traces`.
- `internal/admin/handlers_test.go`: add list-traces handler coverage and extend the in-memory fake DB to serve paginated trace results.
- `internal/adminui/app.js`: keep trace page state, request paginated trace data, render the page bar, and preserve page state when returning from detail.
- `internal/adminui/app.css`: style the pagination summary, numbered buttons, disabled states, and mobile wrapping.
- `ARCHITECTURE.md`: document that the admin Trace list now uses fixed-size page navigation.

### Task 1: Add Repository Pagination Types and SQL

**Files:**
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/repository.go`
- Modify: `internal/admin/repository_test.go`

- [ ] **Step 1: Replace the existing trace repository test with pagination-focused assertions**

In `internal/admin/repository_test.go`, replace `TestRepositoryListTracesBuildsBoundedQuery` with:

```go
func TestRepositoryListTracesReturnsPaginationMetadata(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{
			scanFuncRow{scan: func(dest ...any) error {
				*(dest[0].(*int64)) = int64(120)
				return nil
			}},
		},
		rowsQueue: []pgx.Rows{
			&scanRows{scans: []func(dest ...any) error{
				func(dest ...any) error {
					*(dest[0].(*string)) = "trace_120"
					*(dest[1].(*string)) = "POST"
					*(dest[2].(*string)) = "/v1/chat/completions"
					*(dest[3].(*string)) = "/v1/chat/completions"
					*(dest[4].(*string)) = "openai_chat"
					*(dest[5].(*int)) = 200
					*(dest[6].(*string)) = "E10001"
					*(dest[7].(*string)) = "tkfp_abcd"
					*(dest[8].(*string)) = "gpt-5"
					*(dest[9].(*int)) = 10
					*(dest[10].(*int)) = 5
					*(dest[11].(*int)) = 2
					*(dest[12].(*int)) = 15
					*(dest[13].(*string)) = "2026-06-03 10:00:00+00"
					*(dest[14].(*bool)) = true
					return nil
				},
			}},
		},
	}
	repo := NewRepository(db)

	result, err := repo.ListTraces(context.Background(), TraceFilter{
		Page:  3,
		Limit: 50,
	})

	if err != nil {
		t.Fatalf("ListTraces error: %v", err)
	}
	if len(result.Traces) != 1 {
		t.Fatalf("trace count = %d, want 1", len(result.Traces))
	}
	if result.Traces[0].TraceID != "trace_120" || !result.Traces[0].NeedsReview {
		t.Fatalf("trace row = %#v", result.Traces[0])
	}
	if result.Pagination.Page != 3 || result.Pagination.PageSize != 50 {
		t.Fatalf("pagination page = %#v", result.Pagination)
	}
	if result.Pagination.TotalItems != 120 || result.Pagination.TotalPages != 3 {
		t.Fatalf("pagination totals = %#v", result.Pagination)
	}
	if !result.Pagination.HasPrev || result.Pagination.HasNext {
		t.Fatalf("pagination nav flags = %#v", result.Pagination)
	}
	if len(db.querySQLs) != 2 {
		t.Fatalf("querySQLs = %#v, want count + list queries", db.querySQLs)
	}
	if !strings.Contains(db.querySQLs[0], "SELECT count(*)") {
		t.Fatalf("count query = %s", db.querySQLs[0])
	}
	if !strings.Contains(db.querySQLs[1], "ORDER BY t.created_at DESC, t.trace_id DESC") {
		t.Fatalf("list query = %s", db.querySQLs[1])
	}
	if got := db.queryArgsLog[1]; len(got) != 2 || got[0] != 50 || got[1] != 100 {
		t.Fatalf("list query args = %#v, want [50 100]", got)
	}
}

func TestRepositoryListTracesClampsPageToLastPage(t *testing.T) {
	db := &recordingAdminDB{
		rowQueue: []pgx.Row{
			scanFuncRow{scan: func(dest ...any) error {
				*(dest[0].(*int64)) = int64(60)
				return nil
			}},
		},
		rowsQueue: []pgx.Rows{&fakeRows{}},
	}
	repo := NewRepository(db)

	result, err := repo.ListTraces(context.Background(), TraceFilter{
		Page:  99,
		Limit: 50,
	})

	if err != nil {
		t.Fatalf("ListTraces error: %v", err)
	}
	if result.Pagination.Page != 2 || result.Pagination.TotalPages != 2 {
		t.Fatalf("pagination = %#v, want last page 2/2", result.Pagination)
	}
	if got := db.queryArgsLog[1]; len(got) != 2 || got[0] != 50 || got[1] != 50 {
		t.Fatalf("list query args = %#v, want [50 50]", got)
	}
}
```

- [ ] **Step 2: Run the repository tests to verify they fail against the current non-paginated implementation**

Run:

```bash
go test ./internal/admin/ -run 'TestRepositoryListTraces(ReturnsPaginationMetadata|ClampsPageToLastPage)' -count=1
```

Expected: FAIL because `ListTraces` still returns `[]TraceSummary`, does not run a `COUNT(*)`, and does not apply `OFFSET`.

- [ ] **Step 3: Add pagination types to `internal/admin/models.go`**

In `internal/admin/models.go`, replace the current `TraceFilter` block and insert the new result types directly above `TraceSummary`:

```go
type TraceFilter struct {
	TraceID          string
	Username         string
	TokenFingerprint string
	RoutePattern     string
	Model            string
	StatusCode       int
	Page             int
	Limit            int
}

type TracePagination struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int   `json:"total_pages"`
	HasPrev    bool  `json:"has_prev"`
	HasNext    bool  `json:"has_next"`
}

type TraceListResult struct {
	Traces      []TraceSummary  `json:"traces"`
	Pagination  TracePagination `json:"pagination"`
}
```

- [ ] **Step 4: Replace `ListTraces` with a count + page query implementation**

In `internal/admin/repository.go`, replace the existing `ListTraces` function and update `LookupTokenSummary` to consume `TraceListResult`:

```go
func (r Repository) ListTraces(ctx context.Context, filter TraceFilter) (TraceListResult, error) {
	if r.db == nil {
		return TraceListResult{}, ErrAdminDBRequired
	}
	page := filter.Page
	if page < 1 {
		page = 1
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
		add("t.trace_id = $%d", filter.TraceID)
	}
	if filter.Username != "" {
		add("t.username_snapshot = $%d", filter.Username)
	}
	if filter.TokenFingerprint != "" {
		add("t.token_fingerprint = $%d", filter.TokenFingerprint)
	}
	if filter.RoutePattern != "" {
		add("t.route_pattern = $%d", filter.RoutePattern)
	}
	if filter.Model != "" {
		add("t.model_requested = $%d", filter.Model)
	}
	if filter.StatusCode != 0 {
		add("t.status_code = $%d", filter.StatusCode)
	}

	var totalItems int64
	countQuery := fmt.Sprintf(`SELECT count(*) FROM traces t WHERE %s`, strings.Join(where, " AND "))
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&totalItems); err != nil {
		return TraceListResult{}, err
	}

	totalPages := 0
	if totalItems > 0 {
		totalPages = int((totalItems + int64(limit) - 1) / int64(limit))
		if page > totalPages {
			page = totalPages
		}
	}

	offset := 0
	if totalPages > 0 {
		offset = (page - 1) * limit
	}

	listArgs := append(append([]any(nil), args...), limit, offset)
	query := fmt.Sprintf(`
SELECT t.trace_id, t.method, t.path, t.route_pattern, t.protocol_family, t.status_code,
       t.username_snapshot, t.fingerprint_display, t.model_requested,
       t.usage_prompt_tokens, t.usage_completion_tokens, t.usage_cached_tokens, t.usage_total_tokens,
       t.created_at::text,
       EXISTS(SELECT 1 FROM analysis_results WHERE trace_id = t.trace_id AND severity = 'review') AS needs_review
FROM traces t
WHERE %s
ORDER BY t.created_at DESC, t.trace_id DESC
LIMIT $%d OFFSET $%d`, strings.Join(where, " AND "), len(args)+1, len(args)+2)
	rows, err := r.db.Query(ctx, query, listArgs...)
	if err != nil {
		return TraceListResult{}, err
	}
	defer rows.Close()

	var traces []TraceSummary
	for rows.Next() {
		var trace TraceSummary
		if err := rows.Scan(
			&trace.TraceID, &trace.Method, &trace.Path, &trace.RoutePattern, &trace.ProtocolFamily,
			&trace.StatusCode, &trace.Username, &trace.FingerprintDisplay, &trace.ModelRequested,
			&trace.UsagePromptTokens, &trace.UsageCompletionTokens, &trace.UsageCachedTokens,
			&trace.UsageTotalTokens, &trace.CreatedAt, &trace.NeedsReview,
		); err != nil {
			return TraceListResult{}, err
		}
		traces = append(traces, trace)
	}
	if err := rows.Err(); err != nil {
		return TraceListResult{}, err
	}

	return TraceListResult{
		Traces: traces,
		Pagination: TracePagination{
			Page:       page,
			PageSize:   limit,
			TotalItems: totalItems,
			TotalPages: totalPages,
			HasPrev:    totalPages > 0 && page > 1,
			HasNext:    totalPages > 0 && page < totalPages,
		},
	}, nil
}

func (r Repository) LookupTokenSummary(ctx context.Context, tokenFingerprint, fingerprintDisplay string) (LookupSummary, error) {
	if r.db == nil {
		return LookupSummary{}, ErrAdminDBRequired
	}
	summary := LookupSummary{TokenFingerprint: tokenFingerprint, FingerprintDisplay: fingerprintDisplay}
	err := r.db.QueryRow(ctx, `
SELECT username, new_api_token_id, token_name_raw, token_status
FROM token_identity_cache
WHERE token_fingerprint = $1
LIMIT 1`, tokenFingerprint).Scan(&summary.Username, &summary.NewAPITokenID, &summary.TokenName, &summary.TokenStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LookupSummary{}, err
	}
	traceResult, err := r.ListTraces(ctx, TraceFilter{TokenFingerprint: tokenFingerprint, Page: 1, Limit: 20})
	if err != nil {
		return LookupSummary{}, err
	}
	summary.RecentTraces = traceResult.Traces
	if err := r.db.QueryRow(ctx, `
SELECT count(*)
FROM usage_anomalies
WHERE token_fingerprint = $1 AND status = 'open'`, tokenFingerprint).Scan(&summary.OpenAnomalyCount); err != nil {
		return LookupSummary{}, err
	}
	return summary, nil
}
```

- [ ] **Step 5: Run the focused repository tests and confirm they pass**

Run:

```bash
go test ./internal/admin/ -run 'TestRepositoryListTraces(ReturnsPaginationMetadata|ClampsPageToLastPage)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the repository pagination changes**

Run:

```bash
git add internal/admin/models.go internal/admin/repository.go internal/admin/repository_test.go
git commit -m "feat(admin): paginate trace repository queries"
```

Expected: commit succeeds.

### Task 2: Return Pagination from the Trace Handler and Extend Handler Test Fixtures

**Files:**
- Modify: `internal/admin/handlers.go`
- Modify: `internal/admin/handlers_test.go`

- [ ] **Step 1: Add failing handler tests for `/admin/api/traces`**

In `internal/admin/handlers_test.go`, add these tests near the existing trace-detail coverage:

```go
func TestListTracesIncludesPagination(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	db.traceListCount = 120
	db.traceList = []TraceSummary{
		{
			TraceID:               "trace_099",
			Method:                http.MethodPost,
			Path:                  "/v1/chat/completions",
			RoutePattern:          "/v1/chat/completions",
			ProtocolFamily:        "openai_chat",
			StatusCode:            http.StatusOK,
			Username:              "E10001",
			FingerprintDisplay:    "tkfp_abcd",
			ModelRequested:        "gpt-5",
			UsagePromptTokens:     12,
			UsageCompletionTokens: 23,
			UsageCachedTokens:     7,
			UsageTotalTokens:      42,
			CreatedAt:             "2026-06-03 10:00:00+00",
			NeedsReview:           true,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/traces?page=2", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Traces     []TraceSummary `json:"traces"`
		Pagination TracePagination `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode traces body: %v", err)
	}
	if len(body.Traces) != 1 || body.Traces[0].TraceID != "trace_099" || !body.Traces[0].NeedsReview {
		t.Fatalf("traces = %#v", body.Traces)
	}
	if body.Pagination.Page != 2 || body.Pagination.PageSize != 50 {
		t.Fatalf("pagination = %#v", body.Pagination)
	}
	if body.Pagination.TotalItems != 120 || body.Pagination.TotalPages != 3 {
		t.Fatalf("pagination totals = %#v", body.Pagination)
	}
	if !body.Pagination.HasPrev || !body.Pagination.HasNext {
		t.Fatalf("pagination nav flags = %#v", body.Pagination)
	}
}

func TestListTracesInvalidPageFallsBackToFirstPage(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	db.traceListCount = 0

	req := httptest.NewRequest(http.MethodGet, "/admin/api/traces?page=not-a-number", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Pagination TracePagination `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode traces body: %v", err)
	}
	if body.Pagination.Page != 1 || body.Pagination.TotalPages != 0 || body.Pagination.TotalItems != 0 {
		t.Fatalf("pagination = %#v", body.Pagination)
	}
}
```

- [ ] **Step 2: Run the handler tests and confirm they fail**

Run:

```bash
go test ./internal/admin/ -run 'TestListTraces(IncludesPagination|InvalidPageFallsBackToFirstPage)' -count=1
```

Expected: FAIL because `listTraces` still returns only `{"traces": ...}` and the in-memory DB does not supply trace list rows/counts.

- [ ] **Step 3: Update `listTraces()` and the in-memory handler test DB**

In `internal/admin/handlers.go`, add `strconv` to the imports and replace `listTraces()` with:

```go
func (h Handler) listTraces(w http.ResponseWriter, r *http.Request) {
	page, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
	if err != nil || page < 1 {
		page = 1
	}
	filter := TraceFilter{
		TraceID:          r.URL.Query().Get("trace_id"),
		Username:         r.URL.Query().Get("username"),
		TokenFingerprint: r.URL.Query().Get("token_fingerprint"),
		RoutePattern:     r.URL.Query().Get("route_pattern"),
		Model:            r.URL.Query().Get("model"),
		Page:             page,
		Limit:            50,
	}
	result, err := h.repo.ListTraces(r.Context(), filter)
	if err != nil {
		http.Error(w, "failed to list traces", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
```

In `internal/admin/handlers_test.go`, extend `memoryAdminDB` and its trace branches:

```go
type memoryAdminDB struct {
	user                    User
	session                 Session
	revokedSessionID        string
	auditActions            []string
	auditMetadata           []string
	auditLogs               []AuditActionLog
	reviewDecisions         []ReviewDecision
	contextEntry            ContextCatalogEntry
	rawEvidenceObject       EvidenceObjectSummary
	rawEvidenceErr          error
	rawEvidenceSQL          string
	rawEvidenceArgs         []any
	lookupTokenFingerprint  string
	auditErr                error
	findUserErr             error
	revokeErr               error
	updatedPasswordHash     string
	updatedPasswordUserID   int64
	revokedOtherUserID      int64
	revokedOtherKeepSession string
	revokedOtherAt          time.Time
	updatePasswordErr       error
	revokeOtherErr          error
	passwordChangeOps       []string
	traceList               []TraceSummary
	traceListCount          int64
	traceDetail             TraceDetail
	anomalies               []AnomalySummary
	traceAnomalies          []AnomalySummary
	usageModelFilter        string
	employeeUsageFilter     EmployeeUsageFilter
	employeeUsageCalled     bool
}

func (m *memoryAdminDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "FROM traces") {
		scans := make([]func(dest ...any) error, 0, len(m.traceList))
		for _, item := range m.traceList {
			item := item
			scans = append(scans, func(dest ...any) error {
				*(dest[0].(*string)) = item.TraceID
				*(dest[1].(*string)) = item.Method
				*(dest[2].(*string)) = item.Path
				*(dest[3].(*string)) = item.RoutePattern
				*(dest[4].(*string)) = item.ProtocolFamily
				*(dest[5].(*int)) = item.StatusCode
				*(dest[6].(*string)) = item.Username
				*(dest[7].(*string)) = item.FingerprintDisplay
				*(dest[8].(*string)) = item.ModelRequested
				*(dest[9].(*int)) = item.UsagePromptTokens
				*(dest[10].(*int)) = item.UsageCompletionTokens
				*(dest[11].(*int)) = item.UsageCachedTokens
				*(dest[12].(*int)) = item.UsageTotalTokens
				*(dest[13].(*string)) = item.CreatedAt
				*(dest[14].(*bool)) = item.NeedsReview
				return nil
			})
		}
		return &scanRows{scans: scans}, nil
	}
	// keep the existing usage_aggregates and anomaly branches below this point
```

Add a dedicated count branch in `memoryAdminRow.Scan()` before the generic `FROM traces` branch:

```go
	if strings.Contains(r.sql, "SELECT count(*)") && strings.Contains(r.sql, "FROM traces") {
		total := r.db.traceListCount
		if total == 0 {
			total = int64(len(r.db.traceList))
		}
		*(dest[0].(*int64)) = total
		return nil
	}
```

- [ ] **Step 4: Run the focused handler tests and confirm they pass**

Run:

```bash
go test ./internal/admin/ -run 'TestListTraces(IncludesPagination|InvalidPageFallsBackToFirstPage)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the handler + fixture changes**

Run:

```bash
git add internal/admin/handlers.go internal/admin/handlers_test.go
git commit -m "feat(admin): return trace pagination metadata"
```

Expected: commit succeeds.

### Task 3: Render Traditional Page Controls in the Bundled Admin UI

**Files:**
- Modify: `internal/adminui/app.js`
- Modify: `internal/adminui/app.css`

- [ ] **Step 1: Update trace view state and request the current page from the API**

In `internal/adminui/app.js`, extend `state` and replace the trace branch in `loadView()`:

```javascript
const state = {
  user: null,
  view: "overview",
  error: "",
  usage: {
    username: "",
    range: "30d",
    model: "",
  },
  traces: {
    page: 1,
    pageSize: 50,
  },
  password: {
    error: "",
    success: "",
  },
};
```

Replace the trace branch inside `loadView()` with:

```javascript
    } else if (state.view === "traces") {
      const params = queryString({ page: state.traces.page });
      const body = await api(`/traces?${params}`);
      renderTraces(body);
```

- [ ] **Step 2: Add trace pagination helpers and update `renderTraces()`**

In `internal/adminui/app.js`, insert these helpers above `renderTraces()`:

```javascript
function normalizeTracePagination(pagination) {
  const normalized = pagination || {};
  return {
    page: Math.max(1, Number(normalized.page || state.traces.page || 1)),
    pageSize: Math.max(1, Number(normalized.page_size || state.traces.pageSize || 50)),
    totalItems: Math.max(0, Number(normalized.total_items || 0)),
    totalPages: Math.max(0, Number(normalized.total_pages || 0)),
    hasPrev: Boolean(normalized.has_prev),
    hasNext: Boolean(normalized.has_next),
  };
}

function tracePageNumbers(pagination) {
  const total = pagination.totalPages;
  const current = pagination.page;
  if (total <= 7) {
    return Array.from({ length: total }, (_, index) => index + 1);
  }
  const pages = new Set([1, total, current - 1, current, current + 1]);
  if (current <= 3) {
    pages.add(2);
    pages.add(3);
    pages.add(4);
  }
  if (current >= total - 2) {
    pages.add(total - 1);
    pages.add(total - 2);
    pages.add(total - 3);
  }
  return Array.from(pages)
    .filter((page) => page >= 1 && page <= total)
    .sort((a, b) => a - b);
}

function tracePaginationHTML(pagination) {
  if (pagination.totalItems === 0 || pagination.totalPages === 0) {
    return `<div class="pagination-bar"><div class="pagination-summary">共 0 条</div></div>`;
  }
  const pages = tracePageNumbers(pagination);
  const pageButtons = [];
  let previous = 0;
  pages.forEach((pageNumber) => {
    if (previous && pageNumber - previous > 1) {
      pageButtons.push(`<span class="pagination-ellipsis" aria-hidden="true">...</span>`);
    }
    pageButtons.push(
      `<button type="button" data-trace-page="${pageNumber}" class="${pageNumber === pagination.page ? "active" : ""}" ${pageNumber === pagination.page ? 'aria-current="page"' : ""}>${pageNumber}</button>`,
    );
    previous = pageNumber;
  });
  return `
    <div class="pagination-bar">
      <div class="pagination-summary">第 ${formatNumber(pagination.page)} / ${formatNumber(pagination.totalPages)} 页，共 ${formatNumber(pagination.totalItems)} 条</div>
      <div class="pagination-controls">
        <button type="button" data-trace-page="1" ${pagination.hasPrev ? "" : "disabled"}>首页</button>
        <button type="button" data-trace-page="${pagination.page - 1}" ${pagination.hasPrev ? "" : "disabled"}>上一页</button>
        ${pageButtons.join("")}
        <button type="button" data-trace-page="${pagination.page + 1}" ${pagination.hasNext ? "" : "disabled"}>下一页</button>
        <button type="button" data-trace-page="${pagination.totalPages}" ${pagination.hasNext ? "" : "disabled"}>末页</button>
      </div>
    </div>
  `;
}

function bindTracePagination() {
  document.querySelectorAll("[data-trace-page]").forEach((button) => {
    if (button.disabled) return;
    button.addEventListener("click", async () => {
      const nextPage = Number(button.dataset.tracePage || 1);
      if (!Number.isFinite(nextPage) || nextPage < 1 || nextPage === state.traces.page) {
        return;
      }
      state.traces.page = nextPage;
      renderShell(`<section class="loading-panel">正在加载Trace...</section>`);
      await loadView();
    });
  });
}
```

Then replace `renderTraces()` with:

```javascript
function renderTraces(body) {
  body = body || {};
  const pagination = normalizeTracePagination(body.pagination);
  state.traces.page = pagination.page;
  state.traces.pageSize = pagination.pageSize;
  const rows = arrayValue(body.traces).map((trace) => [
    safeHTML(traceButton(trace.trace_id).html + (trace.needs_review ? badge("review").html : "")),
    formatTime(trace.created_at),
    trace.username || trace.fingerprint_display,
    trace.model_requested,
    trace.route_pattern || trace.path,
    trace.status_code,
    formatNumber(trace.usage_prompt_tokens),
    formatNumber(trace.usage_completion_tokens),
    formatNumber(trace.usage_cached_tokens),
    formatNumber(trace.usage_total_tokens),
  ]);
  renderShell(
    page(
      "Trace",
      `<section class="panel">${table(["Trace", "时间 (UTC+8)", "员工", "Model", "Route", "Status", "Input", "Output", "Cached", "Total"], rows)}${tracePaginationHTML(pagination)}</section>`,
    ),
  );
  bindTracePagination();
  document.querySelectorAll("[data-trace-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      try {
        const body = await api(`/traces/${encodeURIComponent(button.dataset.traceId)}`);
        renderTraceDetail(body);
      } catch (error) {
        renderShell(page("Trace", `<section class="panel error">${escapeHTML(error.message)}</section>`));
      }
    });
  });
}
```

- [ ] **Step 3: Style the pagination summary, controls, and disabled states**

In `internal/adminui/app.css`, add these rules near the table/layout styles:

```css
.pagination-bar {
  margin-top: 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.pagination-summary {
  color: var(--muted);
  font-size: 13px;
  font-weight: 650;
}

.pagination-controls {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.pagination-controls button {
  min-width: 40px;
}

.pagination-controls button.active {
  background: #eff6ff;
  border-color: var(--accent);
  color: var(--accent-strong);
}

.pagination-controls button:disabled {
  background: #f8fafc;
  border-color: var(--line);
  color: var(--muted);
  cursor: not-allowed;
}

.pagination-controls button:disabled:hover {
  border-color: var(--line);
  color: var(--muted);
}

.pagination-ellipsis {
  color: var(--muted);
  font-weight: 650;
}
```

In the mobile media query block, add:

```css
  .pagination-bar {
    align-items: flex-start;
  }

  .pagination-controls {
    width: 100%;
  }
```

- [ ] **Step 4: Run focused backend tests, then manually verify the UI**

Run:

```bash
go test ./internal/admin/ -count=1
```

Expected: PASS.

Then run the gateway locally:

```bash
make run
```

Open [http://localhost:8080/admin/](http://localhost:8080/admin/). If you seeded the default admin from `migrations/0015_seed_default_admin.sql`, log in with `admin / admin`.

Manual checks:

1. Trace 页面默认落在第 1 页。
2. 翻到第 2 页或更后页时，出现 `首页 / 上一页 / 页码 / 下一页 / 末页`。
3. 点击某条 trace 进入详情后，再点“返回”，列表仍停留在原页。
4. 第 1 页时 `首页` 和 `上一页` 为禁用态；末页时 `下一页` 和 `末页` 为禁用态。
5. 当某个筛选条件返回空列表时，页面显示“暂无数据。”和“共 0 条”，不显示页码按钮。

- [ ] **Step 5: Commit the admin UI pagination changes**

Run:

```bash
git add internal/adminui/app.js internal/adminui/app.css
git commit -m "feat(adminui): add trace list pagination controls"
```

Expected: commit succeeds.

### Task 4: Document the New Behavior and Run Final Verification

**Files:**
- Modify: `ARCHITECTURE.md`

- [ ] **Step 1: Update the architecture note about the Trace list**

In `ARCHITECTURE.md`, replace this sentence:

```md
Trace 列表中的 `needs_review` 只对应 `analysis_results` 里的 review 语义；trace 详情页会额外返回关联 anomaly 摘要，且每条 anomaly 同时带原始 `reason` 与 `display_reason`。其中 `display_reason` 仅对当前支持的类型生成中文文案，未知或历史类型回退原始 `reason`。
```

with:

```md
Trace 列表支持固定 50 条/页的页码分页；列表中的 `needs_review` 只对应 `analysis_results` 里的 review 语义。trace 详情页会额外返回关联 anomaly 摘要，且每条 anomaly 同时带原始 `reason` 与 `display_reason`。其中 `display_reason` 仅对当前支持的类型生成中文文案，未知或历史类型回退原始 `reason`。
```

- [ ] **Step 2: Run the full Go test suite**

Run:

```bash
make test
```

Expected: PASS.

- [ ] **Step 3: Commit the documentation and verified pagination work**

Run:

```bash
git add ARCHITECTURE.md
git commit -m "docs: note trace list pagination"
```

Expected: commit succeeds.
