# ARM Embedding Dev Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a lightweight Python embedding server for ARM Mac local development, auto-switched via docker-compose override and `make dev`.

**Architecture:** A FastAPI app (`deploy/embedding/server.py`) loads BAAI/bge-m3 via sentence-transformers and serves an OpenAI-compatible `/v1/embeddings` API. On ARM Macs, `make dev` detects `uname -m == arm64` and stacks `docker-compose.arm.yml` which overrides the `embedding` service to build from the local Dockerfile instead of pulling the x86-only TEI image. Production and x86 dev are unaffected.

**Tech Stack:** Python 3.11, FastAPI, uvicorn, sentence-transformers, Docker multi-stage build

---

### Task 1: Create the Python embedding server

**Files:**
- Create: `deploy/embedding/server.py`

This is the core FastAPI app that replaces TEI for local development. It must respond to `/health` and `/v1/embeddings` identically to what `EmbeddingClient` expects.

- [ ] **Step 1: Create `deploy/embedding/server.py`**

```python
"""Lightweight embedding server compatible with HuggingFace TEI /v1/embeddings API."""

import time

from fastapi import FastAPI
from fastapi.responses import JSONResponse
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer

MODEL_NAME = "BAAI/bge-m3"

app = FastAPI()

_model: SentenceTransformer | None = None


class EmbeddingRequest(BaseModel):
    input: str | list[str]
    model: str = MODEL_NAME


class EmbeddingResponse(BaseModel):
    object: str = "list"
    data: list[dict]
    model: str
    usage: dict


@app.on_event("startup")
def load_model() -> None:
    global _model
    _model = SentenceTransformer(MODEL_NAME)


@app.get("/health")
def health() -> dict:
    if _model is None:
        return JSONResponse({"status": "loading"}, status_code=503)
    return {"status": "ok"}


@app.post("/v1/embeddings")
def embeddings(req: EmbeddingRequest) -> EmbeddingResponse:
    if _model is None:
        return JSONResponse({"error": "model not ready"}, status_code=503)

    texts = req.input if isinstance(req.input, list) else [req.input]
    vectors = _model.encode(texts, normalize_embeddings=True).tolist()

    data = [
        {"object": "embedding", "index": i, "embedding": vec}
        for i, vec in enumerate(vectors)
    ]
    return EmbeddingResponse(
        data=data,
        model=MODEL_NAME,
        usage={"prompt_tokens": sum(len(t.split()) for t in texts), "total_tokens": sum(len(t.split()) for t in texts)},
    )
```

- [ ] **Step 2: Verify the file was created**

Run: `cat deploy/embedding/server.py | head -5`
Expected: Shows the module docstring

- [ ] **Step 3: Commit**

```bash
git add deploy/embedding/server.py
git commit -m "feat(embedding): add lightweight Python embedding server for ARM dev"
```

---

### Task 2: Create the Dockerfile

**Files:**
- Create: `deploy/embedding/Dockerfile`
- Create: `deploy/embedding/requirements.txt`

Multi-stage build that produces a small ARM-native image. The model is downloaded at build time and cached in the Docker layer; at runtime the volume `embedding-model-cache` provides persistence across rebuilds.

- [ ] **Step 1: Create `deploy/embedding/requirements.txt`**

```
fastapi==0.115.12
uvicorn[standard]==0.34.2
sentence-transformers==4.1.0
```

- [ ] **Step 2: Create `deploy/embedding/Dockerfile`**

```dockerfile
FROM python:3.11-slim AS builder

WORKDIR /build
COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

FROM python:3.11-slim

COPY --from=builder /install /usr/local

WORKDIR /app
COPY server.py .

EXPOSE 8000
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8000"]
```

- [ ] **Step 3: Commit**

```bash
git add deploy/embedding/Dockerfile deploy/embedding/requirements.txt
git commit -m "feat(embedding): add Dockerfile for ARM-native embedding server"
```

---

### Task 3: Create docker-compose ARM override

**Files:**
- Create: `deploy/docker-compose.arm.yml`

This override replaces the `embedding` service definition when running on ARM Mac. It switches from the TEI image to building from the local Dockerfile.

- [ ] **Step 1: Create `deploy/docker-compose.arm.yml`**

```yaml
# ARM Mac local development override.
# Stacks on top of docker-compose.yml via: make dev
services:
  embedding:
    image: ""
    build:
      context: ./embedding
    platform: ""
    ports:
      - "${EMBEDDING_PORT:-8081}:8000"
    command: []
    healthcheck:
      test: ["CMD", "python", "-c", "import urllib.request; urllib.request.urlopen('http://localhost:8000/health')"]
      interval: 5s
      timeout: 3s
      retries: 60
      start_period: 120s
    volumes:
      - embedding-model-cache:/root/.cache
```

Notes:
- `image: ""` and `platform: ""` clear the values from the base compose file
- `command: []` clears the TEI `--model-id` command
- `start_period: 120s` gives sentence-transformers time to download bge-m3 (~2GB) on first run
- Volume maps to `/root/.cache` where sentence-transformers stores downloaded models

- [ ] **Step 2: Validate compose file**

Run: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml config --services`
Expected: Lists all services including `embedding` with no errors

- [ ] **Step 3: Commit**

```bash
git add deploy/docker-compose.arm.yml
git commit -m "feat(deploy): add docker-compose ARM override for local embedding"
```

---

### Task 4: Add `make dev` target

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Add platform detection and `dev` target to `Makefile`**

Append after the existing `.PHONY` line at the top. The full updated Makefile:

```makefile
PLATFORM := $(shell uname -m)

ifeq ($(PLATFORM),arm64)
DEV_COMPOSE = -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml
else
DEV_COMPOSE = -f deploy/docker-compose.yml
endif

.PHONY: test run tidy smoke dev

test:
	go test ./...

run:
	go run ./cmd/audit-gateway

tidy:
	go mod tidy

smoke:
	./scripts/smoke_proxy.sh

dev:
	docker compose $(DEV_COMPOSE) --env-file .env.local up
```

- [ ] **Step 2: Verify Makefile parses**

Run: `make -n dev`
Expected on ARM Mac: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml --env-file .env.local up`
Expected on x86: `docker compose -f deploy/docker-compose.yml --env-file .env.local up`

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "feat: add make dev target with ARM platform auto-detection"
```

---

### Task 5: Update .env.example

**Files:**
- Modify: `.env.example:38-45`

- [ ] **Step 1: Update the Embedding section comment**

Replace lines 38-45 (the Embedding section) with:

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

- [ ] **Step 2: Commit**

```bash
git add .env.example
git commit -m "docs: update .env.example embedding section for ARM Mac"
```

---

### Task 6: Update README.md

**Files:**
- Modify: `README.md:94-104` (Embedding 服务配置 section)
- Modify: `README.md:108-131` (本地开发 section)

- [ ] **Step 1: Update Embedding 配置 section (lines 94-104)**

Replace the entire "Embedding 服务配置" section with:

```markdown
#### Embedding 服务配置

Embedding 服务用于语义搜索和异常检测。Linux 环境通过环境变量控制镜像和平台：

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `EMBEDDING_IMAGE` | TEI 镜像地址 | `ghcr.io/huggingface/text-embeddings-inference:latest` |
| `EMBEDDING_PLATFORM` | 目标平台 | `linux/amd64` |
| `EMBEDDING_PORT` | 宿主机端口 | `8081` |

Linux GPU 服务器使用默认值即可；Linux CPU 服务器设置 `EMBEDDING_IMAGE=ghcr.io/huggingface/text-embeddings-inference:cpu-latest`。macOS ARM 开发环境使用 `make dev` 自动启动轻量 Python embedding 服务，无需手动配置。
```

- [ ] **Step 2: Update 本地开发 section (lines 108-131)**

Replace the entire "本地开发" section with:

```markdown
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

`make dev` 会自动检测平台：ARM Mac 叠加 `docker-compose.arm.yml` 使用原生 Python embedding 服务；x86 环境使用 TEI。
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: update README for ARM Mac embedding support"
```

---

### Task 7: Update CLAUDE.md Commands section

**Files:**
- Modify: `CLAUDE.md:34-36` (依赖服务 section)

- [ ] **Step 1: Add `make dev` to the 依赖服务 section**

Replace lines 34-36 with:

```markdown
# 依赖服务（ARM Mac 自动使用原生 embedding）
make dev -d
# 或手动指定 compose 文件
docker compose -f deploy/docker-compose.yml --env-file .env.local up -d postgres redis
docker compose -f deploy/docker-compose.yml --env-file .env.local --profile tools run --rm migrate
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md with make dev for ARM embedding"
```

---

### Task 8: Integration test — build and verify

This task verifies the full stack works on the current machine.

- [ ] **Step 1: Build the embedding image**

Run: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml build embedding`
Expected: Build succeeds, image tagged

- [ ] **Step 2: Start only the embedding service**

Run: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml up embedding -d`
Expected: Container starts, first run downloads bge-m3 model (~2GB)

- [ ] **Step 3: Wait for health check**

Run: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml logs -f embedding`
Wait for: `Application startup complete` in logs

Then verify:
Run: `curl -s http://localhost:8081/health`
Expected: `{"status":"ok"}`

- [ ] **Step 4: Test embedding endpoint**

Run: `curl -s http://localhost:8081/v1/embeddings -H 'Content-Type: application/json' -d '{"input":"hello world","model":"BAAI/bge-m3"}' | python3 -m json.tool | head -10`
Expected: JSON with `data[0].embedding` containing a 1024-float vector

- [ ] **Step 5: Verify compatibility with EmbeddingClient**

Run: `cd workers/analysis_worker && uv run python -c "from embedding_client import EmbeddingClient; c = EmbeddingClient('http://localhost:8081'); v = c.embed('test'); print(f'dim={len(v)}, first={v[0]:.4f}')" `
Expected: `dim=1024, first=<float_value>`

- [ ] **Step 6: Clean up**

Run: `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml down`

- [ ] **Step 7: Final commit (if any fixes were needed)**

If any issues were found and fixed during integration testing, commit them:

```bash
git add -A
git commit -m "fix(embedding): address integration test findings"
```
