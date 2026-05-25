# Linux 部署配置整合 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 ECS 部署验证过的配置整合进项目代码：embedding 改 build 模式、移除外部网络依赖、补充 worker 环境变量、添加 healthcheck。

**Architecture:** 两个 docker-compose stack 完全解耦，gateway 通过 `.env` 配置的 DSN 连接 new-api postgres。embedding 从 TEI 预构建镜像改为本地 Python Dockerfile 构建。移除所有 `networks:` 声明。

**Tech Stack:** Docker Compose, Python (FastAPI + sentence-transformers), Go

---

### Task 1: embedding server.py 模型名改为环境变量

**Files:**
- Modify: `deploy/embedding/server.py:11`

- [ ] **Step 1: 修改 MODEL_NAME 为环境变量读取**

```python
# 第 11 行，将：
MODEL_NAME = "BAAI/bge-m3"
# 改为：
MODEL_NAME = os.environ.get("EMBEDDING_MODEL", "BAAI/bge-m3")
```

`os` 已在文件第 3 行 import，无需额外 import。

- [ ] **Step 2: 验证语法正确**

Run: `python -c "import ast; ast.parse(open('deploy/embedding/server.py').read()); print('OK')"`

Expected: `OK`

- [ ] **Step 3: Commit**

```bash
git add deploy/embedding/server.py
git commit -m "feat(embedding): make model name configurable via EMBEDDING_MODEL env var"
```

---

### Task 2: 创建 embedding healthcheck 脚本

**Files:**
- Create: `deploy/embedding/healthcheck.py`

- [ ] **Step 1: 创建 healthcheck.py**

```python
import urllib.request, sys

try:
    urllib.request.urlopen("http://localhost:8000/health", timeout=2)
except Exception:
    sys.exit(1)
```

- [ ] **Step 2: Commit**

```bash
git add deploy/embedding/healthcheck.py
git commit -m "feat(embedding): add healthcheck script for Docker HEALTHCHECK"
```

---

### Task 3: 更新 embedding Dockerfile

**Files:**
- Modify: `deploy/embedding/Dockerfile:15-19`

当前 Dockerfile 内容：
```dockerfile
COPY --from=builder /install /usr/local

WORKDIR /app
COPY server.py .

EXPOSE 8000
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8000"]
```

- [ ] **Step 1: 添加 healthcheck.py COPY 和 HEALTHCHECK 指令**

将 `COPY server.py .` 改为 `COPY server.py healthcheck.py .`，在 `EXPOSE` 前添加 `HEALTHCHECK`：

```dockerfile
COPY --from=builder /install /usr/local

WORKDIR /app
COPY server.py healthcheck.py .

HEALTHCHECK --interval=5s --timeout=3s --retries=30 --start-period=30s \
    CMD ["python", "/app/healthcheck.py"]

EXPOSE 8000
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8000"]
```

- [ ] **Step 2: 验证 Dockerfile 语法**

Run: `docker build --check deploy/embedding/ 2>&1 || echo "check not supported, trying config parse instead" && grep -c 'HEALTHCHECK' deploy/embedding/Dockerfile`

Expected: 输出包含 `1`（一个 HEALTHCHECK 指令）

- [ ] **Step 3: Commit**

```bash
git add deploy/embedding/Dockerfile
git commit -m "feat(embedding): add Dockerfile HEALTHCHECK with healthcheck.py"
```

---

### Task 4: 重写 docker-compose.yml

这是最大的改动。按以下顺序修改 `deploy/docker-compose.yml`：

**Files:**
- Modify: `deploy/docker-compose.yml`

- [ ] **Step 1: embedding 服务改为 build 模式**

将 embedding 服务（约第 123-136 行）从：
```yaml
  embedding:
    image: ${EMBEDDING_IMAGE:-ghcr.io/huggingface/text-embeddings-inference:latest}
    platform: ${EMBEDDING_PLATFORM:-linux/amd64}
    environment:
      HF_ENDPOINT: ${HF_ENDPOINT:-https://hf-mirror.com}
    command: --model-id BAAI/bge-m3 --port 8000
    volumes:
      - embedding-model-cache:/data
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8000/health"]
      interval: 5s
      timeout: 3s
      retries: 30
      start_period: 30s
```

改为：
```yaml
  embedding:
    build:
      context: ./embedding
    environment:
      HF_ENDPOINT: ${HF_ENDPOINT:-https://hf-mirror.com}
      HF_HOME: /data
      TRANSFORMERS_CACHE: /data
    volumes:
      - embedding-model-cache:/data
    healthcheck:
      test: ["CMD", "python", "/app/healthcheck.py"]
      interval: 5s
      timeout: 3s
      retries: 30
      start_period: 30s
```

变更点：
- `image:` → `build:` + `context: ./embedding`
- 移除 `platform:`
- 移除 `command:`（不再需要 TEI CLI 参数）
- 移除 `start_period: 30s`（已在 Dockerfile HEALTHCHECK 中定义，compose 层覆盖为相同值保持不变）
- 添加 `HF_HOME: /data` 和 `TRANSFORMERS_CACHE: /data`（模型缓存到 volume）
- healthcheck 从 `curl` 改为 `python /app/healthcheck.py`

- [ ] **Step 2: 移除所有 networks 声明**

1. 删除 `audit-gateway` 下的 `networks:` 和 `- new-api_new-api-network`（约第 53-54 行）
2. 删除 `analysis-worker` 下的 `networks:` 和 `- new-api_new-api-network`（约第 80-81 行）
3. 删除文件末尾的整个 `networks:` 块：
```yaml
networks:
  new-api_new-api-network:
    external: true
```

- [ ] **Step 3: 补充 analysis-worker 环境变量**

在 analysis-worker 的 `environment:` 块中，`UV_EXTRA_INDEX_URL` 行之后添加：

```yaml
      EMBEDDING_URL: http://embedding:8000
      UV_INDEX_STRATEGY: unsafe-best-match
```

- [ ] **Step 4: 补充 analysis-batch 环境变量**

在 analysis-batch 的 `environment:` 块中，`UV_INDEX_URL` 行之后添加：

```yaml
      UV_INDEX_STRATEGY: unsafe-best-match
```

- [ ] **Step 5: 验证 compose 配置**

Run: `docker compose -f deploy/docker-compose.yml --env-file .env.local config --quiet 2>&1 || docker compose -f deploy/docker-compose.yml --env-file .env.local config 2>&1 | head -5`

Expected: 无 YAML 语法错误。可能因缺少 `.env.local` 报变量未设，但不应有 YAML parse error。

- [ ] **Step 6: Commit**

```bash
git add deploy/docker-compose.yml
git commit -m "feat(deploy): embedding build mode, remove external network, add worker env vars"
```

---

### Task 5: 简化 docker-compose.arm.yml

ARM Mac override 不再需要覆盖 `image`、`platform`、`command`（base compose 已改为 build 模式）。仅需保留 healthcheck timing 调整和 volume 路径覆盖。

**Files:**
- Modify: `deploy/docker-compose.arm.yml`

- [ ] **Step 1: 替换为简化版**

将整个文件替换为：

```yaml
# ARM Mac local development override.
# Stacks on top of docker-compose.yml via: make dev
services:
  embedding:
    healthcheck:
      start_period: 120s
      retries: 60
```

变更点：
- 移除 `image: ""` 和 `platform: ""`（base compose 不再有这些字段）
- 移除 `build:` override（base compose 已使用 build）
- 移除 `command: !override []`（base compose 不再有 TEI command）
- 移除 `volumes: !override`（base compose 已将 HF_HOME/TRANSFORMERS_CACHE 指向 `/data`，volume 已挂载到 `/data`）
- healthcheck test 从内联 python 改为使用 Dockerfile 内置的 healthcheck.py
- 保留 `start_period: 120s` 和 `retries: 60`（ARM 模型加载较慢）

- [ ] **Step 2: 验证 override 合并正确**

Run: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml config 2>&1 | grep -A5 "start_period"`

Expected: 输出包含 `start_period: 2m0s`（120s）或 `120s`

- [ ] **Step 3: Commit**

```bash
git add deploy/docker-compose.arm.yml
git commit -m "refactor(deploy): simplify ARM override after embedding build mode switch"
```

---

### Task 6: 更新 .env.example

**Files:**
- Modify: `.env.example`

- [ ] **Step 1: 替换 Embedding 服务区块**

将 `.env.example` 中的 Embedding 区块（约第 38-45 行）从：
```
# ── Embedding 服务 ─────────────────────────────────────
# 默认使用 HuggingFace TEI（需要 x86_64 Linux 或 NVIDIA GPU）
# Linux GPU:   EMBEDDING_IMAGE=ghcr.io/huggingface/text-embeddings-inference:latest
# Linux CPU:   EMBEDDING_IMAGE=ghcr.io/huggingface/text-embeddings-inference:cpu-latest
# macOS ARM:   无需配置，运行 make dev 自动使用轻量 Python embedding 服务
# EMBEDDING_IMAGE=ghcr.io/huggingface/text-embeddings-inference:cpu-latest
# EMBEDDING_PLATFORM=linux/amd64
# EMBEDDING_PORT=8081
```

改为：
```
# ── Embedding 服务 ─────────────────────────────────────
# 使用本地构建的 Python embedding 服务（FastAPI + sentence-transformers）
# 模型默认 BAAI/bge-m3，可通过 EMBEDDING_MODEL 环境变量覆盖
# HuggingFace 镜像（中国区默认 hf-mirror.com）
# HF_ENDPOINT=https://hf-mirror.com
# EMBEDDING_MODEL=BAAI/bge-m3
```

- [ ] **Step 2: 在 Embedding 区块之后添加 Worker 区块**

在 Embedding 区块之后添加：
```
# ── Analysis Worker ─────────────────────────────────────
# pip index 冲突解决策略（默认 unsafe-best-match）
# UV_INDEX_STRATEGY=unsafe-best-match
```

- [ ] **Step 3: Commit**

```bash
git add .env.example
git commit -m "docs: update .env.example for build-mode embedding and worker config"
```

---

### Task 7: 更新 ARCHITECTURE.md

**Files:**
- Modify: `ARCHITECTURE.md` — Docker Compose 服务表格和目录结构

- [ ] **Step 1: 更新目录结构描述**

将目录结构中 `deploy/` 部分从：
```
├── deploy/                    # Docker Compose 依赖服务
│   ├── docker-compose.yml     # 基础 compose（TEI embedding，x86/GPU）
│   ├── docker-compose.arm.yml # ARM Mac override（Python embedding）
│   └── embedding/             # ARM 原生 embedding 服务（FastAPI + sentence-transformers）
```

改为：
```
├── deploy/                    # Docker Compose 部署配置
│   ├── Dockerfile             # Go 网关多阶段构建
│   ├── docker-compose.yml     # 生产 compose（Python embedding，无外部网络依赖）
│   ├── docker-compose.arm.yml # ARM Mac override（模型加载超时调整）
│   └── embedding/             # Embedding 服务（FastAPI + sentence-transformers + healthcheck）
```

- [ ] **Step 2: 更新 Docker Compose 服务表格中 embedding 行**

将表格最后一行从：
```
| `embedding` | `${EMBEDDING_IMAGE}`（默认 TEI） | 默认启动 | bge-m3 嵌入服务（语义工作相关性分类，必需）。Linux GPU 用默认值，CPU 设 `EMBEDDING_IMAGE=...cpu-latest`；macOS ARM 通过 `docker-compose.arm.yml` 使用本地构建的 Python embedding 服务（`deploy/embedding/`） |
```

改为：
```
| `embedding` | 本地构建 `deploy/embedding/Dockerfile` | 默认启动 | bge-m3 嵌入服务（FastAPI + sentence-transformers，语义工作相关性分类，必需）。模型缓存到 Docker volume，通过 `HF_ENDPOINT` 环境变量配置 HuggingFace 镜像 |
```

- [ ] **Step 3: Commit**

```bash
git add ARCHITECTURE.md
git commit -m "docs: update ARCHITECTURE.md for build-mode embedding and decoupled deployment"
```

---

### Task 8: 更新 CLAUDE.md（如有必要）

CLAUDE.md 的部署命令部分已经正确（`make dev -d` 和手动 compose 命令），无需修改。Commands 区块的注释 `# 依赖服务（ARM Mac 自动使用原生 embedding）` 仍然准确，因为 `make dev` 在 ARM 上自动使用 arm override。

- [ ] **Step 1: 确认 CLAUDE.md 无需修改**

Run: `grep -n "EMBEDDING_IMAGE\|TEI\|external.*network" CLAUDE.md`

Expected: 无匹配（CLAUDE.md 不包含这些细节）。

如需修改，按 spec 更新。否则跳过。

---

### Task 9: 端到端验证

- [ ] **Step 1: 本地 compose config 验证**

Run: `docker compose -f deploy/docker-compose.yml config 2>&1 | grep -E "^(services|networks)" | head -5`

Expected:
- 无 `networks` 输出（已移除外部网络）
- services 列表包含所有 7 个服务

- [ ] **Step 2: ARM Mac override 合并验证**

Run: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml config 2>&1 | grep -A3 "embedding" | head -10`

Expected: embedding 服务显示 `build` 而非 `image`，healthcheck start_period 为 120s

- [ ] **Step 3: embedding 镜像构建测试（可选，需 Docker 运行中）**

Run: `docker build -t embedding-test deploy/embedding/`

Expected: 构建成功，HEALTHCHECK 指令在构建输出中可见

---

## Self-Review Checklist

### Spec Coverage

| Spec Section | Task |
|---|---|
| 1. 网络拓扑（移除外部网络） | Task 4 Step 2 |
| 2.1 移除外部网络 | Task 4 Step 2 |
| 2.2 embedding 改 build 模式 | Task 4 Step 1 |
| 2.3 analysis-worker 环境变量 | Task 4 Step 3-4 |
| 2.4 audit-gateway 移除 networks | Task 4 Step 2 |
| 3.1 embedding Dockerfile HEALTHCHECK | Task 3 |
| 3.2 healthcheck.py | Task 2 |
| 4. Go Dockerfile apt-get | 已在代码中，无需修改 |
| 6. .env.example 补充 | Task 6 |
| 7. server.py 环境变量 | Task 1 |
| 变更文件清单 — ARCHITECTURE.md | Task 7 |
| 变更文件清单 — CLAUDE.md | Task 8（确认无需修改） |

### Placeholder Scan

无 TBD、TODO、implement later、fill in details。

### Type Consistency

`EMBEDDING_MODEL` 环境变量名在 Task 1（server.py）和 Task 6（.env.example）中一致。
`UV_INDEX_STRATEGY` 在 Task 4（compose）和 Task 6（.env.example）中一致。
`HF_ENDPOINT` 在 Task 4（compose）和 Task 6（.env.example）中一致。
