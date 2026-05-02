# E2E Gateway Capture Test 设计

## 目标

验证网关代理的完整链路：真实 API 请求 → 网关转发 → 数据库落库 → 身份解析 → 证据存储。

## 测试脚本

`e2e/test_gateway_capture.py`，Python，依赖 `requests` + `psycopg[binary]`（psycopg3）。

后续 e2e 测试统一放在 `e2e/` 目录下。

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `AUDIT_GATEWAY_URL` | `http://localhost:8080` | 网关地址 |
| `NEW_API_BASE_URL` | `http://localhost:3000` | 上游直连地址 |
| `NEW_API_KEY` | — | API key（必填，无默认值） |
| `POSTGRES_DSN` | `postgres://audit:audit@localhost:5432/audit_gateway?sslmode=disable` | audit DB |
| `TEST_MODEL` | `gpt-5.2` | 测试用模型 |

## 3 阶段流程

### 阶段 1 — 前置校验

直接向 `NEW_API_BASE_URL` 发一个 chat completion 请求，确认 key 有效、模型可用、上游可达。失败则立即退出并打印错误。

### 阶段 2 — 网关请求

对两个端点各发 2 轮对话（共 4 个请求，4 条 trace）。

#### `/v1/chat/completions`

第 1 轮：
```json
{"model":"gpt-5.2","messages":[{"role":"user","content":"hello"}],"max_tokens":10}
```

第 2 轮（携带第 1 轮的 assistant 回复）：
```json
{"model":"gpt-5.2","messages":[
  {"role":"user","content":"hello"},
  {"role":"assistant","content":"<第1轮回复>"},
  {"role":"user","content":"what is 1+1?"}
],"max_tokens":10}
```

#### `/v1/responses`

第 1 轮：
```json
{"model":"gpt-5.2","input":"hello","max_output_tokens":10}
```

第 2 轮（引用第 1 轮返回的 response id）：
```json
{"model":"gpt-5.2","previous_response_id":"<第1轮resp_id>","input":"what is 1+1?","max_output_tokens":10}
```

每个请求记录响应的 `x-audit-trace-id` header，用于后续 DB 断言。

### 阶段 3 — 数据库断言

对每条 trace（共 4 条）查询 `traces` 表，逐字段验证：

| 字段 | 期望 |
|---|---|
| `trace_id` | 与响应 header `x-audit-trace-id` 一致 |
| `identity_resolution_status` | `resolved` |
| `username_snapshot` | `dave.zhao` |
| `token_fingerprint` | 非空 |
| `fingerprint_display` | `tkfp_` 开头 |
| `protocol_family` | `/v1/chat/completions` → `openai_chat`；`/v1/responses` → `openai_responses` |
| `capture_mode` | `raw_and_normalized` |
| `status_code` | 200 |
| `request_body_size` | > 0 |
| `response_body_size` | > 0 |
| `request_raw_ref` | 非空 |
| `response_raw_ref` | 非空 |
| `model_requested` | `gpt-5.2` |

额外检查：
- `raw_evidence_objects` 表：每条 trace 至少有 `request_body` 和 `response_body` 两条记录
- `token_identity_cache` 表：存在对应 `token_fingerprint` 的缓存记录，且 `username` = `dave.zhao`

## 失败输出

断言失败时打印：端点名、轮次、字段名、期望值 vs 实际值。脚本以非零退出码退出。

## 前提条件

- postgres、redis、new-api 上游、audit-gateway 进程均已启动
- migrations 已执行
- API key `sk-G0YzOkt9WQAwp8S9DL9mLKlcFNEYRjdnA4x6PMrNRgZA05l8` 在 new-api 中有效且关联用户 `dave.zhao`
