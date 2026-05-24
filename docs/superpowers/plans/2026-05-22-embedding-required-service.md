# Embedding 必需依赖 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 embedding 服务从可选 tools profile 提升为 analysis-worker 的必需依赖，确保每条 trace 都经过语义工作相关性分类。

**Architecture:** embedding 服务（HuggingFace TEI + BAAI/bge-m3）从 docker-compose tools profile 移至常驻服务。Python Worker 启动时创建 EmbeddingClient 并等待就绪，沿调用链传递到 process_trace()，替换原有的关键词降级路径。离线批处理不受影响。

**Tech Stack:** Python 3.11, httpx, Docker Compose, HuggingFace TEI

---

### Task 1: EmbeddingClient.wait_until_ready() 方法

**Files:**
- Modify: `workers/analysis_worker/embedding_client.py`
- Test: `workers/analysis_worker/tests/test_embedding_client.py`

- [ ] **Step 1: 为 wait_until_ready 写失败测试**

在 `workers/analysis_worker/tests/test_embedding_client.py` 末尾追加：

```python
import time
import pytest
from unittest.mock import patch, MagicMock


def test_wait_until_ready_succeeds_immediately():
    mock_resp = MagicMock()
    mock_resp.status_code = 200

    with patch("embedding_client.httpx.get", return_value=mock_resp):
        client = EmbeddingClient("http://embedding:80")
        client.wait_until_ready(timeout=5, interval=0.1)

    # no exception = success


def test_wait_until_ready_retries_then_succeeds():
    fail_resp = MagicMock()
    fail_resp.status_code = 503

    ok_resp = MagicMock()
    ok_resp.status_code = 200

    with patch("embedding_client.httpx.get", side_effect=[fail_resp, fail_resp, ok_resp]):
        client = EmbeddingClient("http://embedding:80")
        client.wait_until_ready(timeout=5, interval=0.05)

    # no exception = success after retries


def test_wait_until_ready_raises_on_timeout():
    fail_resp = MagicMock()
    fail_resp.status_code = 503

    with patch("embedding_client.httpx.get", return_value=fail_resp):
        client = EmbeddingClient("http://embedding:80")
        with pytest.raises(RuntimeError, match="embedding service not ready"):
            client.wait_until_ready(timeout=0.2, interval=0.05)
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd workers/analysis_worker && uv run pytest tests/test_embedding_client.py::test_wait_until_ready_succeeds_immediately -v`
Expected: FAIL — `AttributeError: 'EmbeddingClient' object has no attribute 'wait_until_ready'`

- [ ] **Step 3: 实现 wait_until_ready**

在 `workers/analysis_worker/embedding_client.py` 的 `EmbeddingClient` 类中，`embed_batch` 方法之后追加：

```python
    def wait_until_ready(self, timeout: float = 300, interval: float = 5) -> None:
        import time as _time
        deadline = _time.monotonic() + timeout
        while _time.monotonic() < deadline:
            try:
                resp = httpx.get(f"{self.base_url}/health", timeout=3.0)
                if resp.status_code == 200:
                    return
            except httpx.HTTPError:
                pass
            remaining = deadline - _time.monotonic()
            if remaining > 0:
                _time.sleep(min(interval, remaining))
        raise RuntimeError("embedding service not ready")
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd workers/analysis_worker && uv run pytest tests/test_embedding_client.py -v`
Expected: 全部 PASS（原有 3 个 + 新增 3 个 = 6 个）

- [ ] **Step 5: 提交**

```bash
git add workers/analysis_worker/embedding_client.py workers/analysis_worker/tests/test_embedding_client.py
git commit -m "feat(worker): add EmbeddingClient.wait_until_ready() with tests"
```

---

### Task 2: docker-compose.yml — embedding 提升为常驻服务

**Files:**
- Modify: `deploy/docker-compose.yml`

- [ ] **Step 1: 修改 embedding 服务**

将 `deploy/docker-compose.yml` 中的 embedding 服务（第 118-124 行）：

```yaml
  embedding:
    image: ghcr.io/huggingface/text-embeddings-inference:latest
    profiles:
      - tools
    ports:
      - "8081:80"
    command: --model-id BAAI/bge-m3 --port 80
```

替换为：

```yaml
  embedding:
    image: ghcr.io/huggingface/text-embeddings-inference:latest
    ports:
      - "8081:80"
    command: --model-id BAAI/bge-m3 --port 80
    volumes:
      - embedding-model-cache:/data
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:80/health"]
      interval: 5s
      timeout: 3s
      retries: 30
      start_period: 30s
```

变更点：移除 `profiles: - tools`，新增 `volumes` 和 `healthcheck`。

- [ ] **Step 2: analysis-worker 加 embedding 依赖**

在 analysis-worker 服务的 `depends_on` 中（第 59-64 行），在 redis 条目后追加：

```yaml
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      embedding:
        condition: service_healthy
```

- [ ] **Step 3: 新增命名卷**

在文件末尾的 `volumes:` 部分（第 126-127 行），追加：

```yaml
volumes:
  audit-postgres:
  audit-redis
  embedding-model-cache:
```

- [ ] **Step 4: 验证 YAML 语法**

Run: `docker compose -f deploy/docker-compose.yml config --quiet`
Expected: 无报错

- [ ] **Step 5: 提交**

```bash
git add deploy/docker-compose.yml
git commit -m "feat(deploy): promote embedding to required service with healthcheck and model cache"
```

---

### Task 3: main.py — 初始化 EmbeddingClient 并传递调用链

**Files:**
- Modify: `workers/analysis_worker/main.py`

- [ ] **Step 1: 添加 import**

在 `main.py` 第 29 行（`from work_relevance import classify_work_relevance`）之后追加：

```python
from embedding_client import EmbeddingClient
```

- [ ] **Step 2: 修改 process_job_line 签名和调用**

将 `process_job_line`（第 98 行）从：

```python
def process_job_line(line: str, evidence_store: EvidenceStore, repository, context_repository=None, storage_backend: str = "filesystem") -> dict:
```

改为：

```python
def process_job_line(line: str, evidence_store: EvidenceStore, repository, context_repository=None, storage_backend: str = "filesystem", embedding_client=None) -> dict:
```

将第 103 行的 `process_trace` 调用从：

```python
    return process_trace(job, request_body, response_body, repository, contexts, evidence_store, storage_backend=storage_backend)
```

改为：

```python
    return process_trace(job, request_body, response_body, repository, contexts, evidence_store, storage_backend=storage_backend, embedding_client=embedding_client)
```

- [ ] **Step 3: 修改 process_trace 签名和逻辑**

将 `process_trace` 中 `embedding_client=None` 参数前的默认值去掉，并移除降级分支。

第 119-133 行，将：

```python
    embedding_client=None,
    pg_connection=None,
) -> dict:
    extraction_context: MediaExtractionContext | None = None
    if evidence_store and job.request_raw_ref:
        evidence_dir = job.request_raw_ref.rsplit("/", 1)[0]
        extraction_context = MediaExtractionContext(evidence_store, evidence_dir, job.trace_id)
    messages, results = normalize_json_trace(job, request_body, response_body, extraction_context)
    if embedding_client and pg_connection:
        from work_relevance import classify_work_relevance_with_embeddings
        work_relevance = classify_work_relevance_with_embeddings(
            job, messages, list(contexts or []), embedding_client, pg_connection,
        )
    else:
        work_relevance = classify_work_relevance(job, messages, list(contexts or []))
```

改为：

```python
    embedding_client=None,
) -> dict:
    extraction_context: MediaExtractionContext | None = None
    if evidence_store and job.request_raw_ref:
        evidence_dir = job.request_raw_ref.rsplit("/", 1)[0]
        extraction_context = MediaExtractionContext(evidence_store, evidence_dir, job.trace_id)
    messages, results = normalize_json_trace(job, request_body, response_body, extraction_context)
    from work_relevance import classify_work_relevance_with_embeddings
    work_relevance = classify_work_relevance_with_embeddings(
        job, messages, list(contexts or []), embedding_client,
    )
```

注意：`classify_work_relevance_with_embeddings` 的签名中 `pg_connection` 参数需要同步修改（见 Task 4）。此处调用不传 `pg_connection`。

- [ ] **Step 4: 修改 process_stdin 传递 embedding_client**

将 `process_stdin`（第 166-178 行）中的 `process_job_line` 调用改为接收 embedding_client。但由于 stdin 模式是开发调试用，不需要 embedding，这里暂不改动——stdin 走的是 `process_contract_stdin()` 路径（第 374 行），不经过 `process_trace` 的 embedding 路径。

实际上 `process_stdin` 调用的 `process_job_line` 会传 `embedding_client=None`，而 `classify_work_relevance_with_embeddings` 内部已有 `embedding_client is None` 的回退。所以这里不需要改动。

- [ ] **Step 5: 修改 process_redis_once 签名和调用**

将 `process_redis_once`（第 217 行）签名追加 `embedding_client` 参数：

```python
def process_redis_once(
    redis_url: str,
    list_name: str,
    evidence_store: EvidenceStore,
    postgres_dsn: str,
    timeout_seconds: int,
    connection_factory=psycopg.connect,
    storage_backend: str = "filesystem",
    embedding_client=None,
) -> int:
```

将第 244 行的 `process_job_line` 调用追加 `embedding_client=embedding_client`：

```python
            result = process_job_line(
                payload,
                evidence_store,
                PostgresAnalysisRepository(connection),
                PostgresContextRepository(connection),
                storage_backend=storage_backend,
                embedding_client=embedding_client,
            )
```

- [ ] **Step 6: 修改 process_redis_loop 签名和调用**

将 `process_redis_loop`（第 277 行）签名追加 `embedding_client` 参数：

```python
def process_redis_loop(
    redis_url: str,
    list_name: str,
    evidence_store: EvidenceStore,
    postgres_dsn: str,
    timeout_seconds: int,
    storage_backend: str = "filesystem",
    embedding_client=None,
) -> int:
```

将第 316 行的 `process_job_line` 调用追加 `embedding_client=embedding_client`：

```python
                result = process_job_line(
                    payload,
                    evidence_store,
                    PostgresAnalysisRepository(connection),
                    PostgresContextRepository(connection),
                    storage_backend=storage_backend,
                    embedding_client=embedding_client,
                )
```

- [ ] **Step 7: 修改 main() 初始化 EmbeddingClient 并传递**

在 `main()` 函数中（第 377 行 `evidence_store = create_evidence_store()` 之后），追加初始化：

```python
    evidence_store = create_evidence_store()
    storage_backend = os.environ.get("EVIDENCE_STORAGE_BACKEND", "")

    embedding_client = EmbeddingClient("http://embedding:80")
    embedding_client.wait_until_ready()
```

将 `process_redis_once` 调用（第 383 行）追加 `embedding_client=embedding_client`：

```python
        return process_redis_once(
            args.redis_url,
            args.redis_list,
            evidence_store,
            args.postgres_dsn,
            args.redis_timeout_seconds,
            storage_backend=storage_backend,
            embedding_client=embedding_client,
        )
```

将 `process_redis_loop` 调用（第 391 行）追加 `embedding_client=embedding_client`：

```python
    return process_redis_loop(
        args.redis_url,
        args.redis_list,
        evidence_store,
        args.postgres_dsn,
        args.redis_timeout_seconds,
        storage_backend=storage_backend,
        embedding_client=embedding_client,
    )
```

- [ ] **Step 8: 运行现有测试确认不破坏**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: 全部 PASS

- [ ] **Step 9: 提交**

```bash
git add workers/analysis_worker/main.py
git commit -m "feat(worker): initialize EmbeddingClient and wire through call chain"
```

---

### Task 4: work_relevance.py — 移除 pg_connection 参数，embedding_client 必需化

**Files:**
- Modify: `workers/analysis_worker/work_relevance.py`

- [ ] **Step 1: 修改 classify_work_relevance_with_embeddings 签名和内部逻辑**

将 `work_relevance.py` 第 154-163 行从：

```python
def classify_work_relevance_with_embeddings(
    job,
    messages,
    contexts,
    embedding_client,
    pg_connection,
) -> WorkRelevanceAssessment:
    text = _combined_text(messages)
    if not text or embedding_client is None or pg_connection is None:
        return classify_work_relevance(job, messages, contexts)

    trace_embedding = embedding_client.embed(text)
```

改为：

```python
def classify_work_relevance_with_embeddings(
    job,
    messages,
    contexts,
    embedding_client,
) -> WorkRelevanceAssessment:
    text = _combined_text(messages)
    if not text:
        return classify_work_relevance(job, messages, contexts)

    trace_embedding = embedding_client.embed(text)
```

变更点：移除 `pg_connection` 参数，移除 `embedding_client is None` 的降级判断。

- [ ] **Step 2: 修改函数体中 pg_connection 的使用**

该函数后续代码中使用 `pg_connection` 做 pgvector 相似度查询。需要从函数内部获取连接，或者将 pg_connection 的使用改为通过其他方式获取。

阅读完整函数体后确认：pg_connection 用于 `cursor = pg_connection.cursor()` 执行 pgvector 向量相似度查询。这个连接应该由调用方传入。

**决策：保留 pg_connection 参数，但放在 embedding_client 之后，由 process_trace 传入数据库连接。**

撤回 Step 1 的改动，改为：

```python
def classify_work_relevance_with_embeddings(
    job,
    messages,
    contexts,
    embedding_client,
    pg_connection=None,
) -> WorkRelevanceAssessment:
    text = _combined_text(messages)
    if not text:
        return classify_work_relevance(job, messages, contexts)

    trace_embedding = embedding_client.embed(text)
```

变更点：移除 `embedding_client is None` 降级，保留 `pg_connection` 参数用于向量查询。

- [ ] **Step 3: 回到 main.py 的 process_trace 补传 pg_connection**

在 Task 3 Step 3 中，`process_trace` 调用 `classify_work_relevance_with_embeddings` 时需要传 pg_connection。但 `process_trace` 本身没有数据库连接——连接在各 Redis 处理函数中创建。

在 `process_job_line` 中没有直接的 pg_connection 可用。需要在 `process_redis_once` 和 `process_redis_loop` 中把已创建的 connection 向下传递。

**修改 process_job_line 签名追加 pg_connection 参数：**

```python
def process_job_line(line: str, evidence_store: EvidenceStore, repository, context_repository=None, storage_backend: str = "filesystem", embedding_client=None, pg_connection=None) -> dict:
```

**修改 process_job_line 中 process_trace 调用追加 pg_connection：**

```python
    return process_trace(job, request_body, response_body, repository, contexts, evidence_store, storage_backend=storage_backend, embedding_client=embedding_client, pg_connection=pg_connection)
```

**修改 process_trace 中 classify_work_relevance_with_embeddings 调用传 pg_connection：**

```python
    work_relevance = classify_work_relevance_with_embeddings(
        job, messages, list(contexts or []), embedding_client, pg_connection,
    )
```

**修改 process_redis_once 中 process_job_line 调用传 connection：**

```python
            result = process_job_line(
                payload,
                evidence_store,
                PostgresAnalysisRepository(connection),
                PostgresContextRepository(connection),
                storage_backend=storage_backend,
                embedding_client=embedding_client,
                pg_connection=connection,
            )
```

**修改 process_redis_loop 中 process_job_line 调用传 connection：**

```python
                result = process_job_line(
                    payload,
                    evidence_store,
                    PostgresAnalysisRepository(connection),
                    PostgresContextRepository(connection),
                    storage_backend=storage_backend,
                    embedding_client=embedding_client,
                    pg_connection=connection,
                )
```

- [ ] **Step 4: 运行测试**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add workers/analysis_worker/work_relevance.py workers/analysis_worker/main.py
git commit -m "refactor(worker): make embedding_client required, wire pg_connection through call chain"
```

---

### Task 5: 验证与清理

**Files:**
- No new files

- [ ] **Step 1: 运行全量 Python 测试**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: 全部 PASS

- [ ] **Step 2: 运行 Go 测试确认无影响**

Run: `cd /Users/roy/codes/new-api-gateway && make test`
Expected: 全部 PASS

- [ ] **Step 3: 验证 docker-compose 配置**

Run: `docker compose -f deploy/docker-compose.yml config --quiet`
Expected: 无报错

- [ ] **Step 4: 最终提交**

```bash
git add -A
git status
# 确认无遗漏文件
```
