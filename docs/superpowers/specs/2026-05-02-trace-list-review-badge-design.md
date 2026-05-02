# Trace 列表 needs_review 标签

## Context

trace 列表页当前无任何分析状态信息，必须点进详情才能看到 `needs_review` 标记。
管理员需要快速从列表中识别需要人工审查的 trace。

## 设计

在 trace 列表的 Trace ID 按钮后面紧跟一个橙色 `review` badge，对 `analysis_results` 表中
`severity = 'review'` 的 trace 进行标记。

### 视觉

- 复用现有 `badge()` 函数，生成 `<span class="badge review">review</span>`
- CSS 样式与 `.badge.medium` 配色一致（橙色系），强调"需要关注"
- 无 review 标记的 trace 不显示任何内容

### 数据层

在 `ListTraces()` SQL 的 SELECT 中增加 EXISTS 子查询：

```sql
EXISTS(
  SELECT 1 FROM analysis_results
  WHERE trace_id = t.trace_id AND severity = 'review'
) AS needs_review
```

trace_id 在 analysis_results 表有索引，列表最多 100 条，性能影响可忽略。

### 改动文件

| 文件 | 改动 |
|---|---|
| `internal/admin/repository.go` | `ListTraces()` SQL 加子查询，Scan 加字段 |
| `internal/admin/models.go` | `TraceSummary` 加 `NeedsReview bool` |
| `internal/adminui/app.js` | `renderTraces()` 中 Trace ID 后追加 badge |
| `internal/adminui/app.css` | 加 `.badge.review` 样式 |

### 不涉及

- 无新 migration
- 不改 traces 表结构
- 不改 Python worker

## 验证

1. `make test` — 确保 Go 测试通过（检查 `repository_test.go` 是否有 ListTraces 测试需更新）
2. `make run` 启动服务，访问管理端 trace 列表
3. 确认有 review severity 的 trace 显示橙色标签，无 review 的不显示
