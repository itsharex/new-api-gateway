# new-api-gateway

new-api 项目的前端网关代理层，仅代理已注册的模型 API 路由并记录 trace，非模型请求返回 404。

## 架构概览

```
┌──────────┐      ┌──────────────────────┐      ┌──────────────┐
│  Client   │─────▶│   audit-gateway (Go) │─────▶│  new-api     │
└──────────┘      │  ┌────────────────┐  │      └──────────────┘
                  │  │ 路由匹配        │  │
                  │  │ API Key 提取     │  │      ┌──────────────┐
                  │  │ HMAC 指纹        │  │─────▶│ PostgreSQL   │
                  │  │ 身份解析        │  │      │ (traces,     │
                  │  │ 请求/响应采集    │  │      │  evidence)   │
                  │  │ 流式/WebSocket   │  │      └──────────────┘
                  │  └────────────────┘  │
                  │                      │      ┌──────────────┐
                  │                      │─────▶│ Redis        │
                  └──────────────────────┘      │ (job queue,  │
                         │                      │  cache)      │
                         │ trace_captured job    └──────────────┘
                         ▼
                  ┌──────────────────────┐      ┌──────────────┐
                  │ analysis-worker (Py) │─────▶│ PostgreSQL   │
                  │  ┌────────────────┐  │      │ (normalized, │
                  │  │ 协议归一化      │  │      │  anomalies,  │
                  │  │ 用量聚合        │  │      │  aggregates) │
                  │  │ 异常检测        │  │      └──────────────┘
                  │  │ 工作相关性分类   │  │
                  │  └────────────────┘  │
                  └──────────────────────┘
```

### 请求处理流程

1. 客户端请求到达网关
2. 路由匹配 → 未匹配路由直接返回 404；匹配的 30+ 已知 API 路由进入采集流程
3. 从 6 个来源提取 API Key → HMAC-SHA256 指纹脱敏
4. 身份解析（Redis 缓存 → PG 缓存 → new-api DB 逐级查找）
5. 采集请求体与请求头，存入证据库（文件系统或 OSS）
6. 转发请求至上游 new-api 实例
7. 采集响应（支持 SSE 流式、WebSocket 双向隧道）
8. 写入 trace + evidence 记录到 PostgreSQL
9. 发布 `analysis.core` stream 任务到 Redis
10. 返回上游响应，附加 `x-audit-trace-id` 头

### 分析 Worker 流程

1. 默认持续从 Redis `analysis.core` stream 读取任务；core worker 使用 `XREADGROUP COUNT N` + 本地固定并发池 + PostgreSQL 连接池处理批量任务，并通过 consumer group + ack / reclaim 语义保障 lease / retry / DLQ 流程；同一入口可通过 `ANALYSIS_REDIS_LIST=analysis.enrichment` 启动 enrichment worker
2. 从证据库读取证据（文件系统或 OSS）
3. 协议归一化（OpenAI / Claude / Gemini）+ base64 媒体提取
4. core 阶段只执行快速工作相关性 heuristic（输出 decision/action/score/evidence，并在 mixed-signal / high-cost / weak-signal 场景标记需要 LLM enrichment）；明确非工作相关直接收敛为 `non_work_use`，`work_nonwork_conflict` 仅保留在 `analysis_results` / `needs_review`，unknown 走 `record_only` 不再生成 `unknown_high_cost`
5. 当前 worker 新写入/收敛为 4 类 anomaly：`non_work_use`、`high_trace_tokens`、`long_output_anomaly`、`off_hours_high_usage`
6. core 阶段成功后按需投递 `analysis.enrichment`
7. core 热路径只写 `trace_usage_facts`，离线 rollup 再重建 `usage_aggregates` / `baseline_cache`
8. enrichment worker 消费慢增强任务，在不改写 core 结果的前提下执行 LLM judge，并补写 enrichment 阶段状态与结果
9. 管理后台 `分析运行` 视图可查看 queue depth、消费者状态和最近 runtime 趋势

其中成本类异常统一基于 `effective_tokens = max(prompt_tokens - cached_tokens, 0) + completion_tokens` 计算，避免缓存命中 prompt token 被重复计入高成本判断。
管理端异常列表与详情展示会同时返回 `display_reason` 和 worker 原始 `reason`；`display_reason` 会为当前支持的 anomaly type 生成中文展示文案，未知或历史类型则回退原始 `reason`。

## 中转路由清单

网关仅代理以下已注册的模型 API 路由，非模型请求（管理后台、静态资源等）返回 `404`。

| 方法 | 路径模式 | 协议族 | 说明 |
|------|---------|--------|------|
| POST | `/v1/chat/completions` | openai_chat | OpenAI Chat Completions |
| POST | `/pg/chat/completions` | openai_chat | OpenAI Chat Completions（兼容路径） |
| POST | `/v1/responses` | openai_responses | OpenAI Responses API |
| POST | `/v1/responses/compact` | openai_responses | OpenAI Responses API (compact) |
| POST | `/v1/messages` | claude_messages | Anthropic Claude Messages |
| POST | `/v1/completions` | openai_completions | OpenAI Legacy Completions |
| POST | `/v1/embeddings` | embeddings | Embeddings |
| POST | `/v1/engines/:model/embeddings` | embeddings | Legacy Engine Embeddings |
| POST | `/v1/rerank` | rerank | Rerank |
| POST | `/v1/images/generations` | openai_images | Image Generations |
| POST | `/v1/images/edits` | openai_images | Image Edits |
| POST | `/v1/edits` | openai_images | Legacy Edits |
| POST | `/v1/audio/transcriptions` | openai_audio | Audio Transcription |
| POST | `/v1/audio/translations` | openai_audio | Audio Translation |
| POST | `/v1/audio/speech` | openai_audio | Text-to-Speech |
| POST | `/v1beta/models/*` | gemini | Google Gemini |
| POST | `/v1/models/*` | gemini | Google Gemini (v1) |
| GET | `/v1/realtime` | realtime | Realtime WebSocket |
| POST | `/v1/video/generations` | video | Video Generation |
| GET | `/v1/video/generations/:task_id` | video | Video Polling |
| GET | `/v1/videos/:task_id` | video | Video Polling (alt) |
| GET | `/v1/videos/:task_id/content` | video | Video Content Download |
| POST | `/v1/videos/:video_id/remix` | video | Video Remix |
| POST | `/v1/videos*` | video | Video (wildcard) |
| POST | `/kling/v1/videos/text2video` | kling_video | Kling Text-to-Video |
| POST | `/kling/v1/videos/image2video` | kling_video | Kling Image-to-Video |
| GET | `/kling/v1/videos/text2video/:task_id` | kling_video | Kling Polling |
| GET | `/kling/v1/videos/image2video/:task_id` | kling_video | Kling Polling |
| POST | `/jimeng/` | jimeng | Jimeng Image |
| POST | `/:mode/mj/*` | midjourney | Midjourney（带 mode 前缀） |
| POST | `/mj/*` | midjourney | Midjourney |
| POST | `/suno/*` | suno | Suno Music |

## 快速开始

### 前置条件

- Docker 20.10+ & Docker Compose V2（`docker compose version` 可验证）
- 可访问的上游 new-api 实例

### Docker Compose 部署（生产推荐）

```bash
# 0. 克隆代码
git clone <repo-url> && cd new-api-gateway

# 1. 配置环境变量
cp .env.example .env.local
# 编辑 .env.local，填入以下三个必填项：
#   NEW_API_BASE_URL      — 上游 new-api 地址（见下方网络拓扑说明）
#   AUDIT_HMAC_SECRET     — HMAC 密钥（≥32 字符随机字符串）
#   NEW_API_POSTGRES_DSN  — new-api 数据库 DSN（身份解析用）

# 2. 运行数据库迁移（首次部署及每次版本升级后均需执行）
docker compose -f deploy/docker-compose.yml --env-file .env.local --profile tools run --rm migrate

# 3. 启动所有服务（包含每小时整点运行的离线 batch）
docker compose -f deploy/docker-compose.yml --env-file .env.local up -d

# 4. 验证服务状态
docker compose -f deploy/docker-compose.yml --env-file .env.local ps
curl http://localhost:8080/healthz    # 存活探针
curl http://localhost:8080/readyz     # 就绪探针（所有依赖正常）

```

`migrate` 服务会在数据库内维护 `schema_migrations` 记录已执行的 SQL 文件；已执行的迁移会自动跳过，只会应用新增的迁移文件。首次部署会顺序应用全部迁移，版本升级后执行同一命令即可增量应用新迁移。重复执行不会产生副作用。`analysis-batch` 会随默认 `docker compose up -d` 一起启动，并在容器内通过内置调度循环于每小时整点执行一次 `uv run python main.py --offline-batch`，负责重建 `usage_aggregates` 与相关 baseline 数据。

### Docker 网络拓扑

`NEW_API_BASE_URL` 和 `NEW_API_POSTGRES_DSN` 的值取决于 new-api 与 gateway 的部署位置：

#### 场景一：同机部署（new-api 运行在宿主机）

```
┌──────────────── 宿主机 (47.113.144.13) ──────────────────┐
│                                                           │
│  ┌─── Docker Compose 网络 ──────────────────────────┐    │
│  │  ┌──────────────┐  ┌──────────────┐              │    │
│  │  │ audit-gateway│─▶│   postgres   │              │    │
│  │  │  (:8080对外) │─▶│    redis     │              │    │
│  │  └──────┬───────┘  └──────────────┘              │    │
│  │         │host.docker.internal                     │    │
│  └─────────┼─────────────────────────────────────────┘    │
│            │ Docker 网桥 (172.17.0.1)                     │
│            ▼                                              │
│  ┌──────────────┐  ┌──────────────┐                      │
│  │   new-api    │  │  new-api PG  │  ← 宿主机进程        │
│  │  (port 3000) │  │  (port 5432) │                      │
│  └──────────────┘  └──────────────┘                      │
└───────────────────────────────────────────────────────────┘
```

容器通过 `host.docker.internal` 访问宿主机上的服务（compose 已配置 `extra_hosts`）：

```
NEW_API_BASE_URL=http://host.docker.internal:3000
NEW_API_POSTGRES_DSN=postgres://root:123456@host.docker.internal:5432/new-api?sslmode=disable
```

#### 场景二：跨机部署（new-api 在另一台服务器）

```
┌──── 服务器 A (gateway) ────┐     ┌──── 服务器 B (new-api) ────┐
│  ┌── Docker Compose ─────┐ │     │                            │
│  │ audit-gateway (:8080) │ │     │  ┌──────────────┐         │
│  │ postgres, redis       │ │     │  │   new-api    │         │
│  │ analysis-worker       │ │────▶│  │ (port 3000)  │         │
│  └───────────────────────┘ │     │  └──────────────┘         │
└────────────────────────────┘     │  ┌──────────────┐         │
                                   │  │  new-api PG  │         │
                                   │  │ (port 5432)  │         │
                                   │  └──────────────┘         │
                                   └────────────────────────────┘
```

容器直接通过内网 IP 访问对端服务器，`host.docker.internal` 不再适用：

```
NEW_API_BASE_URL=http://192.168.1.100:3000
NEW_API_POSTGRES_DSN=postgres://root:123456@192.168.1.100:5432/new-api?sslmode=disable
```

> 跨机部署需确保两台服务器内网互通，且 new-api 的端口（默认 3000）和 PostgreSQL 端口对 gateway 服务器开放。

### 环境变量

复制 `.env.example` 为 `.env.local`，填入以下必需变量：

| 变量 | 说明 |
|------|------|
| `NEW_API_BASE_URL` | 上游 new-api 实例地址（同机 `host.docker.internal`，跨机用内网 IP，见上方网络拓扑） |
| `AUDIT_HMAC_SECRET` | HMAC 密钥（≥32 字符） |
| `NEW_API_POSTGRES_DSN` | new-api 数据库 DSN（身份解析用，需容器可达的地址，见上方网络拓扑） |
| `AUDIT_GATEWAY_PORT` | 网关对外端口（默认 `8080`） |
| `EVIDENCE_STORAGE_BACKEND` | 证据存储后端：`filesystem`（默认）或 `oss` |
| `EVIDENCE_HOST_DIR` | 证据文件宿主机目录（默认 `./var/evidence`） |

以下变量 Docker 部署时**不需要设置**（compose 内部默认值已覆盖）：
- `POSTGRES_DSN`、`REDIS_ADDR` — 审计网关自身数据库和 Redis 由 compose 自动配置

### LLM Judge（可选，外部服务）

工作相关性识别不再依赖本项目内置语义分类服务。Worker 会先使用 `context_catalog` 的强 alias、弱关键词和 non-work 规则进行分层判断；只有冲突、弱信号中高成本、或高成本无强工作证据时，才调用外部 OpenAI-compatible vLLM endpoint。

本项目不部署 LLM 服务。生产环境如需启用 LLM judge，请提供以下变量：

| 变量 | 说明 |
|------|------|
| `LLM_JUDGE_BASE_URL` | 外部 vLLM OpenAI-compatible base URL，例如 `http://llm.internal:8000/v1` |
| `LLM_JUDGE_MODEL` | vLLM 暴露的模型名 |
| `LLM_JUDGE_API_KEY` | 可选 API key |
| `LLM_JUDGE_TIMEOUT_SECONDS` | 可选超时时间，默认 20 秒 |

启用时至少需要同时设置 `LLM_JUDGE_BASE_URL` 和 `LLM_JUDGE_MODEL`。默认 Docker Compose 会把这些变量透传到 `analysis-worker` / `analysis-batch` 容器，因此在容器部署里只需要把值写进 `--env-file` 指定的 env 文件（例如 `.env.local`）即可；手动运行 Python worker 时同样直接读取这些环境变量。

配置语义如下：
- 四个 `LLM_JUDGE_*` 变量都不配置：worker 正常启动，LLM judge 关闭，工作相关性仅使用规则与 `context_catalog` 判断。
- 只配置了部分变量，或 `LLM_JUDGE_TIMEOUT_SECONDS` 不是合法正数：worker 启动时直接退出并报错，不会带着不完整配置继续运行。
- 配置了 `LLM_JUDGE_BASE_URL` 和 `LLM_JUDGE_MODEL`，但未配置 `LLM_JUDGE_API_KEY`：允许启动，适用于无需鉴权的 OpenAI-compatible 服务。
- 配置完整但外部 LLM 服务超时、返回 HTTP 错误或响应格式不合法：worker 保持运行，对需要 LLM judge 的 trace 记录 `llm_judge_status=degraded` 并走保守 fallback，不会因为单次请求失败而整体退出。

如果 worker 跑在 Docker 容器里，而 LLM 服务跑在宿主机本地，`LLM_JUDGE_BASE_URL` 不能写 `http://localhost:1234/v1`；容器内应改用 `http://host.docker.internal:1234/v1`。

Streams worker 的并发/恢复参数分为两组：
- core：`ANALYSIS_CORE_READ_COUNT`、`ANALYSIS_CORE_MAX_INFLIGHT`、`ANALYSIS_CORE_LEASE_SECONDS`、`ANALYSIS_CORE_RETRY_LIMIT`
- enrichment：`ANALYSIS_ENRICHMENT_READ_COUNT`、`ANALYSIS_ENRICHMENT_MAX_INFLIGHT`、`ANALYSIS_ENRICHMENT_LEASE_SECONDS`、`ANALYSIS_ENRICHMENT_RETRY_LIMIT`、`ANALYSIS_ENRICHMENT_LLM_MAX_CONCURRENCY`

默认 Docker Compose 会把 core 参数透传到 `analysis-worker`，把 enrichment 参数透传到 `analysis-enrichment-worker`。其中 `ANALYSIS_ENRICHMENT_LLM_MAX_CONCURRENCY` 会作为 enrichment 批量消费的并发上限，用于限制慢增强阶段同时发起的 LLM judge 数量。

LLM 不直接写入异常表；它只生成 `work_relevance` analysis result，现有 `rules.py` 再统一决定是否写入 `usage_anomalies`：
- `alert_non_work` → 持久化为 `non_work_use`
- `review_conflict` → 仅保留在 `analysis_results` / `needs_review`
- `record_only` → 仅保留分析结果，unknown 不再生成 `unknown_high_cost`

OSS 后端额外变量（`EVIDENCE_STORAGE_BACKEND=oss` 时必需）：`OSS_ENDPOINT`、`OSS_BUCKET`、`OSS_ACCESS_KEY_ID`、`OSS_ACCESS_KEY_SECRET`。

### 本地开发

前置条件：Go 1.26+、Python 3.11+（uv）、Docker。

```bash
# 一键启动（基础设施 Docker + Go/Python 本地进程）
bash start.sh
```

或手动启动：

```bash
# 仅启动依赖基础设施
docker compose -f deploy/docker-compose.yml --env-file .env.local up -d postgres redis
# ARM Mac 追加: -f deploy/docker-compose.arm.yml

# 首次部署运行迁移
docker compose -f deploy/docker-compose.yml --env-file .env.local --profile tools run --rm migrate

# Go 网关
make run

# Python 分析 Worker（持续 Redis 消费）
cd workers/analysis_worker
uv sync
uv run python main.py

# core worker 吞吐调优（示例）
ANALYSIS_CORE_READ_COUNT=16 \
ANALYSIS_CORE_MAX_INFLIGHT=8 \
ANALYSIS_CORE_LEASE_SECONDS=300 \
ANALYSIS_CORE_RETRY_LIMIT=5 \
uv run python main.py

# 独立启动 enrichment worker
ANALYSIS_REDIS_LIST=analysis.enrichment uv run python main.py
```

管理后台新增以下 runtime API：

- `GET /admin/api/analysis-runtime?stage=core`
- `GET /admin/api/analysis-runtime/history?stage=core&range=1h`
- `GET /admin/api/analysis-runtime/consumers?stage=core`

`make dev` 会启动整套本地 Compose 栈；ARM Mac 会自动叠加 `docker-compose.arm.yml`。

本地开发环境变量参考 `.env.example`。

### 测试

```bash
# Go 单元测试
make test

# Python 单元测试（分析 Worker）
cd workers/analysis_worker && uv run pytest -q

# Smoke / E2E（需先启动依赖服务）
make smoke
./scripts/smoke_ops_health.sh
./scripts/e2e_worker_anomaly_coverage.sh
./scripts/e2e_worker_work_relevance.sh
```

### E2E 测试

端到端测试位于 `e2e/` 目录，验证网关代理 → Redis 队列 → Worker 分析 → 数据库写入的完整链路。运行前需确保 postgres、redis、new-api、audit-gateway 均已启动，且数据库迁移已应用。

如果本地已经有常驻 `analysis-worker` 在消费默认队列/库（例如 Docker Compose 启动后的 `redis://redis:6379/0`），worker 类 e2e 不要直接复用默认 `REDIS_URL`，否则测试任务可能会被后台 worker 抢先消费，脚本自己运行的 `--redis-once` 只会拿到 `idle`。推荐做法是：

- 网关链路类 e2e：继续使用默认环境，验证真实的持续消费链路。
- worker 单进程类 e2e：使用隔离的 Redis DB（例如 `redis://redis:6379/15`），并通过一次性容器运行脚本。

```bash
# worker 规则相关 e2e：使用隔离 Redis DB，避免被常驻 analysis-worker 抢占
docker compose -f deploy/docker-compose.yml --env-file .env.local run --rm \
  -e POSTGRES_DSN='postgres://audit:audit@postgres:5432/audit_gateway?sslmode=disable' \
  -e REDIS_URL='redis://redis:6379/15' \
  analysis-worker sh -lc 'cd /workspace/e2e && uv run python test_worker_work_relevance.py'

docker compose -f deploy/docker-compose.yml --env-file .env.local run --rm \
  -e POSTGRES_DSN='postgres://audit:audit@postgres:5432/audit_gateway?sslmode=disable' \
  -e REDIS_URL='redis://redis:6379/15' \
  analysis-worker sh -lc 'cd /workspace/e2e && uv run python test_worker_anomaly_coverage.py'

docker compose -f deploy/docker-compose.yml --env-file .env.local run --rm \
  -e POSTGRES_DSN='postgres://audit:audit@postgres:5432/audit_gateway?sslmode=disable' \
  -e REDIS_URL='redis://redis:6379/15' \
  analysis-worker sh -lc 'cd /workspace/e2e && uv run python test_worker_baseline_anomaly.py'

# offline batch e2e：不消费 Redis，可直接用一次性 worker 容器运行
docker compose -f deploy/docker-compose.yml --env-file .env.local run --rm \
  -e POSTGRES_DSN='postgres://audit:audit@postgres:5432/audit_gateway?sslmode=disable' \
  analysis-worker sh -lc 'cd /workspace/e2e && uv run python test_offline_batch.py'
```

```bash
# OpenAI 协议（/v1/chat/completions、/v1/responses，含流式）
uv run e2e/test_gateway_openai.py

# Claude 协议（/v1/messages，含 SSE 流式）
uv run e2e/test_gateway_claude.py

# 完整 Worker 管线（网关采集 → Redis → Worker 分析 → DB 验证）
uv run e2e/test_gateway_worker_pipeline.py

# 媒体 base64 提取（发送 base64 图片 → Worker 提取 → 证据改写验证）
uv run e2e/test_media_extraction.py
```

## 安全设计

- API Key 仅以 HMAC 指纹形式持久化，绝不存储明文
- 日志输出自动脱敏 Bearer Token 与 API Key
- 管理 API 使用 HMAC 签名 Cookie + RBAC 权限控制
- 敏感操作写入审计日志
- CSRF 防护 / 频率限制 / bcrypt 密码哈希
- 证据文件路径穿越防护（Go + Python 双层）

## 项目地图

详细目录结构和模块说明见 [ARCHITECTURE.md](ARCHITECTURE.md)。

## 许可

内部项目，未公开授权。
