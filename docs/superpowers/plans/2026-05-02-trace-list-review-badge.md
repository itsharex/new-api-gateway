# Trace 列表 needs_review Badge 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 trace 列表页 Trace ID 旁边显示橙色 `review` badge，标识需要人工审查的 trace。

**Architecture:** 在 `ListTraces()` SQL 中用 EXISTS 子查询检查 `analysis_results` 表，返回 `needs_review` 布尔值。前端用现有 `badge()` 函数渲染。

**Tech Stack:** Go (pgx), PostgreSQL, vanilla JS

---

### Task 1: Go 数据层 — TraceSummary 加 NeedsReview 字段

**Files:**
- Modify: `internal/admin/models.go:87-99`

- [ ] **Step 1: 在 TraceSummary struct 中加字段**

在 `internal/admin/models.go` 的 `TraceSummary` struct 中，`CreatedAt` 字段之后添加：

```go
NeedsReview bool `json:"needs_review"`
```

- [ ] **Step 2: Commit**

```bash
git add internal/admin/models.go
git commit -m "feat: add NeedsReview field to TraceSummary model"
```

---

### Task 2: Go 数据层 — ListTraces SQL 加子查询

**Files:**
- Modify: `internal/admin/repository.go:125-184`

- [ ] **Step 1: 修改 SQL 查询加 EXISTS 子查询**

在 `internal/admin/repository.go` 的 `ListTraces()` 函数中，将 SELECT 语句（约 line 158-162）改为：

```go
	query := fmt.Sprintf(`
	SELECT trace_id, method, path, route_pattern, protocol_family, status_code,
	       username_snapshot, fingerprint_display, model_requested, usage_total_tokens,
	       created_at::text,
	       EXISTS(SELECT 1 FROM analysis_results WHERE trace_id = t.trace_id AND severity = 'review') AS needs_review
	FROM traces t
	WHERE %s
	ORDER BY created_at DESC
	LIMIT $%d`, strings.Join(where, " AND "), len(args))
```

注意 FROM 子句从 `FROM traces` 改为 `FROM traces t`。

- [ ] **Step 2: 在 Scan 中加 needs_review 字段**

在同一个函数中，rows.Scan 调用（约 line 174-178）改为：

```go
		if err := rows.Scan(
			&trace.TraceID, &trace.Method, &trace.Path, &trace.RoutePattern, &trace.ProtocolFamily,
			&trace.StatusCode, &trace.Username, &trace.FingerprintDisplay, &trace.ModelRequested,
			&trace.UsageTotalTokens, &trace.CreatedAt, &trace.NeedsReview,
		); err != nil {
```

- [ ] **Step 3: 运行测试验证**

Run: `go test ./internal/admin/...`
Expected: PASS（现有测试使用 fakeRows，需要确认 fakeRows 返回的列数兼容）

- [ ] **Step 4: 更新 fakeRows 兼容新列数**

如果测试失败，检查 `internal/admin/repository_test.go` 中的 `fakeRows` 实现，在 Scan 返回值中多加一个 `false`（bool）值对应 `needs_review` 列。

- [ ] **Step 5: Commit**

```bash
git add internal/admin/repository.go internal/admin/repository_test.go
git commit -m "feat: add needs_review subquery to ListTraces SQL"
```

---

### Task 3: 前端 JS — renderTraces 加 badge 渲染

**Files:**
- Modify: `internal/adminui/app.js:295-317`

- [ ] **Step 1: 修改 renderTraces 中每行数据**

在 `internal/adminui/app.js` 的 `renderTraces()` 函数（line 297-305）中，将每行第一列从单独的 `traceButton(trace.trace_id)` 改为组合 HTML：

```javascript
  const rows = arrayValue(body.traces).map((trace) => [
    safeHTML(traceButton(trace.trace_id).html + (trace.needs_review ? badge("review").html : "")),
    trace.created_at,
    trace.username || trace.fingerprint_display,
    trace.model_requested,
    trace.route_pattern || trace.path,
    trace.status_code,
    formatNumber(trace.usage_total_tokens),
  ]);
```

注意：`traceButton()` 和 `badge()` 都返回 `safeHTML` 对象，需要用 `.html` 取出原始字符串拼接，再用 `safeHTML()` 包装回去。

- [ ] **Step 2: Commit**

```bash
git add internal/adminui/app.js
git commit -m "feat(adminui): show review badge next to trace ID in list"
```

---

### Task 4: 前端 CSS — 添加 .badge.review 样式

**Files:**
- Modify: `internal/adminui/app.css:281-311`

- [ ] **Step 1: 在 .badge.low 样式之后添加 .badge.review**

在 `internal/adminui/app.css` 中，`.badge.low` 规则块之后（约 line 311 后）添加：

```css
.badge.review {
  border-color: #fedf89;
  background: #fffaeb;
  color: #b54708;
}
```

复用 `.badge.medium` 的橙色配色。

- [ ] **Step 2: Commit**

```bash
git add internal/adminui/app.css
git commit -m "feat(adminui): add .badge.review CSS style"
```

---

### Task 5: 端到端验证

- [ ] **Step 1: 运行 Go 测试**

```bash
go test ./...
```

Expected: 全部 PASS

- [ ] **Step 2: 启动服务并验证 UI**

```bash
make run
```

1. 访问管理端 trace 列表页
2. 确认有 review severity 的 trace 行在 Trace ID 后显示橙色 "review" badge
3. 确认无 review 标记的 trace 行正常显示，不多余内容
4. 点击 trace 进入详情页，确认分析结果仍然正常展示
