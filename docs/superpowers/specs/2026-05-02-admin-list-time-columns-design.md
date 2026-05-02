# Admin UI 列表时间字段补全

## 问题

Admin UI 中有 5 个数据列表虽然按时间排序，但时间字段未显示在表格中，用户无法看到记录的时间信息。

## 涉及的列表

| 列表 | 隐藏时间字段 | 当前排序 |
|---|---|---|
| Traces | `created_at` | `created_at DESC` |
| 分析结果（Trace 详情子表） | `created_at` | `created_at ASC` |
| 异常 | `created_at` | `created_at DESC` |
| 覆盖告警 | `last_seen_at` | `last_seen_at DESC` |
| Context 目录 | `created_at`, `updated_at` | `context_type, name` |

## 改动

### 前端 `internal/adminui/app.js`

**Traces — `renderTraces()`**
- 在 Trace ID 列后增加 `时间` 列，显示 `created_at`

**分析结果 — `renderTraceDetail()`**
- 在分析结果表格增加 `时间` 列，显示 `created_at`

**异常 — `renderAnomalies()`**
- 在 ID 列后增加 `时间` 列，显示 `created_at`

**覆盖告警 — `renderCoverage()`**
- 在 ID 列后增加 `最后发现` 列，显示 `last_seen_at`

**Context 目录 — `renderContext()`**
- 增加 `创建时间`（`created_at`）和 `更新时间`（`updated_at`）列

### 后端 `internal/admin/repository.go`

**Context Catalog 查询**
- 排序从 `ORDER BY context_type, name` 改为 `ORDER BY c.created_at DESC`

### 不变

- API 响应结构：时间字段已存在于 JSON 中
- Go 数据模型：不变
- 数据库 schema：不变

## 验证

- `make run` 启动后检查各列表是否显示时间列
- 确认 Traces、异常、覆盖告警、Context 目录按时间倒序排列
- 确认分析结果保持按 sequence_index 和 created_at 正序
