# AGENTS.md

## Scope

- 默认用中文沟通；代码、标识符、错误文本沿用项目现有语言。
- 只信当前代码和可执行配置；任务队列已切到 Redis Streams，不要再按旧的 Redis list/BLPOP 心智理解系统。

## Repo Shape

- 这是双进程系统，不是单体服务：`cmd/audit-gateway/` 是 Go 网关入口，`workers/analysis_worker/` 是 Python 分析 worker。
- Go 网关主链路在 `internal/gateway/proxy.go`：路由匹配、证据采集、身份解析、转发、trace 持久化、投递分析任务都在这里串起来。
- Go/Python 契约边界在 `internal/jobs/` 和 `workers/analysis_worker/models.py`。改 job payload、trace 状态、analysis stage 时，必须同时检查 Go 发布端、Python 消费端、迁移和 e2e。
- 当前任务队列是 Redis Streams：core stream 为 `analysis.core`，enrichment stream 为 `analysis.enrichment`。

## Commands

```bash
# 全量基础验证：先 Node UI 测试，再 Go 全仓测试
make test

# 只跑网关包 / 单入口
go test ./internal/gateway/...
make run

# 本地 compose 栈（前台运行；ARM64 自动叠加 deploy/docker-compose.arm.yml）
make dev

# 仅起基础依赖 + 跑迁移
docker compose -f deploy/docker-compose.yml --env-file .env.local up -d postgres redis
docker compose -f deploy/docker-compose.yml --env-file .env.local --profile tools run --rm migrate

# Python worker
cd workers/analysis_worker
uv sync
uv run pytest -q
uv run pytest -q tests/test_normalizers.py
uv run python main.py
uv run python main.py --redis-once

# E2E（docker 部署后，profile=e2e on-demand 容器；要求网关/postgres/redis/常驻 worker/new-api 已部署，且 new-api 配齐 E2E_OPENAI_MODEL 与 E2E_CLAUDE_MODEL）
docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e
```

## Verification Rules

- `make test` 不是纯 Go：它先跑 `node --test internal/adminui/analysis_result_cards.test.js`，再跑 `go test ./...`。改动 admin UI 渲染时，别只跑 Go 测试。
- 小改动优先跑最窄验证：Go 改动先跑对应包测试，worker 改动先跑对应 `pytest` 文件，再决定是否升到全量或 e2e。
- `scripts/smoke_proxy.sh` 依赖 `NEW_API_KEY`；`scripts/smoke_ops_health.sh` 验 `/healthz`、`/readyz`、`/metrics`。

## Env And Runtime

- 本地快速启动可用 `bash start.sh`：它会在缺少 `.env.local` 时从 `.env.example` 复制并退出，自动拉起 postgres/redis，必要时执行迁移，再并行启动 Go 网关和 Python worker。
- Docker Compose 默认会同时启动 `analysis-worker`、`analysis-enrichment-worker`、`analysis-batch`；不要误以为只有一个 worker。
- worker 使用 Python 3.11+ 和 `uv`；Compose 容器启动命令里会先 `uv sync --quiet`。
- 启用 OSS 证据存储时，Go 网关和 Python worker 都要能读到同一套 `OSS_*` 环境变量。
- LLM judge 是可选外部能力；如果设置了任意 `LLM_JUDGE_*`，至少要同时设置 `LLM_JUDGE_BASE_URL` 和 `LLM_JUDGE_MODEL`，否则 worker 启动直接退出。

## Testing Gotchas

- E2E 是 docker-native `profile=e2e` on-demand 容器：复用已部署的网关 + 常驻 worker（`analysis-worker` / `analysis-enrichment-worker` / `analysis-batch`），不再 `go run` 网关、不发布宿主机端口、不再手动投 list/跑 `--redis-once`。
- worker 单元逻辑已回归 `workers/analysis_worker/tests/`（pytest）；e2e 只保留 5 个端到端用例，网关→worker 全链路依赖常驻 worker 消费 `analysis.core` stream。

## Data And Safety

- 不记录、不持久化 plaintext API key；相关逻辑只能用 HMAC 指纹、元数据和脱敏证据。
- 证据存储同时有 filesystem/OSS 两条路径；涉及 `object_ref`、证据派生、副本写回时，注意兼容 `file:///` 和 `oss://`。
- 修改 schema 时只新增 `migrations/NNNN_*.sql`，不要改写已发布迁移。迁移执行器会维护 `schema_migrations`，并对部分历史迁移做兼容性补记。

## Docs To Sync

- 如果你改了架构、命令、队列语义、运行方式或测试流程，至少检查 `README.md`、`ARCHITECTURE.md`、`CLAUDE.md`、`AGENTS.md` 是否一起过时。
