# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

new-api 的前端网关代理层。Go 网关（`cmd/audit-gateway/`）负责反向代理、请求/响应采集、证据与 trace 持久化、管理端与运维接口。Python 分析 worker（`workers/analysis_worker/`）消费 Redis 任务，执行协议归一化、用量聚合、异常检测。

两个进程通过 Redis `analysis_jobs` 列表协作：Go 网关 RPUSH `trace_captured` 任务，Python worker BLPOP 消费。

## Key Docs

- `README.md` — 快速开始、配置变量、请求处理流程图
- `ARCHITECTURE.md` — 目录结构、Go 模块详解、数据库 schema 迁移、Python worker 管线
- `docs/superpowers/` — 需求与计划材料（非稳定架构文档）

## Commands

```bash
# Go
make test              # 全量单元测试
go test ./internal/gateway/  # 单包测试
make run               # 启动网关 (go run ./cmd/audit-gateway)
make tidy              # go mod tidy

# Python worker
cd workers/analysis_worker
uv sync                # 安装依赖
uv run pytest -q       # 单元测试
uv run pytest -q tests/test_normalizers.py  # 单文件测试
uv run python main.py              # 持续消费模式
uv run python main.py --redis-once # 处理一个任务后退出（调试用）

# 依赖服务（ARM Mac 自动使用原生 embedding）
make dev -d
# 或手动指定 compose 文件
docker compose -f deploy/docker-compose.yml --env-file .env.local up -d postgres redis
docker compose -f deploy/docker-compose.yml --env-file .env.local --profile tools run --rm migrate

# E2E（需 postgres/redis/new-api 运行中）
cd e2e && uv run run_all.py
```

## Architecture Essentials

**请求生命周期（`internal/gateway/proxy.go`）：**
路由匹配 → API Key 提取（6 来源）→ HMAC 指纹脱敏 → 身份解析（Redis→PG→new-api DB 三级缓存）→ 请求采集 → 转发上游 → 响应采集（支持 SSE 流式、WebSocket 双向隧道、multipart）→ trace 持久化 → 发布 Redis 任务 → 返回响应

**Go→Python 契约（`internal/jobs/` 发布，`workers/analysis_worker/models.py` 消费）：**
涉及此契约的改动必须同步检查 Go 发布端、Python 消费端、`migrations/` 和 e2e 测试。

**证据存储（`internal/evidence/`）：**
`Store` 接口统一 filesystem/OSS 双后端，写入时计算 SHA-256 并做路径穿越防护。`object_ref` 格式：`file:///` 或 `oss://`。

**管理 API（`internal/admin/`）：**
RBAC 四级权限（viewer→auditor→raw_access→admin），HMAC 签名 Cookie，全量审计日志。raw evidence 访问和 API key lookup 改动必须保留审计日志语义。

## Conventions

- 默认用中文沟通；代码、标识符、错误文本沿用项目现有语言。
- `main` 分支禁止直接进行功能开发或 bug 修复。遇到这类任务时，如果当前工作区位于 `main`，必须先创建并切换到独立的 git worktree，再开始后续实现、调试、测试与提交。
- 创建 worktree 时优先复用已存在的 `.worktrees/`（其次 `worktrees/`）目录；如果使用项目内目录，先确认该目录已被 `.gitignore` 忽略，避免误把 worktree 内容纳入版本控制。
- 不记录、不持久化 plaintext API key；相关逻辑使用 HMAC 指纹、元数据和脱敏证据。
- 修改 schema 时新增迁移文件（`migrations/` 按编号顺序），不改写已发布迁移。
- 查找文件与文本优先用 `rg` / `rg --files`。
- Python 依赖用 `uv` 管理。
- 代码修改完成后，主动检查 `README.md`、`ARCHITECTURE.md`、`CLAUDE.md` 等文档是否需要同步更新（如部署命令、架构描述、服务依赖关系），不要只做代码调整而遗漏文档。
