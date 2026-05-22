# Embedding 服务提升为必需依赖

日期: 2026-05-22

## 背景

Embedding 服务（HuggingFace TEI + BAAI/bge-m3）用于分析 Worker 的语义工作相关性分类。当前状态：

- docker-compose 中放在 `profiles: [tools]`，不随 `up -d` 启动
- `EmbeddingClient` 类已实现，但 `main.py` 所有入口函数均未初始化或传递该实例
- `process_trace()` 的 `embedding_client` 参数始终为 `None`，走纯关键词匹配降级路径
- 实际上 embedding 功能从未被使用过

## 目标

将 embedding 从可选增强提升为 Python 分析 Worker 的必需依赖，确保每条 trace 都经过语义分类。

## 约束

- 仅影响 Python Worker，Go 网关不受影响
- embedding 地址硬编码为 `http://embedding:80`（同一 docker-compose 网络）
- 离线批处理（`--offline-batch`）不需要 embedding，只做统计基线计算和模型训练

## 设计

### docker-compose.yml

**embedding 服务：**
- 移除 `profiles: - tools`，变为常驻服务
- 新增 healthcheck（`/health` 端点，给模型加载留 `start_period: 30s`，最多重试 30 次）
- 新增模型缓存卷 `embedding-model-cache:/data`，避免容器重建时重复下载 ~2GB 模型文件

**volumes 部分：**
- 新增命名卷 `embedding-model-cache`

**analysis-worker 服务：**
- 新增 `depends_on: embedding: condition: service_healthy`

**analysis-batch 服务：**
- 不变，保留 `profiles: [tools]`，不加 embedding 依赖

### embedding_client.py

新增 `wait_until_ready(timeout, interval)` 方法：
- 循环 GET `http://embedding:80/health`
- 返回 200 则返回
- 超时则抛异常退出 Worker 进程

### main.py

1. `main()` 函数中，Redis 消费启动前：创建 `EmbeddingClient("http://embedding:80")`，调用 `wait_until_ready()`
2. `--offline-batch` 分支不创建 EmbeddingClient，直接走 `run_offline_batch()`
3. 沿调用链传递 embedding_client：`main()` → `process_redis_loop()` → `process_redis_once()` → `process_job_line()` → `process_trace()`
4. `process_trace()` 的 `embedding_client` 参数从 `None` 默认值改为必需参数
5. 移除 `if embedding_client and pg_connection:` 条件判断，直接调用 `classify_work_relevance_with_embeddings()`
6. `classify_work_relevance_with_embeddings()` 中 embedding 请求失败时抛异常，终止消费循环

### 测试

`tests/test_embedding_client.py` 新增 `wait_until_ready()` 测试：正常就绪、超时失败、重试后成功。

## 改动文件

| 文件 | 改动 |
|------|------|
| `deploy/docker-compose.yml` | embedding 移出 profile、加 healthcheck；analysis-worker 加 depends_on |
| `workers/analysis_worker/embedding_client.py` | 新增 `wait_until_ready()` |
| `workers/analysis_worker/main.py` | 初始化 EmbeddingClient、传递参数、移除降级逻辑 |
| `workers/analysis_worker/tests/test_embedding_client.py` | 补测试 |

## 不改动的部分

- Go 网关代码
- `offline.py` / analysis-batch
- `.env.example`（无新环境变量）
- `work_relevance.py`（现有逻辑不变）
