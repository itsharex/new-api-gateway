# AGENTS.md

本文件是给代码代理的项目地图。保持精简；不要在这里沉淀长篇架构说明。需要架构、需求、计划或运行细节时，优先跳转到下方文档。

## 先看这里

- 项目概览与快速开始：`README.md`
- 项目结构与模块说明：`ARCHITECTURE.md`
- 需求与计划材料：`docs/superpowers/`（superpowers 产物，不是稳定的系统架构文档）
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
- `internal/evidence/`、`internal/traces/`、`internal/jobs/`：原始证据存储（filesystem/OSS 双后端）、trace 持久化、Redis 分析任务发布。
- `internal/admin/`、`internal/adminui/`：管理 API、RBAC、审计日志、内置管理界面。
- `internal/alerts/`、`internal/ops/`：覆盖告警、健康检查、Prometheus 指标。
- `workers/analysis_worker/`：Python 分析 worker，负责归一化、用量聚合、异常/覆盖告警、工作相关性分类。
- `migrations/`：PostgreSQL schema 迁移，按文件编号顺序应用。
- `deploy/`：本地 Docker Compose 依赖与工具服务。
- `e2e/`：端到端测试（`run_all.py` 统一入口），依赖本地 postgres/redis。
- `scripts/`：smoke 与运维脚本。

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
```

```bash
# e2e 测试（需要 postgres/redis/new-api 运行中 + OSS 凭据）
cd e2e && uv run run_all.py
```

## 工作约定

- 默认用中文沟通；代码、标识符、错误文本沿用项目现有语言。
- Go 改动优先覆盖 `go test ./...` 或 `make test`；worker 改动同时跑 `cd workers/analysis_worker && uv run pytest -q`。
- 涉及网关与 worker 契约时，同步检查 Go 发布端、Python 消费端、迁移与测试。
- 不记录、不持久化 plaintext API key；相关逻辑应使用 HMAC 指纹、元数据和脱敏证据。
- 修改 schema 时新增迁移文件，不改写已发布迁移；注意本地 e2e 脚本会按 `migrations/*.sql` 顺序执行。
- 管理端 raw evidence 访问、API key lookup、RBAC 相关改动必须保留审计日志语义。
- 查找文件与文本优先用 `rg` / `rg --files`。
