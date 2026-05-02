# Admin UI 时间显示时区修复

## 背景

Admin UI 所有涉及时间的页面（用量、异常告警、覆盖告警、审计日志、Traces）直接展示 PostgreSQL `TIMESTAMPTZ::text` 的输出。当前 PostgreSQL session 时区默认为 UTC，导致中国用户看到的时间比实际晚 8 小时，且界面没有时区标识。

## 方案

### 数据库连接层设置时区

在 `cmd/audit-gateway/main.go` 中，连接池初始化时通过 pgx RuntimeParams 硬编码设置 `timezone = "Asia/Shanghai"`：

- 用 `pgxpool.ParseConfig(dsn)` 替代 `pgxpool.New(ctx, dsn)`
- 设置 `config.ConnConfig.RuntimeParams["timezone"] = "Asia/Shanghai"`
- 用 `pgxpool.NewWithConfig(ctx, config)` 创建连接池
- 主库和 new-api 库连接池都做同样处理

效果：所有 `TIMESTAMPTZ::text` 输出自动变为 `2026-05-02 16:00:00+08` 格式。SQL 查询和 Go 模型无需任何改动。

### 前端列头时区标注

在 `internal/adminui/app.js` 中，所有时间相关列头加上 `(UTC+8)` 标识：

| 页面 | 当前列头 | 改为 |
|------|---------|------|
| 用量 | "时间段" | "时间 (UTC+8)" |
| 异常告警 | "时间" | "时间 (UTC+8)" |
| 覆盖告警 | "最近出现" | "最近出现 (UTC+8)" |
| 审计日志 | "时间" | "时间 (UTC+8)" |
| Traces | "时间" | "时间 (UTC+8)" |

## 影响范围

- `cmd/audit-gateway/main.go`：连接池初始化（~6 行改动）
- `internal/adminui/app.js`：列头文本（~5 处字符串改动）
- 不涉及 SQL 迁移、Go 模型、Python worker
