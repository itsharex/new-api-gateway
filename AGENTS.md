# AGENTS.md

本文件是给代码代理的项目地图。保持精简；不要在这里沉淀长篇架构说明。需要架构、需求、计划或运行细节时，优先跳转到下方文档。

## 先看这里

- 需求与计划材料：`docs/superpowers/`（superpowers 产物，不是稳定的系统架构文档）
- 架构说明：当前暂无独立架构文档；需要沉淀时新建 `docs/architecture.md` 或 `docs/architecture/`，并在本文件仅保留链接。
- 当前未跟踪文件不要擅自覆盖；动手前先看 `git status --short`。
- python的依赖使用uv进行管理

## 项目定位

- 本项目是 new-api 项目的前端网关代理层，用于记录所有访问 new-api 的请求信息，并支撑后续数据记录与分析。
- Go 部分负责网关代理、请求/响应采集、证据与 trace 持久化、管理端与运维接口。
- Python 部分提供数据分析能力，主要在 `workers/analysis_worker/` 中消费网关任务并写入分析结果。

## 项目地图

- `cmd/audit-gateway/`：Go 网关进程入口、HTTP 路由装配。
- `internal/gateway/`：代理转发、请求/响应捕获、流式与 multipart 处理。
- `internal/routes/`：上游路由注册与匹配。
- `internal/config/`：环境变量加载与校验。
- `internal/authkeys/`、`internal/fingerprint/`、`internal/identity/`、`internal/employee/`：API key 提取、HMAC 指纹、身份解析、员工号规则。
- `internal/evidence/`、`internal/traces/`、`internal/jobs/`：原始证据存储、trace 持久化、Redis 分析任务发布。
- `internal/admin/`、`internal/adminui/`：管理 API、RBAC、审计日志、内置管理界面。
- `internal/alerts/`、`internal/ops/`：覆盖告警、健康检查、Prometheus 指标。
- `workers/analysis_worker/`：Python 分析 worker，负责归一化、用量聚合、异常/覆盖告警、工作相关性分类。
- `migrations/`：PostgreSQL schema 迁移，按文件编号顺序应用。
- `deploy/`：本地 Docker Compose 依赖与工具服务。
- `scripts/`：smoke 与 e2e 检查脚本。

## 常用命令

```bash
make test
make run
make tidy
make smoke
```

```bash
docker compose -f deploy/docker-compose.yml up -d
docker compose -f deploy/docker-compose.yml run --rm migrate
```

```bash
cd workers/analysis_worker
uv sync
uv run pytest -q
```

```bash
./scripts/smoke_ops_health.sh
./scripts/e2e_worker_anomaly_coverage.sh
./scripts/e2e_worker_work_relevance.sh
```

## 工作约定

- 默认用中文沟通；代码、标识符、错误文本沿用项目现有语言。
- Go 改动优先覆盖 `go test ./...` 或 `make test`；worker 改动同时跑 `cd workers/analysis_worker && uv run pytest -q`。
- 涉及网关与 worker 契约时，同步检查 Go 发布端、Python 消费端、迁移与测试。
- 不记录、不持久化 plaintext API key；相关逻辑应使用 HMAC 指纹、元数据和脱敏证据。
- 修改 schema 时新增迁移文件，不改写已发布迁移；注意本地 e2e 脚本会按 `migrations/*.sql` 顺序执行。
- 管理端 raw evidence 访问、API key lookup、RBAC 相关改动必须保留审计日志语义。
- 查找文件与文本优先用 `rg` / `rg --files`。

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **new-api-gateway** (5323 symbols, 10430 relationships, 229 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> If any GitNexus tool warns the index is stale, run `npx gitnexus analyze` in terminal first.

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `gitnexus_impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `gitnexus_detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `gitnexus_query({query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `gitnexus_context({name: "symbolName"})`.

## Never Do

- NEVER edit a function, class, or method without first running `gitnexus_impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `gitnexus_rename` which understands the call graph.
- NEVER commit changes without running `gitnexus_detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/new-api-gateway/context` | Codebase overview, check index freshness |
| `gitnexus://repo/new-api-gateway/clusters` | All functional areas |
| `gitnexus://repo/new-api-gateway/processes` | All execution flows |
| `gitnexus://repo/new-api-gateway/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
