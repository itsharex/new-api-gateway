# Admin Web UI and Dashboard APIs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first usable admin web UI for audit review, backed by dashboard/query APIs for overview metrics, usage aggregates, trace detail, context catalog, and audit action logs.

**Architecture:** Keep the MVP inside the existing Go service: authenticated JSON APIs in `internal/admin`, static admin assets embedded from `internal/adminui`, and route mounting in `cmd/audit-gateway`. The UI is a small vanilla HTML/CSS/JavaScript app that consumes the existing session cookie and API routes, so no Node toolchain is introduced.

**Tech Stack:** Go `net/http`, Go `embed`, PostgreSQL via existing `pgx` repository interfaces, existing admin RBAC/session model, vanilla HTML/CSS/JavaScript.

---

## File Structure

- Modify: `internal/admin/models.go` adds DTOs for overview cards, usage buckets, trace detail, normalized messages, analysis results, context catalog entries, and audit log rows.
- Modify: `internal/admin/repository.go` adds read/query methods for those DTOs plus context catalog upsert.
- Modify: `internal/admin/repository_test.go` adds SQL-shape and bounded-query tests using the existing fake DB style.
- Modify: `internal/admin/handlers.go` registers and implements the new API routes.
- Modify: `internal/admin/handlers_test.go` tests RBAC, JSON shape, context catalog validation, and audit logging.
- Create: `internal/adminui/static.go` embeds the admin web assets and exposes a handler.
- Create: `internal/adminui/index.html` defines the admin app shell.
- Create: `internal/adminui/app.css` provides the operational dashboard styling.
- Create: `internal/adminui/app.js` implements login, navigation, table rendering, filters, review actions, API key lookup, and raw evidence links.
- Modify: `cmd/audit-gateway/main.go` mounts `/admin` static UI while preserving `/admin/api/*` behavior.
- Modify: `cmd/audit-gateway/main_test.go` verifies admin UI routing does not intercept gateway proxy routes.
- Modify: `docs/development.md` documents local admin UI usage and route/API expectations.

---

### Task 1: Admin Dashboard Models and Repository Queries

**Files:**
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/repository.go`
- Test: `internal/admin/repository_test.go`

- [ ] **Step 1: Add failing repository tests for the new queries**

Append these tests to `internal/admin/repository_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/admin -run 'TestRepository(OverviewSummary|ListUsageAggregates|InsertContextCatalogEntry)' -count=1
```

Expected: FAIL with undefined `OverviewSummary`, `UsageFilter`, `ContextCatalogEntry`, `OverviewSummary`, `ListUsageAggregates`, and `InsertContextCatalogEntry`.

- [ ] **Step 3: Add admin DTOs**

Add these definitions to `internal/admin/models.go` after `EvidenceObjectSummary`:

```go
type OverviewSummary struct {
	RequestCount24h     int64 `json:"request_count_24h"`
	SuccessCount24h     int64 `json:"success_count_24h"`
	ErrorCount24h       int64 `json:"error_count_24h"`
	TotalTokens24h      int64 `json:"total_tokens_24h"`
	OpenAnomalies       int64 `json:"open_anomalies"`
	OpenCoverageAlerts  int64 `json:"open_coverage_alerts"`
	RawOnlyTraceCount24h int64 `json:"raw_only_trace_count_24h"`
}

type UsageFilter struct {
	EmployeeNo       string
	TokenFingerprint string
	Model            string
	RoutePattern     string
	BucketSize       string
	Limit            int
}

type UsageBucket struct {
	BucketStart       string `json:"bucket_start"`
	BucketSize        string `json:"bucket_size"`
	EmployeeNo        string `json:"employee_no"`
	FingerprintDisplay string `json:"fingerprint_display"`
	Model             string `json:"model"`
	RoutePattern      string `json:"route_pattern"`
	RequestCount      int64  `json:"request_count"`
	SuccessCount      int64  `json:"success_count"`
	ErrorCount        int64  `json:"error_count"`
	TotalTokens       int64  `json:"total_tokens"`
	EstimatedCost     string `json:"estimated_cost"`
}

type TraceDetail struct {
	TraceSummary
	RequestRawRef             string                    `json:"request_raw_ref"`
	ResponseRawRef            string                    `json:"response_raw_ref"`
	RequestHeadersRef         string                    `json:"request_headers_ref"`
	ResponseHeadersRef        string                    `json:"response_headers_ref"`
	IdentityResolutionStatus string                    `json:"identity_resolution_status"`
	AnalysisStatus           string                    `json:"analysis_status"`
	NormalizedMessages       []NormalizedMessageSummary `json:"normalized_messages"`
	AnalysisResults          []AnalysisResultSummary    `json:"analysis_results"`
}

type NormalizedMessageSummary struct {
	Direction        string `json:"direction"`
	SequenceIndex    int    `json:"sequence_index"`
	Role             string `json:"role"`
	Modality         string `json:"modality"`
	ContentText       string `json:"content_text"`
	MediaURL          string `json:"media_url"`
	ProtocolItemType  string `json:"protocol_item_type"`
	TokenCountEstimate int   `json:"token_count_estimate"`
}

type AnalysisResultSummary struct {
	AnalyzerName string `json:"analyzer_name"`
	Category     string `json:"category"`
	Label        string `json:"label"`
	Score        string `json:"score"`
	Confidence   string `json:"confidence"`
	Severity     string `json:"severity"`
	ResultJSON   string `json:"result_json"`
	CreatedAt    string `json:"created_at"`
}

type ContextCatalogEntry struct {
	ID                     int64    `json:"id"`
	ContextType            string   `json:"context_type"`
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	Keywords               []string `json:"keywords"`
	Aliases                []string `json:"aliases"`
	Owner                  string   `json:"owner"`
	ExpectedTaskCategories []string `json:"expected_task_categories"`
	ExpectedModels         []string `json:"expected_models"`
	ExpectedUsageLevel     string   `json:"expected_usage_level"`
	Active                 bool     `json:"active"`
	CreatedBy              string   `json:"created_by"`
	UpdatedBy              string   `json:"updated_by"`
	CreatedAt              string   `json:"created_at"`
	UpdatedAt              string   `json:"updated_at"`
}

type AuditActionLogSummary struct {
	ActorUsername      string `json:"actor_username"`
	Action             string `json:"action"`
	TargetType         string `json:"target_type"`
	TargetID           string `json:"target_id"`
	FingerprintDisplay string `json:"fingerprint_display"`
	TraceID            string `json:"trace_id"`
	MetadataJSON       string `json:"metadata_json"`
	CreatedAt          string `json:"created_at"`
}
```

- [ ] **Step 4: Implement repository methods**

Add this import to `internal/admin/repository.go`:

```go
	"github.com/jackc/pgx/v5/pgtype"
```

Then add these methods at the end of `internal/admin/repository.go`:

```go
func (r Repository) OverviewSummary(ctx context.Context, now time.Time) (OverviewSummary, error) {
	if r.db == nil {
		return OverviewSummary{}, ErrAdminDBRequired
	}
	since := now.Add(-24 * time.Hour)
	var summary OverviewSummary
	err := r.db.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE created_at >= $1),
  count(*) FILTER (WHERE created_at >= $1 AND status_code >= 200 AND status_code < 400),
  count(*) FILTER (WHERE created_at >= $1 AND status_code >= 400),
  coalesce(sum(usage_total_tokens) FILTER (WHERE created_at >= $1), 0),
  (SELECT count(*) FROM usage_anomalies WHERE status = 'open'),
  (SELECT count(*) FROM coverage_alerts WHERE status = 'open'),
  count(*) FILTER (WHERE created_at >= $1 AND route_support_level IN ('raw_only','unknown_route','unsupported'))
FROM traces`, since).Scan(
		&summary.RequestCount24h,
		&summary.SuccessCount24h,
		&summary.ErrorCount24h,
		&summary.TotalTokens24h,
		&summary.OpenAnomalies,
		&summary.OpenCoverageAlerts,
		&summary.RawOnlyTraceCount24h,
	)
	return summary, err
}

func (r Repository) ListUsageAggregates(ctx context.Context, filter UsageFilter) ([]UsageBucket, error) {
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
	if filter.EmployeeNo != "" {
		add("employee_no = $%d", filter.EmployeeNo)
	}
	if filter.TokenFingerprint != "" {
		add("token_fingerprint = $%d", filter.TokenFingerprint)
	}
	if filter.Model != "" {
		add("model = $%d", filter.Model)
	}
	if filter.RoutePattern != "" {
		add("route_pattern = $%d", filter.RoutePattern)
	}
	if filter.BucketSize != "" {
		add("bucket_size = $%d", filter.BucketSize)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`
SELECT bucket_start::text, bucket_size, employee_no, token_name_snapshot, model, route_pattern,
       request_count, success_count, error_count, total_tokens, estimated_cost
FROM usage_aggregates
WHERE %s
ORDER BY bucket_start DESC
LIMIT $%d`, strings.Join(where, " AND "), len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []UsageBucket{}
	for rows.Next() {
		var item UsageBucket
		if err := rows.Scan(
			&item.BucketStart,
			&item.BucketSize,
			&item.EmployeeNo,
			&item.FingerprintDisplay,
			&item.Model,
			&item.RoutePattern,
			&item.RequestCount,
			&item.SuccessCount,
			&item.ErrorCount,
			&item.TotalTokens,
			&item.EstimatedCost,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) InsertContextCatalogEntry(ctx context.Context, entry ContextCatalogEntry) error {
	if r.db == nil {
		return ErrAdminDBRequired
	}
	_, err := r.db.Exec(ctx, `
INSERT INTO context_catalog (
  context_type, name, description, keywords, aliases, owner,
  expected_task_categories, expected_models, expected_usage_level, active,
  created_by, updated_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (context_type, name) DO UPDATE SET
  description = EXCLUDED.description,
  keywords = EXCLUDED.keywords,
  aliases = EXCLUDED.aliases,
  owner = EXCLUDED.owner,
  expected_task_categories = EXCLUDED.expected_task_categories,
  expected_models = EXCLUDED.expected_models,
  expected_usage_level = EXCLUDED.expected_usage_level,
  active = EXCLUDED.active,
  updated_by = EXCLUDED.updated_by,
  updated_at = now()`,
		entry.ContextType,
		entry.Name,
		entry.Description,
		entry.Keywords,
		entry.Aliases,
		entry.Owner,
		entry.ExpectedTaskCategories,
		entry.ExpectedModels,
		entry.ExpectedUsageLevel,
		entry.Active,
		entry.CreatedBy,
		entry.UpdatedBy,
	)
	return err
}
```

- [ ] **Step 5: Add remaining repository methods**

Add these methods to `internal/admin/repository.go`:

```go
func (r Repository) GetTraceDetail(ctx context.Context, traceID string) (TraceDetail, error) {
	if r.db == nil {
		return TraceDetail{}, ErrAdminDBRequired
	}
	var detail TraceDetail
	err := r.db.QueryRow(ctx, `
SELECT trace_id, method, path, route_pattern, protocol_family, status_code,
       employee_no_snapshot, fingerprint_display, model_requested, usage_total_tokens,
       created_at::text, request_raw_ref, response_raw_ref, request_headers_ref,
       response_headers_ref, identity_resolution_status, analysis_status
FROM traces
WHERE trace_id = $1
LIMIT 1`, traceID).Scan(
		&detail.TraceID,
		&detail.Method,
		&detail.Path,
		&detail.RoutePattern,
		&detail.ProtocolFamily,
		&detail.StatusCode,
		&detail.EmployeeNo,
		&detail.FingerprintDisplay,
		&detail.ModelRequested,
		&detail.UsageTotalTokens,
		&detail.CreatedAt,
		&detail.RequestRawRef,
		&detail.ResponseRawRef,
		&detail.RequestHeadersRef,
		&detail.ResponseHeadersRef,
		&detail.IdentityResolutionStatus,
		&detail.AnalysisStatus,
	)
	if err != nil {
		return TraceDetail{}, err
	}
	messages, err := r.listNormalizedMessages(ctx, traceID)
	if err != nil {
		return TraceDetail{}, err
	}
	results, err := r.listAnalysisResults(ctx, traceID)
	if err != nil {
		return TraceDetail{}, err
	}
	detail.NormalizedMessages = messages
	detail.AnalysisResults = results
	return detail, nil
}

func (r Repository) listNormalizedMessages(ctx context.Context, traceID string) ([]NormalizedMessageSummary, error) {
	rows, err := r.db.Query(ctx, `
SELECT direction, sequence_index, role, modality, content_text, media_url,
       protocol_item_type, token_count_estimate
FROM normalized_messages
WHERE trace_id = $1
ORDER BY sequence_index ASC`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NormalizedMessageSummary{}
	for rows.Next() {
		var item NormalizedMessageSummary
		if err := rows.Scan(
			&item.Direction,
			&item.SequenceIndex,
			&item.Role,
			&item.Modality,
			&item.ContentText,
			&item.MediaURL,
			&item.ProtocolItemType,
			&item.TokenCountEstimate,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) listAnalysisResults(ctx context.Context, traceID string) ([]AnalysisResultSummary, error) {
	rows, err := r.db.Query(ctx, `
SELECT analyzer_name, category, label, score::text, confidence::text,
       severity, result_json::text, created_at::text
FROM analysis_results
WHERE trace_id = $1
ORDER BY created_at ASC`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AnalysisResultSummary{}
	for rows.Next() {
		var item AnalysisResultSummary
		if err := rows.Scan(
			&item.AnalyzerName,
			&item.Category,
			&item.Label,
			&item.Score,
			&item.Confidence,
			&item.Severity,
			&item.ResultJSON,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) ListContextCatalog(ctx context.Context, activeOnly bool, limit int) ([]ContextCatalogEntry, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	where := "1=1"
	args := []any{limit}
	if activeOnly {
		where = "active = true"
	}
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
SELECT id, context_type, name, description, keywords, aliases, owner,
       expected_task_categories, expected_models, expected_usage_level, active,
       created_by, updated_by, created_at::text, updated_at::text
FROM context_catalog
WHERE %s
ORDER BY context_type, name
LIMIT $1`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ContextCatalogEntry{}
	for rows.Next() {
		var item ContextCatalogEntry
		if err := rows.Scan(
			&item.ID,
			&item.ContextType,
			&item.Name,
			&item.Description,
			(*pgtype.FlatArray[string])(&item.Keywords),
			(*pgtype.FlatArray[string])(&item.Aliases),
			&item.Owner,
			(*pgtype.FlatArray[string])(&item.ExpectedTaskCategories),
			(*pgtype.FlatArray[string])(&item.ExpectedModels),
			&item.ExpectedUsageLevel,
			&item.Active,
			&item.CreatedBy,
			&item.UpdatedBy,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r Repository) ListAuditActionLogs(ctx context.Context, limit int) ([]AuditActionLogSummary, error) {
	if r.db == nil {
		return nil, ErrAdminDBRequired
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.db.Query(ctx, `
SELECT actor_username, action, target_type, target_id, fingerprint_display,
       trace_id, metadata_json::text, created_at::text
FROM audit_action_logs
ORDER BY created_at DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AuditActionLogSummary{}
	for rows.Next() {
		var item AuditActionLogSummary
		if err := rows.Scan(
			&item.ActorUsername,
			&item.Action,
			&item.TargetType,
			&item.TargetID,
			&item.FingerprintDisplay,
			&item.TraceID,
			&item.MetadataJSON,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
```

- [ ] **Step 6: Run repository tests**

Run:

```bash
go test ./internal/admin -run 'TestRepository' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/models.go internal/admin/repository.go internal/admin/repository_test.go
git commit -m "feat: add admin dashboard repository queries"
```

---

### Task 2: Admin API Handlers for Dashboard Data

**Files:**
- Modify: `internal/admin/handlers.go`
- Test: `internal/admin/handlers_test.go`

- [ ] **Step 1: Add failing handler tests**

Append these tests to `internal/admin/handlers_test.go`:

```go
func TestOverviewRequiresAggregatePermission(t *testing.T) {
	handler, _, cookie := newAuthenticatedAdminHandler(t, RoleViewer, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/overview", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}

func TestContextCatalogCreateRequiresReviewPermissionAndWritesActor(t *testing.T) {
	handler, db, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/context-catalog", bytes.NewBufferString(`{
		"context_type":"repo",
		"name":"new-api-gateway",
		"description":"Audit gateway repository",
		"keywords":["gateway","new-api"],
		"aliases":["audit gateway"],
		"owner":"platform",
		"expected_task_categories":["coding","debugging"],
		"expected_models":["gpt-5.2"],
		"expected_usage_level":"medium",
		"active":true
	}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	if db.contextEntry.Name != "new-api-gateway" || db.contextEntry.CreatedBy != "alice" || db.contextEntry.UpdatedBy != "alice" {
		t.Fatalf("context entry = %#v", db.contextEntry)
	}
}

func TestAuditLogsRequireAdminRole(t *testing.T) {
	handler, _, cookie := newAuthenticatedAdminHandler(t, RoleAuditor, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/audit-logs", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
```

Extend `memoryAdminDB` in the same file with this field:

```go
	contextEntry ContextCatalogEntry
```

Extend `memoryAdminDB.Exec` with this case:

```go
	case strings.Contains(sql, "INSERT INTO context_catalog"):
		m.contextEntry = ContextCatalogEntry{
			ContextType:            args[0].(string),
			Name:                   args[1].(string),
			Description:            args[2].(string),
			Keywords:               args[3].([]string),
			Aliases:                args[4].([]string),
			Owner:                  args[5].(string),
			ExpectedTaskCategories: args[6].([]string),
			ExpectedModels:         args[7].([]string),
			ExpectedUsageLevel:     args[8].(string),
			Active:                 args[9].(bool),
			CreatedBy:              args[10].(string),
			UpdatedBy:              args[11].(string),
		}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/admin -run 'Test(OverviewRequiresAggregatePermission|ContextCatalogCreateRequiresReviewPermissionAndWritesActor|AuditLogsRequireAdminRole)' -count=1
```

Expected: FAIL because `/admin/api/overview`, `/admin/api/context-catalog`, and `/admin/api/audit-logs` are not registered.

- [ ] **Step 3: Register new routes**

Add these route registrations inside `NewHandler` after the existing `GET /admin/api/me` route:

```go
	h.mux.Handle("GET /admin/api/overview", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.overview))))
	h.mux.Handle("GET /admin/api/usage", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listUsage))))
	h.mux.Handle("GET /admin/api/traces/{trace_id}", h.auth.Middleware(h.auth.Require(PermissionViewNormalizedTraces, http.HandlerFunc(h.getTraceDetail))))
	h.mux.Handle("GET /admin/api/context-catalog", h.auth.Middleware(h.auth.Require(PermissionViewAggregates, http.HandlerFunc(h.listContextCatalog))))
	h.mux.Handle("POST /admin/api/context-catalog", h.auth.Middleware(h.auth.Require(PermissionReview, http.HandlerFunc(h.createContextCatalogEntry))))
	h.mux.Handle("GET /admin/api/audit-logs", h.auth.Middleware(h.auth.Require(PermissionManageUsers, http.HandlerFunc(h.listAuditLogs))))
```

- [ ] **Step 4: Implement handlers**

Add these methods to `internal/admin/handlers.go`:

```go
func (h Handler) overview(w http.ResponseWriter, r *http.Request) {
	summary, err := h.repo.OverviewSummary(r.Context(), h.auth.now())
	if err != nil {
		http.Error(w, "failed to load overview", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"overview": summary})
}

func (h Handler) listUsage(w http.ResponseWriter, r *http.Request) {
	filter := UsageFilter{
		EmployeeNo:       r.URL.Query().Get("employee_no"),
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
	writeJSON(w, http.StatusOK, map[string]any{"usage": items})
}

func (h Handler) getTraceDetail(w http.ResponseWriter, r *http.Request) {
	traceID := strings.TrimSpace(r.PathValue("trace_id"))
	if traceID == "" {
		http.Error(w, "trace_id is required", http.StatusBadRequest)
		return
	}
	detail, err := h.repo.GetTraceDetail(r.Context(), traceID)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "trace not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to load trace", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trace": detail})
}

func (h Handler) listContextCatalog(w http.ResponseWriter, r *http.Request) {
	activeOnly := r.URL.Query().Get("active") != "false"
	items, err := h.repo.ListContextCatalog(r.Context(), activeOnly, 100)
	if err != nil {
		http.Error(w, "failed to list context catalog", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"context_catalog": items})
}

func (h Handler) createContextCatalogEntry(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	var input ContextCatalogEntry
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	input.ContextType = strings.TrimSpace(input.ContextType)
	input.Name = strings.TrimSpace(input.Name)
	input.ExpectedUsageLevel = strings.TrimSpace(input.ExpectedUsageLevel)
	if !validContextCatalogEntry(input) {
		http.Error(w, "invalid context catalog entry", http.StatusBadRequest)
		return
	}
	input.CreatedBy = principal.Username
	input.UpdatedBy = principal.Username
	if err := h.repo.InsertContextCatalogEntry(r.Context(), input); err != nil {
		http.Error(w, "failed to save context catalog entry", http.StatusInternalServerError)
		return
	}
	_ = h.repo.InsertAuditActionLog(r.Context(), AuditActionLog{
		ActorUserID:   principal.UserID,
		ActorUsername: principal.Username,
		Action:        "context_catalog_upsert",
		TargetType:    "context_catalog",
		TargetID:      input.ContextType + ":" + input.Name,
		MetadataJSON:  `{"source":"admin_api"}`,
		CreatedAt:     h.auth.now(),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"context": input})
}

func validContextCatalogEntry(input ContextCatalogEntry) bool {
	switch input.ContextType {
	case "repo", "project", "product", "service", "team", "keyword_set":
	default:
		return false
	}
	if input.Name == "" {
		return false
	}
	switch input.ExpectedUsageLevel {
	case "", "low", "medium", "high":
		return true
	default:
		return false
	}
}

func (h Handler) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListAuditActionLogs(r.Context(), 100)
	if err != nil {
		http.Error(w, "failed to list audit logs", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_logs": items})
}
```

- [ ] **Step 5: Run admin tests**

Run:

```bash
go test ./internal/admin -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/handlers.go internal/admin/handlers_test.go
git commit -m "feat: add admin dashboard api routes"
```

---

### Task 3: Embedded Admin UI Handler and Gateway Mount

**Files:**
- Create: `internal/adminui/static.go`
- Create: `internal/adminui/index.html`
- Create: `internal/adminui/app.css`
- Create: `internal/adminui/app.js`
- Modify: `cmd/audit-gateway/main.go`
- Test: `cmd/audit-gateway/main_test.go`

- [ ] **Step 1: Add failing gateway routing test**

Append this test to `cmd/audit-gateway/main_test.go`:

```go
func TestBuildHTTPHandlerServesAdminUIWithoutInterceptingAPIOrProxy(t *testing.T) {
	handler := buildHTTPHandler(config.Config{}, nil, nil, log.New(io.Discard, "", 0))

	adminReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminRec := httptest.NewRecorder()
	handler.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("/admin status = %d, body = %s", adminRec.Code, adminRec.Body.String())
	}
	if !strings.Contains(adminRec.Body.String(), `id="app"`) {
		t.Fatalf("/admin did not return app shell: %s", adminRec.Body.String())
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/admin/api/me status = %d, want 503 when database unavailable", apiRec.Code)
	}
}
```

Add missing imports in `cmd/audit-gateway/main_test.go`:

```go
	"io"
	"net/http"
	"net/http/httptest"
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/audit-gateway -run TestBuildHTTPHandlerServesAdminUIWithoutInterceptingAPIOrProxy -count=1
```

Expected: FAIL because `/admin` is currently forwarded to the gateway proxy.

- [ ] **Step 3: Create the embedded admin UI package**

Create `internal/adminui/static.go`:

```go
package adminui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed index.html app.css app.js
var assets embed.FS

func Handler() http.Handler {
	sub, err := fs.Sub(assets, ".")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin")
		if path == "" || path == "/" {
			http.ServeFileFS(w, r, sub, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
```

Create `internal/adminui/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Audit Gateway Admin</title>
    <link rel="stylesheet" href="/admin/app.css">
  </head>
  <body>
    <main id="app" class="app" aria-live="polite"></main>
    <script src="/admin/app.js" defer></script>
  </body>
</html>
```

Create a temporary `internal/adminui/app.css`:

```css
html {
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  color: #172033;
  background: #f7f8fb;
}

body {
  margin: 0;
}

.app {
  min-height: 100vh;
}
```

Create a temporary `internal/adminui/app.js`:

```javascript
const app = document.querySelector("#app");
app.innerHTML = "<section class=\"login\"><h1>Audit Gateway Admin</h1></section>";
```

- [ ] **Step 4: Mount `/admin` in the gateway binary**

Modify imports in `cmd/audit-gateway/main.go`:

```go
	"github.com/your-company/new-api-gateway/internal/adminui"
```

Update `buildHTTPHandler` so the nil-pool branch and normal branch route `/admin` to the UI and `/admin/api/*` to the API:

```go
func buildHTTPHandler(cfg config.Config, pool *pgxpool.Pool, redisClient *redis.Client, logger *log.Logger) http.Handler {
	gatewayHandler := buildHandler(cfg, pool, redisClient, logger)
	uiHandler := adminui.Handler()
	if pool == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isAdminAPIPath(r.URL.Path) {
				http.Error(w, "admin database unavailable", http.StatusServiceUnavailable)
				return
			}
			if isAdminUIPath(r.URL.Path) {
				uiHandler.ServeHTTP(w, r)
				return
			}
			gatewayHandler.ServeHTTP(w, r)
		})
	}

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
		if isAdminAPIPath(r.URL.Path) {
			adminHandler.ServeHTTP(w, r)
			return
		}
		if isAdminUIPath(r.URL.Path) {
			uiHandler.ServeHTTP(w, r)
			return
		}
		gatewayHandler.ServeHTTP(w, r)
	})
}

func isAdminUIPath(path string) bool {
	return path == "/admin" || strings.HasPrefix(path, "/admin/")
}
```

- [ ] **Step 5: Run gateway tests**

Run:

```bash
go test ./cmd/audit-gateway -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/audit-gateway/main.go cmd/audit-gateway/main_test.go internal/adminui/static.go internal/adminui/index.html internal/adminui/app.css internal/adminui/app.js
git commit -m "feat: serve embedded admin web ui"
```

---

### Task 4: Admin UI Application

**Files:**
- Modify: `internal/adminui/index.html`
- Modify: `internal/adminui/app.css`
- Modify: `internal/adminui/app.js`

- [ ] **Step 1: Replace the static app shell markup**

Replace `internal/adminui/index.html` with:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Audit Gateway Admin</title>
    <link rel="stylesheet" href="/admin/app.css">
  </head>
  <body>
    <main id="app" class="app" aria-live="polite">
      <section class="loading-panel">Loading admin session...</section>
    </main>
    <script src="/admin/app.js" defer></script>
  </body>
</html>
```

- [ ] **Step 2: Replace the stylesheet**

Replace `internal/adminui/app.css` with:

```css
:root {
  --bg: #f6f7f9;
  --panel: #ffffff;
  --ink: #162033;
  --muted: #5d6678;
  --line: #d9dee8;
  --accent: #176b5b;
  --accent-strong: #0d4f42;
  --danger: #b42318;
  --warning: #9a5b00;
  --focus: #2f6feb;
}

* {
  box-sizing: border-box;
}

html {
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  background: var(--bg);
  color: var(--ink);
}

body {
  margin: 0;
}

button,
input,
select,
textarea {
  font: inherit;
}

button {
  border: 1px solid var(--line);
  background: var(--panel);
  border-radius: 6px;
  padding: 0.55rem 0.75rem;
  cursor: pointer;
}

button.primary {
  border-color: var(--accent);
  background: var(--accent);
  color: white;
}

button:focus,
input:focus,
select:focus,
textarea:focus {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}

.app-shell {
  display: grid;
  grid-template-columns: 220px minmax(0, 1fr);
  min-height: 100vh;
}

.sidebar {
  border-right: 1px solid var(--line);
  background: #101828;
  color: white;
  padding: 1rem;
}

.brand {
  font-weight: 700;
  margin-bottom: 1rem;
}

.nav {
  display: grid;
  gap: 0.35rem;
}

.nav button {
  color: white;
  background: transparent;
  border-color: transparent;
  text-align: left;
}

.nav button.active {
  background: #233049;
  border-color: #34415d;
}

.main {
  min-width: 0;
  padding: 1.25rem;
}

.toolbar,
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  align-items: center;
  margin-bottom: 1rem;
}

.panel {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 1rem;
  margin-bottom: 1rem;
}

.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 0.75rem;
}

.metric {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 0.9rem;
}

.metric span {
  display: block;
  color: var(--muted);
  font-size: 0.85rem;
}

.metric strong {
  display: block;
  margin-top: 0.35rem;
  font-size: 1.5rem;
}

table {
  width: 100%;
  border-collapse: collapse;
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 8px;
  overflow: hidden;
}

th,
td {
  border-bottom: 1px solid var(--line);
  padding: 0.65rem;
  text-align: left;
  vertical-align: top;
  font-size: 0.9rem;
}

th {
  background: #eef1f6;
  color: #30384a;
  font-weight: 650;
}

tr:last-child td {
  border-bottom: 0;
}

.login {
  display: grid;
  min-height: 100vh;
  place-items: center;
  padding: 1rem;
}

.login form {
  width: min(420px, 100%);
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 1.25rem;
}

.field {
  display: grid;
  gap: 0.35rem;
  margin-bottom: 0.75rem;
}

.field input,
.field select,
.field textarea {
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.55rem 0.65rem;
  background: white;
  min-width: 0;
}

.error {
  color: var(--danger);
}

.muted {
  color: var(--muted);
}

.badge {
  display: inline-flex;
  border: 1px solid var(--line);
  border-radius: 999px;
  padding: 0.15rem 0.45rem;
  font-size: 0.78rem;
}

.badge.high,
.badge.critical {
  border-color: #fecaca;
  color: var(--danger);
  background: #fff1f2;
}

.badge.medium {
  border-color: #fed7aa;
  color: var(--warning);
  background: #fff7ed;
}

@media (max-width: 820px) {
  .app-shell {
    grid-template-columns: 1fr;
  }

  .sidebar {
    position: sticky;
    top: 0;
    z-index: 2;
  }

  .nav {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
```

- [ ] **Step 3: Replace the JavaScript application**

Replace `internal/adminui/app.js` with:

```javascript
const state = {
  user: null,
  view: "overview",
  error: "",
};

const app = document.querySelector("#app");

const views = [
  ["overview", "Overview"],
  ["usage", "Usage"],
  ["traces", "Traces"],
  ["anomalies", "Anomalies"],
  ["coverage", "Coverage"],
  ["lookup", "API Key Lookup"],
  ["context", "Context Catalog"],
  ["audit", "Audit Logs"],
];

async function api(path, options = {}) {
  const response = await fetch(`/admin/api${path}`, {
    credentials: "same-origin",
    headers: {
      "content-type": "application/json",
      ...(options.headers || {}),
    },
    ...options,
  });
  if (!response.ok) {
    const text = await response.text();
    throw new Error(text || `HTTP ${response.status}`);
  }
  if (response.status === 204) {
    return null;
  }
  return response.json();
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\"", "&quot;")
    .replaceAll("'", "&#039;");
}

function table(headers, rows) {
  const head = headers.map((header) => `<th>${escapeHTML(header)}</th>`).join("");
  const body = rows.length
    ? rows.map((row) => `<tr>${row.map((cell) => `<td>${cell}</td>`).join("")}</tr>`).join("")
    : `<tr><td colspan="${headers.length}" class="muted">No rows</td></tr>`;
  return `<table><thead><tr>${head}</tr></thead><tbody>${body}</tbody></table>`;
}

function renderLogin() {
  app.innerHTML = `
    <section class="login">
      <form id="login-form">
        <h1>Audit Gateway Admin</h1>
        <p class="muted">Sign in with your local audit account.</p>
        ${state.error ? `<p class="error">${escapeHTML(state.error)}</p>` : ""}
        <label class="field">Username<input name="username" autocomplete="username" required></label>
        <label class="field">Password<input name="password" type="password" autocomplete="current-password" required></label>
        <button class="primary" type="submit">Sign in</button>
      </form>
    </section>`;
  document.querySelector("#login-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const data = Object.fromEntries(new FormData(event.currentTarget));
    try {
      const body = await api("/login", { method: "POST", body: JSON.stringify(data) });
      state.user = body.user;
      state.error = "";
      renderShell();
      loadView();
    } catch (error) {
      state.error = error.message.trim();
      renderLogin();
    }
  });
}

function renderShell(content = "<section class=\"panel\">Loading...</section>") {
  app.innerHTML = `
    <section class="app-shell">
      <aside class="sidebar">
        <div class="brand">Audit Gateway</div>
        <nav class="nav">
          ${views.map(([key, label]) => `<button data-view="${key}" class="${state.view === key ? "active" : ""}">${label}</button>`).join("")}
        </nav>
        <p class="muted">${escapeHTML(state.user?.username || "")} · ${escapeHTML(state.user?.role || "")}</p>
        <button id="logout">Sign out</button>
      </aside>
      <section class="main">${content}</section>
    </section>`;
  document.querySelectorAll("[data-view]").forEach((button) => {
    button.addEventListener("click", () => {
      state.view = button.dataset.view;
      renderShell();
      loadView();
    });
  });
  document.querySelector("#logout").addEventListener("click", async () => {
    await api("/logout", { method: "POST" });
    state.user = null;
    renderLogin();
  });
}

async function loadView() {
  try {
    if (state.view === "overview") return renderOverview(await api("/overview"));
    if (state.view === "usage") return renderUsage(await api("/usage?bucket_size=hour"));
    if (state.view === "traces") return renderTraces(await api("/traces"));
    if (state.view === "anomalies") return renderAnomalies(await api("/anomalies"));
    if (state.view === "coverage") return renderCoverage(await api("/coverage-alerts"));
    if (state.view === "lookup") return renderLookup();
    if (state.view === "context") return renderContext(await api("/context-catalog"));
    if (state.view === "audit") return renderAudit(await api("/audit-logs"));
  } catch (error) {
    renderShell(`<section class="panel error">${escapeHTML(error.message)}</section>`);
  }
}

function renderOverview(body) {
  const overview = body.overview || {};
  const cards = [
    ["Requests 24h", overview.request_count_24h],
    ["Tokens 24h", overview.total_tokens_24h],
    ["Errors 24h", overview.error_count_24h],
    ["Open Anomalies", overview.open_anomalies],
    ["Open Coverage", overview.open_coverage_alerts],
    ["Raw Only 24h", overview.raw_only_trace_count_24h],
  ];
  renderShell(`<h1>Overview</h1><section class="cards">${cards.map(([label, value]) => `<article class="metric"><span>${label}</span><strong>${escapeHTML(value || 0)}</strong></article>`).join("")}</section>`);
}

function renderUsage(body) {
  const rows = (body.usage || []).map((item) => [
    escapeHTML(item.bucket_start),
    escapeHTML(item.employee_no),
    escapeHTML(item.model),
    escapeHTML(item.route_pattern),
    escapeHTML(item.request_count),
    escapeHTML(item.total_tokens),
    escapeHTML(item.estimated_cost),
  ]);
  renderShell(`<h1>Usage</h1>${table(["Bucket", "Employee", "Model", "Route", "Requests", "Tokens", "Cost"], rows)}`);
}

function renderTraces(body) {
  const rows = (body.traces || []).map((item) => [
    `<button data-trace="${escapeHTML(item.trace_id)}">${escapeHTML(item.trace_id)}</button>`,
    escapeHTML(item.employee_no),
    escapeHTML(item.model_requested),
    escapeHTML(item.route_pattern),
    escapeHTML(item.status_code),
    escapeHTML(item.usage_total_tokens),
  ]);
  renderShell(`<h1>Traces</h1>${table(["Trace", "Employee", "Model", "Route", "Status", "Tokens"], rows)}`);
  document.querySelectorAll("[data-trace]").forEach((button) => {
    button.addEventListener("click", async () => renderTraceDetail(await api(`/traces/${encodeURIComponent(button.dataset.trace)}`)));
  });
}

function renderTraceDetail(body) {
  const trace = body.trace || {};
  const messages = (trace.normalized_messages || []).map((item) => [
    escapeHTML(item.sequence_index),
    escapeHTML(item.direction),
    escapeHTML(item.role),
    escapeHTML(item.modality),
    escapeHTML(item.content_text),
  ]);
  const results = (trace.analysis_results || []).map((item) => [
    escapeHTML(item.analyzer_name),
    escapeHTML(item.category),
    escapeHTML(item.label),
    escapeHTML(item.score),
    escapeHTML(item.confidence),
  ]);
  renderShell(`
    <div class="toolbar"><button id="back-to-traces">Back</button></div>
    <section class="panel">
      <h1>${escapeHTML(trace.trace_id)}</h1>
      <p>${escapeHTML(trace.method)} ${escapeHTML(trace.path)} · ${escapeHTML(trace.employee_no)} · ${escapeHTML(trace.model_requested)}</p>
      <p class="muted">Identity: ${escapeHTML(trace.identity_resolution_status)} · Analysis: ${escapeHTML(trace.analysis_status)}</p>
      <a href="/admin/api/raw-evidence/${encodeURIComponent(trace.trace_id)}/request_body">Request raw</a>
      <a href="/admin/api/raw-evidence/${encodeURIComponent(trace.trace_id)}/response_body">Response raw</a>
    </section>
    <h2>Messages</h2>${table(["#", "Direction", "Role", "Modality", "Text"], messages)}
    <h2>Analysis</h2>${table(["Analyzer", "Category", "Label", "Score", "Confidence"], results)}
  `);
  document.querySelector("#back-to-traces").addEventListener("click", async () => renderTraces(await api("/traces")));
}

function renderAnomalies(body) {
  const rows = (body.anomalies || []).map((item) => [
    escapeHTML(item.anomaly_id),
    `<span class="badge ${escapeHTML(item.severity)}">${escapeHTML(item.severity)}</span>`,
    escapeHTML(item.anomaly_type),
    escapeHTML(item.employee_no),
    escapeHTML(item.observed_value),
    escapeHTML(item.reason),
  ]);
  renderShell(`<h1>Anomalies</h1>${table(["ID", "Severity", "Type", "Employee", "Observed", "Reason"], rows)}`);
}

function renderCoverage(body) {
  const rows = (body.coverage_alerts || []).map((item) => [
    escapeHTML(item.alert_id),
    `<span class="badge ${escapeHTML(item.severity)}">${escapeHTML(item.severity)}</span>`,
    escapeHTML(item.alert_code),
    escapeHTML(item.method),
    escapeHTML(item.route_pattern || item.raw_path),
    escapeHTML(item.occurrence_count),
  ]);
  renderShell(`<h1>Coverage</h1>${table(["ID", "Severity", "Code", "Method", "Route", "Count"], rows)}`);
}

function renderLookup(result = "") {
  renderShell(`
    <h1>API Key Lookup</h1>
    <section class="panel">
      <form id="lookup-form">
        <label class="field">API key<input name="api_key" type="password" autocomplete="off" required></label>
        <button class="primary" type="submit">Lookup</button>
      </form>
    </section>
    ${result}
  `);
  document.querySelector("#lookup-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const data = Object.fromEntries(new FormData(event.currentTarget));
    const body = await api("/api-key-lookup", { method: "POST", body: JSON.stringify(data) });
    const lookup = body.lookup || {};
    renderLookup(`<section class="panel"><h2>${escapeHTML(lookup.fingerprint_display)}</h2><p>Employee: ${escapeHTML(lookup.employee_no)}</p><p>Open anomalies: ${escapeHTML(lookup.open_anomaly_count)}</p></section>`);
  });
}

function renderContext(body) {
  const rows = (body.context_catalog || []).map((item) => [
    escapeHTML(item.context_type),
    escapeHTML(item.name),
    escapeHTML(item.owner),
    escapeHTML((item.keywords || []).join(", ")),
    escapeHTML(item.expected_usage_level),
    escapeHTML(item.active),
  ]);
  renderShell(`
    <h1>Context Catalog</h1>
    <section class="panel">
      <form id="context-form">
        <label class="field">Type<select name="context_type"><option>repo</option><option>project</option><option>product</option><option>service</option><option>team</option><option>keyword_set</option></select></label>
        <label class="field">Name<input name="name" required></label>
        <label class="field">Keywords<input name="keywords" placeholder="comma separated"></label>
        <label class="field">Owner<input name="owner"></label>
        <label class="field">Usage level<select name="expected_usage_level"><option value="">Unset</option><option>low</option><option>medium</option><option>high</option></select></label>
        <button class="primary" type="submit">Save</button>
      </form>
    </section>
    ${table(["Type", "Name", "Owner", "Keywords", "Usage", "Active"], rows)}
  `);
  document.querySelector("#context-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const data = Object.fromEntries(new FormData(event.currentTarget));
    data.keywords = data.keywords.split(",").map((value) => value.trim()).filter(Boolean);
    data.aliases = [];
    data.expected_task_categories = [];
    data.expected_models = [];
    data.active = true;
    await api("/context-catalog", { method: "POST", body: JSON.stringify(data) });
    renderContext(await api("/context-catalog"));
  });
}

function renderAudit(body) {
  const rows = (body.audit_logs || []).map((item) => [
    escapeHTML(item.created_at),
    escapeHTML(item.actor_username),
    escapeHTML(item.action),
    escapeHTML(item.target_type),
    escapeHTML(item.target_id),
    escapeHTML(item.trace_id),
  ]);
  renderShell(`<h1>Audit Logs</h1>${table(["Time", "Actor", "Action", "Target Type", "Target", "Trace"], rows)}`);
}

(async function boot() {
  try {
    const body = await api("/me");
    state.user = body.user;
    renderShell();
    loadView();
  } catch {
    renderLogin();
  }
})();
```

- [ ] **Step 4: Run package tests**

Run:

```bash
go test ./internal/admin ./cmd/audit-gateway -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adminui/index.html internal/adminui/app.css internal/adminui/app.js
git commit -m "feat: build admin web ui screens"
```

---

### Task 5: Documentation and Full Verification

**Files:**
- Modify: `docs/development.md`

- [ ] **Step 1: Update development docs**

Append this section to `docs/development.md`:

```markdown
## Admin Web UI

The gateway serves the admin UI from the same binary at `/admin`.

Local flow:

1. Apply migrations through `migrations/0006_admin_rbac_audit_logs.sql`.
2. Seed an `audit_users` row with a password hash generated by `internal/admin.HashPassword`.
3. Start the gateway with `ADMIN_SESSION_SECRET`, `ADMIN_COOKIE_NAME`, `AUDIT_HMAC_SECRET`, `POSTGRES_DSN`, `REDIS_ADDR`, `NEW_API_BASE_URL`, and `EVIDENCE_STORAGE_DIR`.
4. Open `http://localhost:8080/admin`.

The UI uses the existing session cookie and calls these APIs:

- `GET /admin/api/me`
- `GET /admin/api/overview`
- `GET /admin/api/usage`
- `GET /admin/api/traces`
- `GET /admin/api/traces/{trace_id}`
- `GET /admin/api/anomalies`
- `GET /admin/api/coverage-alerts`
- `POST /admin/api/api-key-lookup`
- `GET /admin/api/context-catalog`
- `POST /admin/api/context-catalog`
- `GET /admin/api/audit-logs`

Raw evidence links point at `/admin/api/raw-evidence/{trace_id}/{object_type}` and require the `raw_access` or `admin` role. Every raw evidence request and API key lookup writes `audit_action_logs`.
```

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./internal/admin ./cmd/audit-gateway -count=1
```

Expected: PASS.

- [ ] **Step 3: Run full Go tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 4: Verify the embedded UI assets compile into the binary**

Run:

```bash
go test ./cmd/audit-gateway -run TestBuildHTTPHandlerServesAdminUIWithoutInterceptingAPIOrProxy -count=1
```

Expected: PASS and the `/admin` response body contains `id="app"`.

- [ ] **Step 5: Commit**

```bash
git add docs/development.md
git commit -m "docs: document admin web ui"
```

---

## Self-Review Notes

- Spec coverage: This plan addresses the approved design's Admin Product sections for Overview, Employee/Token Usage through usage aggregates, Trace Explorer, Anomaly Inbox, Coverage Alerts, API Key Lookup, Context Catalog, Raw Evidence access, RBAC, and Audit Action Logs.
- Scope boundary: This is an MVP web UI and query API plan. It does not add SSO/OIDC, route registry editing, anomaly rule editing, metrics exporters, retention jobs, or backfill/reanalysis workers.
- Security checks: The UI never receives plaintext API keys back from the server, raw evidence still goes through the existing audited backend endpoint, and audit logs remain admin-only.
- Verification: Each task has failing tests first, focused passing tests after implementation, and a final `go test ./... -count=1` check.
