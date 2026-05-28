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
9. 发布 `trace_captured` 任务到 Redis 队列
10. 返回上游响应，附加 `x-audit-trace-id` 头

### 分析 Worker 流程

1. 持续从 Redis `BLPOP analysis_jobs` 阻塞等待任务
2. 从证据库读取证据（文件系统或 OSS）
3. 协议归一化（OpenAI / Claude / Gemini）+ base64 媒体提取
4. 工作相关性分类
5. 运行 13+ 异常检测规则
6. 用量聚合（小时 + 天级别 upsert）
7. 持久化所有分析结果

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

# 2. 运行数据库迁移（首次部署）
docker compose -f deploy/docker-compose.yml --env-file .env.local --profile tools run --rm migrate

# 3. 启动所有服务（首次启动 embedding 需等待模型下载，约 2-5 分钟）
docker compose -f deploy/docker-compose.yml --env-file .env.local up -d

# 4. 验证服务状态
docker compose -f deploy/docker-compose.yml --env-file .env.local ps
curl http://localhost:8080/healthz    # 存活探针
curl http://localhost:8080/readyz     # 就绪探针（所有依赖正常）

# 5. 按需启动定时批处理
docker compose -f deploy/docker-compose.yml --env-file .env.local --profile tools up -d analysis-batch
```

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
│  │ embedding, worker     │ │────▶│  │ (port 3000)  │         │
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
- `EMBEDDING_URL` — 分析 Worker 的 embedding 地址由 compose 自动配置

### Embedding 服务

Embedding 服务（语义工作相关性分类）使用本地构建的 Python 服务（FastAPI + sentence-transformers，模型 BAAI/bge-m3）。

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `HF_ENDPOINT` | HuggingFace 镜像地址 | `https://hf-mirror.com` |
| `EMBEDDING_MODEL` | 嵌入模型名 | `BAAI/bge-m3` |

中国区部署默认使用 `hf-mirror.com` 下载模型，无需额外配置。模型权重缓存到 Docker volume，首次启动需等待下载完成（约 2-5 分钟）。分析 Worker 依赖 embedding 服务，模型未就绪时 Worker 会等待重试。

OSS 后端额外变量（`EVIDENCE_STORAGE_BACKEND=oss` 时必需）：`OSS_ENDPOINT`、`OSS_BUCKET`、`OSS_ACCESS_KEY_ID`、`OSS_ACCESS_KEY_SECRET`。

### 本地开发

前置条件：Go 1.26+、Python 3.11+（uv）、Docker。

```bash
# 一键启动（基础设施 Docker + Go/Python 本地进程）
bash start.sh
```

或手动启动：

```bash
# 启动依赖服务（ARM Mac 自动使用原生 embedding 服务）
make dev -d

# 首次部署运行迁移
docker compose -f deploy/docker-compose.yml --env-file .env.local --profile tools run --rm migrate

# Go 网关
make run

# Python 分析 Worker（持续 Redis 消费）
cd workers/analysis_worker
uv sync
uv run python main.py
```

`make dev` 会自动检测平台：ARM Mac 叠加 `docker-compose.arm.yml` 调整模型加载超时（ARM 较慢）。

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
