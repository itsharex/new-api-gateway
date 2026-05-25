# Linux 部署配置整合

## 背景

在阿里云 ECS（47.113.144.13）部署 gateway 时，因 GFW 封锁 ghcr.io、网络与 new-api 共享 flat 网络导致 DNS 冲突等问题，对部署配置做了多处临时修改。需将验证过的修改整合进项目代码。

## 设计

### 1. 网络拓扑：双 stack 完全解耦

两个 docker-compose stack 各自独立运行，无共享网络。跨栈数据库连接通过 `.env` 配置主机地址。

```
┌─ new-api compose ──────────┐    ┌─ gateway compose ──────────┐
│  new-api services           │    │  audit-gateway              │
│  postgres ──► :5432 (host)  │◄───│    connects via .env DSN    │
│  redis                      │    │  postgres                   │
└─────────────────────────────┘    │  redis                      │
     完全独立                       │  embedding                  │
                                   │  analysis-worker            │
                                   └─────────────────────────────┘
                                        完全独立，无外部网络
```

- new-api 的 postgres 暴露主机端口（在 new-api 的 compose 中 `ports: "5432:5432"`）
- gateway 通过 `NEW_API_POSTGRES_DSN` 环境变量连接，地址由 `.env` 文件配置
- 去掉 `new-api_new-api-network` 外部网络依赖
- gateway compose 使用默认 bridge 网络（不声明自定义 network）

**优势：**
- 解耦部署，两个 stack 互不影响
- 未来拆分多机部署只需改 `.env` 地址
- 本地开发和线上部署用同一份 compose 文件，仅 `.env` 不同

### 2. docker-compose.yml 修改

#### 2.1 移除外部网络

```yaml
# 删除
networks:
  new-api_new-api-network:
    external: true
```

所有服务的 `networks:` 配置移除。gateway stack 使用 Docker 默认 bridge 网络，服务间通过 compose 服务名互相访问。

#### 2.2 embedding 服务改为 build 模式

TEI 镜像（ghcr.io）在中国被墙，改用本地 Python Dockerfile 构建：

```yaml
embedding:
  build:
    context: ./embedding
  environment:
    HF_ENDPOINT: ${HF_ENDPOINT:-https://hf-mirror.com}
    HF_HOME: /data
    TRANSFORMERS_CACHE: /data
  command: --model-id BAAI/bge-m3 --port 8000
  volumes:
    - embedding-model-cache:/data
  healthcheck:
    test: ["CMD", "python", "/app/healthcheck.py"]
    interval: 5s
    timeout: 3s
    retries: 30
    start_period: 30s
```

删除 `image:` 和 `platform:` 指令。`EMBEDDING_IMAGE` 和 `EMBEDDING_PLATFORM` 环境变量不再需要。

embedding 的 `command` 中的 `--model-id` 和 `--port` 参数是 TEI 的 CLI 参数，Python 服务不需要。改为在 `server.py` 中通过环境变量配置模型名，compose 中去掉 `command` 覆盖。

#### 2.3 analysis-worker 环境变量补充

```yaml
analysis-worker:
  environment:
    # 新增
    EMBEDDING_URL: http://embedding:8000
    UV_INDEX_STRATEGY: unsafe-best-match
```

- `EMBEDDING_URL`：显式声明，不依赖代码默认值
- `UV_INDEX_STRATEGY`：解决多 pip index 冲突（alibaba + pytorch）

#### 2.4 audit-gateway 移除 networks 和 extra_hosts

```yaml
audit-gateway:
  # 不再需要 networks 或 extra_hosts
  # NEW_API_POSTGRES_DSN 由 .env 配置实际地址
  environment:
    REDIS_ADDR: redis:6379    # compose 服务名，无前缀
```

### 3. embedding Dockerfile 增强

#### 3.1 添加 HEALTHCHECK（deploy/embedding/Dockerfile）

```dockerfile
COPY server.py healthcheck.py .
HEALTHCHECK --interval=5s --timeout=3s --retries=30 --start-period=30s \
    CMD ["python", "/app/healthcheck.py"]
```

#### 3.2 新增 healthcheck.py（deploy/embedding/healthcheck.py）

```python
import urllib.request, sys
try:
    urllib.request.urlopen("http://localhost:8000/health", timeout=2)
except Exception:
    sys.exit(1)
```

python:3.11-slim 无 curl，用 Python 标准库实现 healthcheck。

### 4. Go Dockerfile（deploy/Dockerfile）

添加 apt-get 宽容标志，应对 debian bookworm GPG key 过期：

```dockerfile
RUN apt-get update --allow-insecure-repositories || true && \
    apt-get install -y --no-install-recommends --allow-unauthenticated ca-certificates && \
    rm -rf /var/lib/apt/lists/*
```

### 5. 不纳入项目代码的修改

| 修改 | 原因 |
|------|------|
| `/etc/docker/daemon.json` 镜像加速 | 服务器运维配置，非项目代码 |
| `UV_INDEX_URL` / `UV_EXTRA_INDEX_URL` | 已在 compose 中配置，无需额外修改 |
| pip mirror 配置 | 已在 embedding Dockerfile 中配置 |

### 6. .env.example 补充

新增变量：

```env
# Embedding 服务
HF_ENDPOINT=https://hf-mirror.com

# Analysis Worker
UV_INDEX_STRATEGY=unsafe-best-match

# 跨栈连接（按实际环境配置）
# 本地开发：
# NEW_API_POSTGRES_DSN=postgres://user:pass@host.docker.internal:5432/new_api
# 线上部署：
# NEW_API_POSTGRES_DSN=postgres://user:pass@47.113.144.13:5432/new_api
```

### 7. embedding server.py 调整

当前 `command: --model-id BAAI/bge-m3 --port 8000` 是 TEI 的 CLI 参数格式。改为 Python 服务后，模型名通过环境变量配置：

```python
MODEL_NAME = os.environ.get("EMBEDDING_MODEL", "BAAI/bge-m3")
```

compose 中通过环境变量传入，不覆盖 command。

## 变更文件清单

| 文件 | 操作 |
|------|------|
| `deploy/docker-compose.yml` | 移除外部网络、embedding 改 build、补充 worker 环境变量、移除所有 networks 声明 |
| `deploy/Dockerfile` | apt-get 添加宽容标志 |
| `deploy/embedding/Dockerfile` | 添加 HEALTHCHECK、COPY healthcheck.py |
| `deploy/embedding/healthcheck.py` | 新建 |
| `deploy/embedding/server.py` | 模型名改为环境变量配置 |
| `.env.example` | 补充 HF_ENDPOINT、UV_INDEX_STRATEGY |
| `ARCHITECTURE.md` | 更新部署架构描述（双 stack 解耦） |
| `CLAUDE.md` | 更新部署命令说明 |
