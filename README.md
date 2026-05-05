# new-api-gateway

new-api 项目的前端网关代理层，用于记录所有访问 new-api 的请求信息，并支撑后续数据记录与分析。

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
2. 路由匹配 → 确定 30+ 已知 API 路由的采集模式
3. 从 6 个来源提取 API Key → HMAC-SHA256 指纹脱敏
4. 身份解析（Redis 缓存 → PG 缓存 → new-api DB 逐级查找）
5. 采集请求体与请求头，存入文件系统证据库
6. 转发请求至上游 new-api 实例
7. 采集响应（支持 SSE 流式、WebSocket 双向隧道）
8. 写入 trace + evidence 记录到 PostgreSQL
9. 发布 `trace_captured` 任务到 Redis 队列
10. 返回上游响应，附加 `x-audit-trace-id` 头

### 分析 Worker 流程

1. 持续从 Redis `BLPOP analysis_jobs` 阻塞等待任务
2. 从文件系统读取证据
3. 协议归一化（OpenAI / Claude / Gemini）+ base64 媒体提取
4. 工作相关性分类
5. 运行 12+ 异常检测规则
6. 用量聚合（小时 + 天级别 upsert）
7. 持久化所有分析结果

## 快速开始

### 前置条件

- Go 1.26+
- Python 3.11+（使用 uv 管理依赖）
- Docker & Docker Compose

### 启动依赖服务

```bash
docker compose -f deploy/docker-compose.yml up -d
docker compose -f deploy/docker-compose.yml run --rm migrate
```

### 配置

复制 `.env.example` 为 `.env`，填入以下必需变量：

| 变量 | 说明 |
|------|------|
| `NEW_API_BASE_URL` | 上游 new-api 实例地址 |
| `AUDIT_HMAC_SECRET` | HMAC 密钥（≥32 字符） |
| `EVIDENCE_STORAGE_DIR` | 证据文件存储路径 |
| `POSTGRES_DSN` | 审计网关数据库 DSN |
| `NEW_API_POSTGRES_DSN` | new-api 数据库 DSN（身份解析用） |

### 运行

```bash
# Go 网关
make run

# Python 分析 Worker（持续 Redis 消费）
cd workers/analysis_worker
uv sync
uv run python main.py
```

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
