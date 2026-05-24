# ARM Mac 本地开发 Embedding 服务方案

**日期：** 2026-05-24
**状态：** 已批准
**目标：** 让 ARM Mac 开发者无需 QEMU 模拟即可运行完整分析管线

## 背景

当前 embedding 服务使用 HuggingFace TEI（`ghcr.io/huggingface/text-embeddings-inference`），该镜像仅有 `linux/amd64` 构建，在 ARM Mac 上需通过 QEMU 模拟运行，导致：

- 镜像下载 2.3GB
- 模型加载耗时 60s+
- 推理延迟高，CPU 占用大
- 开发体验差

## 方案

创建一个轻量 Python FastAPI 服务替代 TEI，仅在本地开发时使用，通过 docker-compose override 文件自动切换。

### 自动切换机制

```
make dev
  ├─ 检测 uname -m
  ├─ arm64 → 叠加 docker-compose.arm.yml（Python embedding 服务）
  └─ x86   → 仅用 docker-compose.yml（TEI，不变）
```

- 生产部署不受影响，继续用 TEI
- x86 开发者也不受影响
- ARM Mac 开发者一个命令 `make dev` 启动全栈

### Python Embedding 服务

**技术栈：** FastAPI + sentence-transformers（BAAI/bge-m3）

**API 兼容性：**
- `POST /v1/embeddings` — 兼容 OpenAI embedding API 格式（与 TEI 一致）
- `GET /health` — 模型加载完成后返回 200

**镜像：**
- 基础镜像 `python:3.11-slim`（双平台 ARM64/AMD64）
- 多阶段构建：依赖安装 → 运行时
- 模型缓存到 `embedding-model-cache` Docker volume，首次下载后复用

### 与现有代码的关系

- `EmbeddingClient`（`embedding_client.py`）**零改动**
- `docker-compose.yml`（生产配置）**不变**
- Python worker 代码**不变**

## 文件变更

| 操作 | 文件 | 说明 |
|---|---|---|
| 新建 | `deploy/embedding/Dockerfile` | Python embedding 服务镜像 |
| 新建 | `deploy/embedding/server.py` | FastAPI 服务，兼容 TEI API |
| 新建 | `deploy/docker-compose.arm.yml` | ARM 开发 override |
| 修改 | `Makefile` | 加 `dev` target，自动检测平台 |
| 修改 | `.env.example` | 更新 Embedding 段注释，说明 ARM Mac 用 `make dev` 自动启用 |
| 修改 | `README.md` | 本地开发说明 |
| 修改 | `CLAUDE.md` | Commands 部分 |

## server.py 设计

```
FastAPI app
├── startup event: 加载 SentenceTransformer("BAAI/bge-m3") 到内存
├── GET  /health
│   模型未就绪 → 503
│   模型已就绪 → {"status": "ok"}
├── POST /v1/embeddings
│   输入: {"input": str | list[str], "model": "BAAI/bge-m3"}
│   处理: model.encode(input) → 归一化
│   输出: {"data": [{"embedding": [...]}], "model": "...", "usage": {...}}
└── 运行: uvicorn on 0.0.0.0:8000
```

## docker-compose.arm.yml

覆盖 `embedding` 服务：
- `build.context: ./embedding` 替代 `image: TEI`
- 端口映射到 `${EMBEDDING_PORT:-8081}:8000`
- 复用 `embedding-model-cache` volume

## Makefile dev target

```makefile
PLATFORM := $(shell uname -m)

ifeq ($(PLATFORM),arm64)
DEV_COMPOSE = -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml
else
DEV_COMPOSE = -f deploy/docker-compose.yml
endif

.PHONY: dev
dev:
	docker compose $(DEV_COMPOSE) --env-file .env.local up
```
