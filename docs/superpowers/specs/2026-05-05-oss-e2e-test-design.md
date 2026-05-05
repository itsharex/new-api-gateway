# OSS 证据存储 E2E 测试设计

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增独立的 OSS e2e 测试，验证 Gateway + Worker 全链路使用 OSS 后端时证据存储的正确性。

**Architecture:** 通过 shell 脚本控制 Gateway 生命周期（停旧启新，设 `EVIDENCE_STORAGE_BACKEND=oss`），两个独立 Python e2e 文件分别验证基本管线和媒体提取。通过 `oss2` SDK 直接从 OSS 回读对象验证数据完整性。

**Tech Stack:** Bash, Python 3.11+, oss2, psycopg, redis, subprocess

---

## 新增文件

### 1. `scripts/e2e_oss_pipeline.sh`

测试入口脚本。职责：

1. 检查必需的 OSS 环境变量（`OSS_ENDPOINT`, `OSS_BUCKET`, `OSS_ACCESS_KEY_ID`, `OSS_ACCESS_KEY_SECRET`），缺一则报错退出
2. 检查 Gateway 进程是否存在，存在则停止
3. 在新环境中后台启动 Gateway：
   ```
   EVIDENCE_STORAGE_BACKEND=oss \
   OSS_ENDPOINT=$OSS_ENDPOINT \
   OSS_BUCKET=$OSS_BUCKET \
   OSS_ACCESS_KEY_ID=$OSS_ACCESS_KEY_ID \
   OSS_ACCESS_KEY_SECRET=$OSS_ACCESS_KEY_SECRET \
   go run ./cmd/audit-gateway &
   ```
4. 等待 Gateway 就绪（轮询 `/healthz`）
5. 运行 Python e2e 测试：
   ```
   uv run e2e/test_gateway_worker_pipeline_oss.py
   uv run e2e/test_media_extraction_oss.py
   ```
6. 清理：停止 Gateway 进程（trap EXIT 确保异常退出也会清理）

不修改现有 Makefile 或 docker-compose.yml。

### 2. `e2e/test_gateway_worker_pipeline_oss.py`

基本管线测试。与 `test_gateway_worker_pipeline.py` 对称，核心差异：

**Worker 环境变量：**
```python
env = {
    **os.environ,
    "EVIDENCE_STORAGE_BACKEND": "oss",
    "OSS_ENDPOINT": os.environ["OSS_ENDPOINT"],
    "OSS_BUCKET": os.environ["OSS_BUCKET"],
    "OSS_ACCESS_KEY_ID": os.environ["OSS_ACCESS_KEY_ID"],
    "OSS_ACCESS_KEY_SECRET": os.environ["OSS_ACCESS_KEY_SECRET"],
    "POSTGRES_DSN": PG_DSN,
    "REDIS_URL": REDIS_URL,
}
```

**新增断言：**

- `assert_oss_evidence_refs` — 查询 `raw_evidence_objects`，验证 `object_ref` 以 `oss://<bucket>/` 开头，`storage_backend = 'oss'`
- `assert_oss_object_readable` — 用 `oss2` SDK 根据 `object_ref` 回读对象内容，验证非空且长度与 `size_bytes` 一致

**前置检查：** 文件开头检查所有 OSS 环境变量，缺一则 `bail("Set OSS_ENDPOINT, OSS_BUCKET, OSS_ACCESS_KEY_ID, OSS_ACCESS_KEY_SECRET to run OSS e2e")`

### 3. `e2e/test_media_extraction_oss.py`

媒体提取 + OSS 存储。与 `test_media_extraction.py` 对称，核心差异：

**Worker 环境变量**同上（设 `EVIDENCE_STORAGE_BACKEND=oss` + OSS 凭证）。

**新增/替换断言：**

- `assert_oss_media_assets` — 查询 `raw_evidence_objects` 中 `media_asset_*` 行的 `object_ref` 以 `oss://` 开头
- `assert_oss_media_content` — 用 `oss2` SDK 回读 `media_asset_000001` 对象，验证二进制内容与 `SMALL_PNG` 一致
- 替换原有的 `assert_evidence_rewritten` 中的文件系统读取逻辑：request_body 证据也在 OSS 上，通过 SDK 回读 JSON 内容验证 `audit-media:` 引用替换

## 不修改的文件

- `e2e/helpers.py` — OSS 测试需要的 oss2 逻辑内联在新文件中，不污染共享 helper
- `e2e/test_gateway_worker_pipeline.py` — filesystem 测试不动
- `e2e/test_media_extraction.py` — filesystem 测试不动
- `e2e/test_gateway_openai.py` / `test_gateway_claude.py` — 纯网关测试，不涉及 evidence 后端

## OSS 对象路径

测试产生的 OSS 对象路径格式遵循现有约定：`raw/{year}/{month}/{day}/{trace_id}/{object_type}.bin`。因为是真实请求的 trace_id，路径不可预测，测试通过 DB 查询获取实际 `object_ref` 再回读。

## 运行方式

```bash
# 需要先设置 OSS 凭证
export OSS_ENDPOINT=oss-cn-hangzhou.aliyuncs.com
export OSS_BUCKET=my-audit-evidence
export OSS_ACCESS_KEY_ID=...
export OSS_ACCESS_KEY_SECRET=...

# 运行完整 OSS e2e（自动管理 Gateway 生命周期）
./scripts/e2e_oss_pipeline.sh
```

## e2e 依赖

`e2e/pyproject.toml` 需新增 `oss2` 依赖。
