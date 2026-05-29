# Overview Token Usage Chart Design

## Goal

在管理后台概览页指标卡下方展示最近 30 天的 token 使用曲线，帮助管理员快速判断每日用量趋势。

## Scope

- 后端扩展 `GET /admin/api/overview` 响应，在 `overview` 对象内返回 `token_usage_daily`。
- `token_usage_daily` 固定包含最近 30 天，含当前日期。
- 每个点包含 `date` 和 `total_tokens`，缺失日期返回 0。
- 数据来源复用 Python worker 已写入的 `usage_aggregates` 表中 `bucket_size = 'day'` 聚合。
- 前端不引入新依赖，用现有原生 JS 管理 UI 渲染 SVG 折线图。

## Data Contract

```json
{
  "overview": {
    "request_count_24h": 12,
    "total_tokens_24h": 3400,
    "token_usage_daily": [
      { "date": "2026-04-30", "total_tokens": 0 },
      { "date": "2026-05-01", "total_tokens": 1234 }
    ]
  }
}
```

## UI

概览页保持现有六张指标卡。指标卡下方新增一个面板，标题为 `最近 30 天 Token 使用趋势`。图表绘制总 token 折线、面积底色、起止日期标签和最大值提示。没有数据时仍展示 30 天零值曲线，并显示空态文案。

## Testing

- Go repository 测试覆盖 `OverviewSummary` 补齐 30 天 daily token 点。
- Go handler 测试覆盖 `/admin/api/overview` JSON envelope 包含 `token_usage_daily`。
- 运行 `go test ./internal/admin/` 验证管理 API。

