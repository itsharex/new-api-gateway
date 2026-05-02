# Admin UI 列表时间字段补全 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 5 个缺少时间列的 admin UI 列表中添加时间字段显示，并调整 Context Catalog 排序为时间倒序。

**Architecture:** 纯前端改动（JS 列定义增加时间列）+ 1 处后端 SQL 排序调整。所有时间字段已从数据库取出并通过 API 返回，只需在 JS render 函数中将它们加入 table 列。

**Tech Stack:** Go (后端查询)、原生 JS (前端渲染)

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `internal/adminui/app.js` | Modify | 5 个 render 函数增加时间列 |
| `internal/admin/repository.go:678` | Modify | Context Catalog 排序改为 `created_at DESC` |

---

### Task 1: Traces 列表增加时间列

**Files:**
- Modify: `internal/adminui/app.js:295-305`（`renderTraces` 函数）

- [ ] **Step 1: 修改 renderTraces 增加 created_at 列**

在 `renderTraces` 函数中，在 `traceButton(trace.trace_id)` 之后增加 `trace.created_at`，并在 headers 数组中 "Trace" 之后增加 "时间"。

将 `renderTraces` 函数（第 295-316 行）中的 rows 映射改为：

```javascript
const rows = arrayValue(body.traces).map((trace) => [
    traceButton(trace.trace_id),
    trace.created_at,
    trace.username || trace.fingerprint_display,
    trace.model_requested,
    trace.route_pattern || trace.path,
    trace.status_code,
    formatNumber(trace.usage_total_tokens),
]);
```

并将 table headers 改为：

```javascript
table(["Trace", "时间", "员工", "Model", "Route", "Status", "Token"], rows)
```

- [ ] **Step 2: 验证改动**

Run: `cd /Users/roy/codes/new-api-gateway && grep -A8 'const rows = arrayValue(body.traces)' internal/adminui/app.js`

Expected: rows 数组包含 `trace.created_at`，headers 包含 "时间"

- [ ] **Step 3: Commit**

```bash
git add internal/adminui/app.js
git commit -m "feat(adminui): add created_at column to Traces list"
```

---

### Task 2: 分析结果子表增加时间列

**Files:**
- Modify: `internal/adminui/app.js:373-380`（`renderTraceDetail` 中 analysis 映射）

- [ ] **Step 1: 修改 analysis results 映射增加 created_at 列**

在 `renderTraceDetail` 函数中，将 analysis 映射改为：

```javascript
const analysis = arrayValue(trace.analysis_results).map((item) => [
    item.analyzer_name,
    item.category,
    item.label,
    item.score,
    item.confidence,
    badge(item.severity),
    item.created_at,
]);
```

并将分析结果 table headers 改为：

```javascript
table(["分析器", "分类", "标签", "分数", "置信度", "Severity", "时间"], analysis)
```

- [ ] **Step 2: 验证改动**

Run: `cd /Users/roy/codes/new-api-gateway && grep -A8 'const analysis = arrayValue' internal/adminui/app.js`

Expected: rows 数组包含 `item.created_at`，headers 包含 "时间"

- [ ] **Step 3: Commit**

```bash
git add internal/adminui/app.js
git commit -m "feat(adminui): add created_at column to analysis results table"
```

---

### Task 3: 异常列表增加时间列

**Files:**
- Modify: `internal/adminui/app.js:398-408`（`renderAnomalies` 函数）

- [ ] **Step 1: 修改 renderAnomalies 增加 created_at 列**

将 `renderAnomalies` 函数中的 rows 映射改为：

```javascript
const rows = arrayValue(body.anomalies).map((item) => [
    item.anomaly_id,
    item.created_at,
    badge(item.severity),
    item.anomaly_type,
    item.username || item.fingerprint_display,
    item.observed_value,
    item.reason,
]);
```

并将 table headers 改为：

```javascript
table(["ID", "时间", "Severity", "类型", "员工", "观测值", "原因"], rows)
```

- [ ] **Step 2: 验证改动**

Run: `cd /Users/roy/codes/new-api-gateway && grep -A9 'const rows = arrayValue(body.anomalies)' internal/adminui/app.js`

Expected: rows 数组包含 `item.created_at`，headers 包含 "时间"

- [ ] **Step 3: Commit**

```bash
git add internal/adminui/app.js
git commit -m "feat(adminui): add created_at column to Anomalies list"
```

---

### Task 4: 覆盖告警列表增加最后发现时间列

**Files:**
- Modify: `internal/adminui/app.js:411-421`（`renderCoverage` 函数）

- [ ] **Step 1: 修改 renderCoverage 增加 last_seen_at 列**

将 `renderCoverage` 函数中的 rows 映射改为：

```javascript
const rows = arrayValue(body.coverage_alerts).map((item) => [
    item.alert_id,
    item.last_seen_at,
    badge(item.severity),
    item.alert_code,
    item.method,
    item.route_pattern || item.raw_path,
    formatNumber(item.occurrence_count),
]);
```

并将 table headers 改为：

```javascript
table(["ID", "最后发现", "Severity", "Code", "Method", "Route", "数量"], rows)
```

- [ ] **Step 2: 验证改动**

Run: `cd /Users/roy/codes/new-api-gateway && grep -A9 'const rows = arrayValue(body.coverage_alerts)' internal/adminui/app.js`

Expected: rows 数组包含 `item.last_seen_at`，headers 包含 "最后发现"

- [ ] **Step 3: Commit**

```bash
git add internal/adminui/app.js
git commit -m "feat(adminui): add last_seen_at column to Coverage Alerts list"
```

---

### Task 5: Context 目录增加时间列并改排序

**Files:**
- Modify: `internal/adminui/app.js:469-483`（`renderContext` 函数）
- Modify: `internal/admin/repository.go:678`（Context Catalog 查询排序）

- [ ] **Step 1: 修改 renderContext 增加 created_at 和 updated_at 列**

将 `renderContext` 函数中的 rows 映射改为：

```javascript
const rows = arrayValue(body.context_catalog).map((item) => [
    item.context_type,
    item.name,
    item.owner,
    arrayValue(item.keywords).join(", "),
    item.expected_usage_level,
    badge(item.active ? "active" : "inactive"),
    item.created_at,
    item.updated_at,
]);
```

并将 table headers 改为：

```javascript
table(["类型", "名称", "负责人", "关键词", "使用级别", "状态", "创建时间", "更新时间"], rows)
```

- [ ] **Step 2: 修改后端 Context Catalog 排序**

在 `internal/admin/repository.go` 第 678 行，将：

```go
ORDER BY context_type, name
```

改为（该查询未使用表别名，参考同文件 WHERE 子句用 `active = true` 而非 `c.active`）：

```go
ORDER BY created_at DESC
```

- [ ] **Step 3: 验证改动**

Run: `cd /Users/roy/codes/new-api-gateway && grep 'ORDER BY' internal/admin/repository.go | grep -i context`

Expected: 显示 `ORDER BY created_at DESC` 而非 `ORDER BY context_type, name`

- [ ] **Step 4: 运行测试**

Run: `cd /Users/roy/codes/new-api-gateway && make test`

Expected: 所有测试通过

- [ ] **Step 5: Commit**

```bash
git add internal/adminui/app.js internal/admin/repository.go
git commit -m "feat(adminui): add time columns to Context Catalog and sort by created_at DESC"
```

---

### Task 6: 全量验证

- [ ] **Step 1: 运行完整测试套件**

Run: `cd /Users/roy/codes/new-api-gateway && make test`

Expected: 所有测试通过

- [ ] **Step 2: 检查所有改动文件**

Run: `cd /Users/roy/codes/new-api-gateway && git diff HEAD~5 --stat`

Expected: 只修改了 `internal/adminui/app.js` 和 `internal/admin/repository.go`，共 5 个 commit

- [ ] **Step 3: 确认 JS 语法无误**

Run: `node -c /Users/roy/codes/new-api-gateway/internal/adminui/app.js`

Expected: 无输出（语法正确）或 "SyntaxError"（需修复）
