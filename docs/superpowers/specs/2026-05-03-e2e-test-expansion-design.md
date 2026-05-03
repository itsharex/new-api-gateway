# E2E 测试扩展设计

## 背景

现有 e2e 测试只覆盖 OpenAI Chat/Responses 的 4 种场景（非流式 + SSE 流式），缺少 Claude、Gemini 等协议家族的全链路验证，也没有覆盖"网关采集 → Worker 分析"的完整闭环。

## 目标

- 覆盖 Claude `/v1/messages` 和 Gemini `/v1beta/models/*` 的非流式与 SSE 流式场景
- 验证网关采集的 trace 能被 Worker 正确消费并产出分析结果
- 保持现有脚本的独立运行风格，不引入 pytest 框架依赖

## 文件结构

```
e2e/
  helpers.py                        # 共享基础设施（新增）
  test_gateway_capture.py           # OpenAI Chat/Responses（现有，不变）
  test_gateway_claude.py            # Claude /v1/messages（新增）
  test_gateway_gemini.py            # Gemini /v1beta/models/*（新增）
  test_gateway_worker_pipeline.py   # Worker 分析闭环（新增）
```

## helpers.py 内容

从现有 `test_gateway_capture.py` 提取以下内容：

- **配置常量**：`GATEWAY_URL`、`UPSTREAM_URL`、`API_KEY`、`PG_DSN`、`MODEL`、`EXPECTED_USERNAME`，均从环境变量读取
- **HTTP session**：禁用代理的 `requests.Session()`
- **断言工具**：`check()`、`eq()`、`not_empty()`、`starts_with()`、`gt()`、`bail()`
- **`preflight(endpoint, body)`**：向 upstream 发一个最小请求验证可达性
- **`wait_for_traces(conn, trace_ids, timeout=10)`**：轮询等待 trace 写入
- **`assert_trace_fields(conn, trace_id, ctx, protocol_family, ...)`**：通用 trace 字段断言
- **`assert_evidence_objects(conn, trace_id, ctx)`**：验证 request_body + response_body
- **`assert_identity_cache(conn, fingerprint, ctx)`**：验证 token_identity_cache 条目
- **`report_results(all_results, errors)`**：最终 PASSED/FAILED 输出 + sys.exit

## test_gateway_claude.py

### 前置条件

- 网关、new-api、Postgres、Redis 均运行中
- migrations 已应用
- `NEW_API_KEY` 设置为 dave.zhao 的有效 key
- new-api 支持路由 Claude 请求（即 upstream 能转发到 Claude API）

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `CLAUDE_MODEL` | `claude-sonnet-4-20250514` | 使用的 Claude 模型 |
| `AUDIT_GATEWAY_URL` | `http://localhost:8080` | 网关地址 |
| `NEW_API_BASE_URL` | `http://localhost:3000` | 上游地址 |
| `POSTGRES_DSN` | `postgres://audit:audit@localhost:5432/audit_gateway?sslmode=disable` | 数据库连接 |

### 测试场景

| # | 场景 | 流式 | 说明 |
|---|------|------|------|
| 1 | 单轮非流式 | No | `{"model":"...","messages":[{"role":"user","content":"hello"}],"max_tokens":10}` |
| 2 | 多轮非流式 | No | 带 assistant 历史消息的多轮对话 |
| 3 | 单轮 SSE 流式 | Yes | `"stream":true`，消费完整 SSE 流 |

### DB 断言

每个 trace 验证：
- `protocol_family = "claude_messages"`
- `capture_mode = "raw_and_normalized"`
- `status_code = 200`
- `identity_resolution_status = "resolved"`
- `username_snapshot = "dave.zhao"`
- `token_fingerprint` 非空，`fingerprint_display` 以 `tkfp_` 开头
- `model_requested` 非空
- `usage_total_tokens > 0`、`usage_prompt_tokens > 0`
- `request_body_size > 0`、`response_body_size > 0`
- evidence 有 `request_body` + `response_body`
- token_identity_cache 有对应条目

## test_gateway_gemini.py

### 前置条件

同 Claude 测试，但要求 new-api 支持路由 Gemini 请求。

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `GEMINI_MODEL` | `gemini-2.0-flash` | 使用的 Gemini 模型 |
| 其余 | 同 Claude 测试 | — |

### 测试场景

| # | 场景 | 端点 | 流式 | 说明 |
|---|------|------|------|------|
| 1 | 非流式 | `/v1beta/models/{model}:generateContent` | No | 标准 Gemini 请求 |
| 2 | SSE 流式 | `/v1beta/models/{model}:streamGenerateContent` | Yes | 流式生成 |
| 3 | v1 路径变体 | `/v1/models/{model}:generateContent` | No | 验证 v1 路径也能正确采集 |

请求体格式：
```json
{"contents": [{"role": "user", "parts": [{"text": "hello"}]}]}
```

### DB 断言

与 Claude 测试类似，差异点：
- `protocol_family = "gemini"`
- `model_upstream` 可能为空（Gemini 响应不返回 model），不做强制断言
- 其余字段断言与 Claude 一致

## test_gateway_worker_pipeline.py

### 前置条件

- 网关、new-api、Postgres、Redis 均运行中
- Python worker 可用（`workers/analysis_worker/`）
- migrations 已应用

### 测试流程

1. 通过网关发送一个 OpenAI Chat 非流式请求
2. 等待 trace 写入 Postgres（轮询，最多 10s）
3. 从 Postgres 读取 trace_id
4. 向 Redis `analysis_jobs` 队列推送 `trace_captured` 任务（JSON：`{"type":"trace_captured","trace_id":"..."}`)
5. 运行 Worker：`cd workers/analysis_worker && uv run python -m analysis_worker --redis-once`
6. 断言 Worker stdout JSON 包含 `worker_status = "processed"`
7. 断言 `analysis_results` 表有对应 trace_id 的记录

### 断言内容

- trace 在网关侧写入正确（复用 helpers.assert_trace_fields）
- Worker 处理成功（`worker_status = "processed"`）
- `analysis_results` 表有记录，`trace_id` 匹配
- 归一化字段存在（`protocol_family`、`model_requested`、`usage_total_tokens` 等）

### 注意事项

- 此脚本不覆盖异常检测和覆盖告警的专项逻辑（已有 `e2e_worker_anomaly_coverage.sh` 和 `e2e_worker_work_relevance.sh` 覆盖）
- 只验证"trace → Worker 消费 → analysis_results 写入"这条主路径

## 不做的事情

- **不重构现有 `test_gateway_capture.py`**：保持原样，避免一次性大改的风险。helpers.py 是新脚本使用，现有脚本不动。
- **不引入 pytest 框架**：保持 `python xxx.py` 直接运行的风格。
- **不覆盖 WebSocket realtime**：需要特殊的基础设施支持，复杂度高，暂不在本次范围。
- **不覆盖错误与边界场景**（4xx/5xx、超时等）：属于单独的测试设计范畴。
- **不覆盖 multipart 上传**：与媒体文件处理相关，复杂度高，暂不在本次范围。

## 运行方式

```bash
# Claude 测试
CLAUDE_MODEL=claude-sonnet-4-20250514 uv run e2e/test_gateway_claude.py

# Gemini 测试
GEMINI_MODEL=gemini-2.0-flash uv run e2e/test_gateway_gemini.py

# Worker 闭环测试
uv run e2e/test_gateway_worker_pipeline.py

# 全部 e2e
uv run e2e/test_gateway_capture.py && \
uv run e2e/test_gateway_claude.py && \
uv run e2e/test_gateway_gemini.py && \
uv run e2e/test_gateway_worker_pipeline.py
```
