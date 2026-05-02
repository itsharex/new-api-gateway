# Admin UI 时区修复实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Admin UI 所有时间字段显示为 UTC+8 时区，并在列头上标注。

**Architecture:** 在 Go 连接池初始化时通过 pgx RuntimeParams 设置 PostgreSQL session timezone 为 `Asia/Shanghai`，使所有 `TIMESTAMPTZ::text` 输出自动转为东八区格式。前端仅需修改列头文本加上 `(UTC+8)` 标识。

**Tech Stack:** Go (pgx/v5/pgxpool), JavaScript (vanilla), PostgreSQL

---

### Task 1: 连接池初始化设置时区

**Files:**
- Modify: `cmd/audit-gateway/main.go:45-56`

- [ ] **Step 1: 修改 `run` 函数，用 `pgxpool.NewWithConfig` 替代 `pgxpool.New`**

将 `cmd/audit-gateway/main.go` 第 45-56 行的连接池初始化从：

```go
func run(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	newAPIPool, err := pgxpool.New(ctx, cfg.NewAPIPostgresDSN)
	if err != nil {
		return err
	}
	defer newAPIPool.Close()
```

改为：

```go
func run(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	pool, err := newPoolWithTimezone(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	newAPIPool, err := newPoolWithTimezone(ctx, cfg.NewAPIPostgresDSN)
	if err != nil {
		return err
	}
	defer newAPIPool.Close()
```

- [ ] **Step 2: 在 `main.go` 文件末尾添加 `newPoolWithTimezone` 辅助函数**

```go
func newPoolWithTimezone(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.ConnConfig.RuntimeParams["timezone"] = "Asia/Shanghai"
	return pgxpool.NewWithConfig(ctx, config)
}
```

- [ ] **Step 3: 运行测试确认编译和现有测试通过**

Run: `go test ./cmd/audit-gateway/ -v`
Expected: PASS (所有现有测试通过，函数签名未变)

- [ ] **Step 4: Commit**

```bash
git add cmd/audit-gateway/main.go
git commit -m "feat: set PostgreSQL session timezone to Asia/Shanghai for UTC+8 display"
```

---

### Task 2: 前端列头加 UTC+8 标识

**Files:**
- Modify: `internal/adminui/app.js`

需要修改以下 6 处列头字符串：

- [ ] **Step 1: 修改用量页列头（第 292 行）**

```js
// 第 292 行
renderShell(page("用量", `<section class="panel">${table(["时间 (UTC+8)", "员工", "Model", "Route", "请求数", "Token", "费用"], rows)}</section>`));
```

- [ ] **Step 2: 修改 Trace 页列头（第 306 行）**

```js
// 第 306 行
renderShell(page("Trace", `<section class="panel">${table(["Trace", "时间 (UTC+8)", "员工", "Model", "Route", "Status", "Token"], rows)}</section>`));
```

- [ ] **Step 3: 修改 Trace 详情页分析结果列头（第 389 行）**

```js
// 第 389 行
<section class="panel"><h2>分析结果</h2>${table(["分析器", "分类", "标签", "分数", "置信度", "Severity", "时间 (UTC+8)"], analysis)}</section>
```

- [ ] **Step 4: 修改异常页列头（第 411 行）**

```js
// 第 411 行
renderShell(page("异常", `<section class="panel">${table(["ID", "时间 (UTC+8)", "Severity", "类型", "员工", "观测值", "原因"], rows)}</section>`));
```

- [ ] **Step 5: 修改覆盖告警页列头（第 425 行）**

```js
// 第 425 行
renderShell(page("覆盖", `<section class="panel">${table(["ID", "最后发现 (UTC+8)", "Severity", "Code", "Method", "Route", "数量"], rows)}</section>`));
```

- [ ] **Step 6: 修改审核记录页列头（第 574 行）**

```js
// 第 574 行
renderShell(page("审核记录", `<section class="panel">${table(["时间 (UTC+8)", "目标类型", "目标", "决定", "审核人", "备注"], rows)}</section>`));
```

- [ ] **Step 7: 修改审计日志页列头（第 599 行）**

```js
// 第 599 行
renderShell(page("审计日志", `<section class="panel">${table(["时间 (UTC+8)", "操作人", "操作", "目标类型", "目标", "Trace"], rows)}</section>`));
```

- [ ] **Step 8: 修改员工目录页列头（第 331-334 行）**

```js
// 第 331-334 行
renderShell(
  page(
    "员工目录",
    `<section class="panel">${table(["员工", "名称", "部门", "Fingerprint", "Token ID", "Token Name", "分组", "最后活跃 (UTC+8)"], rows)}</section>`,
  ),
);
```

- [ ] **Step 9: 修改 Context 目录页列头（第 489 行）**

```js
// 第 489 行
<section class="panel">${table(["类型", "名称", "负责人", "关键词", "使用级别", "状态", "创建时间 (UTC+8)", "更新时间 (UTC+8)"], rows)}</section>
```

- [ ] **Step 10: Commit**

```bash
git add internal/adminui/app.js
git commit -m "feat(adminui): add (UTC+8) labels to all time column headers"
```

---

### Task 3: 端到端验证

**Files:** 无代码改动

- [ ] **Step 1: 启动服务并登录 Admin UI**

```bash
make run
```

浏览器打开管理后台，检查以下页面的时间列头是否显示 `(UTC+8)`，且时间值符合东八区预期：
- 用量页
- Trace 页
- 异常页
- 覆盖页
- 审核记录页
- 审计日志页
- 员工目录页
- Context 目录页

- [ ] **Step 2: 运行完整测试套件确认无回归**

```bash
make test
```

Expected: PASS
