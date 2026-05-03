# SSE 流式响应拼装设计

## 背景

当前网关对 SSE 流式响应的 evidence 存储是原始 SSE 文本（`event: ...\ndata: ...`），归一化器（Python worker）需要额外的 SSE 解析逻辑才能提取结构化数据。Python 侧已有 `_load_sse_json_events()` + `_response_json_from_sse_events()` 做了部分拼装，但只拼接文本内容，不还原完整结构。

同时，不同 SSE 格式的完成事件结构差异很大：
- **Responses API**：`response.completed` 事件包含完整的 response 对象，可直接提取
- **Chat Completions**：没有完成事件，需要从 `choices[].delta` 片段拼装
- **Claude Messages**：类似 Chat Completions，需要从 `content_block_delta` 片段拼装

## 目标

- 在 Go 网关层完成 SSE 流式响应的结构化 JSON 拼装
- 拼装后的 JSON 替换原始 SSE 文本存入 evidence，与归一化器透明对接
- 支持 OpenAI Chat Completions、OpenAI Responses、Claude Messages 三种格式
- 不支持拼装的格式（generic、images）继续存原始 SSE 文本

## 架构

### 方案选择

在现有 `usageExtractor` 接口上扩展，新增 `assembleSSE()` 方法。

**选择理由：** `processSSE` 已经在逐行解析 SSE 事件，累积 delta 的逻辑自然落在同一个 struct 里。复用已有的 extractor 注册机制，每个格式独立文件，跟现有架构一致。

### 接口变更

```go
type usageExtractor interface {
    processSSE(payload []byte)          // 已有
    sseResult() (minimalUsage, string)  // 已有
    extractResponse(body []byte) (minimalUsage, string) // 已有
    extractRequest(path string, body []byte) string      // 已有
    assembleSSE() []byte                // 新增：流结束后返回拼装的完整响应 JSON
}
```

- `assembleSSE()` 返回 `nil` 表示该格式不支持拼装
- 在流结束后（`[DONE]` 之后）调用一次

## 各格式的拼装逻辑

### OpenAI Responses（openai_responses）

`processSSE` 中识别 `type: "response.completed"` 事件，保存整个 `response` 对象。`assembleSSE()` 直接返回 `json.Marshal(savedResponse)`。

拼装结果与该格式的非流式响应结构完全一致。

### OpenAI Chat Completions（openai_chat）

在 `processSSE` 中累积：
- 第一个 `choices[].delta.role` → 最终 `message.role`
- 所有 `choices[].delta.content` 拼接 → 最终 `message.content`
- `choices[].delta.tool_calls` → 按 index 累积 `function.name` 和 `function.arguments` 片段
- 最后一个带 `usage` 的 chunk → 最终 usage
- `id`、`model` 从 chunk 中提取

`assembleSSE()` 拼装为：
```json
{
  "id": "chatcmpl-xxx",
  "model": "gpt-5.2",
  "choices": [{"index":0,"message":{"role":"assistant","content":"...","tool_calls":[...]},"finish_reason":"stop"}],
  "usage": {"prompt_tokens":..., "completion_tokens":..., "total_tokens":...}
}
```

### Claude Messages（claude_messages）

在 `processSSE` 中累积（扩展已有的累积逻辑）：
- `content_block_start` → 记录 content block 类型和 index
- `content_block_delta` → 累积 text delta
- `message_start` → 记录 id、model、input usage
- `message_delta` → 记录 stop_reason、output usage

`assembleSSE()` 拼装为：
```json
{
  "id": "msg_xxx",
  "model": "claude-sonnet-4-20250514",
  "content": [{"type":"text","text":"拼接后的完整内容"}],
  "stop_reason": "end_turn",
  "usage": {"input_tokens":..., "output_tokens":...}
}
```

### Generic / Images

`assembleSSE()` 返回 `nil`，evidence 继续存原始 SSE 文本。

## Evidence 存储变更

当前流式路径用 `io.Pipe` 边流边写 evidence。改为流结束后一次性写入：

1. 流式传输过程中，原始 SSE body 写入 `bytes.Buffer`
2. 流结束后，调用 `ext.assembleSSE()` 获取拼装 JSON
3. 非-nil 则 `Store.Put(response_body.bin, assembled)`；否则 `Store.Put(response_body.bin, buf.Bytes())`

内存影响：`bytes.Buffer` 将整个 response body 持有在内存中，与非流式路径（`io.ReadAll`）行为一致。

## 归一化器适配

无需改动。拼装后的 JSON 与非流式响应结构一致，Python worker 的 `_load_json_object()` 直接成功。现有的 `_response_json_from_sse_events()` 保留作为兜底处理不支持拼装的格式。

## 回归兼容

- 非 SSE 请求：不受影响
- 已有 evidence 中的旧 SSE 文件：归一化器旧逻辑仍可处理
- 不支持拼装的 SSE 格式：继续存原始 SSE，行为不变

## 错误处理

- `assembleSSE()` 返回 `nil` → 存原始 SSE，不报错
- `processSSE` 中 JSON 解析错误 → 静默忽略（与现有行为一致）
- 拼装结果为空（流中断、无完成事件）→ 返回 `nil`，回退到原始 SSE
- 内存占用不做额外限制，与非流式路径一致

## 文件变更清单

| 操作 | 文件 |
|---|---|
| 修改 | `internal/gateway/usage.go`（接口新增 `assembleSSE()` 方法） |
| 修改 | `internal/gateway/usage_openai_responses.go`（新增 `assembleSSE()` 实现） |
| 修改 | `internal/gateway/usage_openai_chat.go`（扩展 `processSSE` 累积 delta + `assembleSSE()` 实现） |
| 修改 | `internal/gateway/usage_claude.go`（扩展 `processSSE` 累积 delta + `assembleSSE()` 实现） |
| 修改 | `internal/gateway/usage_generic.go`（新增返回 `nil` 的 `assembleSSE()`） |
| 修改 | `internal/gateway/usage_openai_images.go`（新增返回 `nil` 的 `assembleSSE()`） |
| 修改 | `internal/gateway/usage_gemini.go`（新增返回 `nil` 的 `assembleSSE()`，Gemini SSE 拼装后续按需补充） |
| 修改 | `internal/gateway/stream.go`（流结束后调用 `assembleSSE()`，传回拼装结果） |
| 修改 | `internal/gateway/proxy.go`（流式路径 evidence 存储改为流结束后一次性写入） |
| 修改 | `internal/gateway/usage_openai_responses_test.go`（新增 assemble 测试） |
| 修改 | `internal/gateway/usage_openai_chat_test.go`（新增 assemble 测试） |
| 修改 | `internal/gateway/usage_claude_test.go`（新增 assemble 测试） |
| 修改 | `internal/gateway/stream_test.go`（集成测试验证 assembled JSON） |
| 修改 | `e2e/test_gateway_capture.py`（流式场景断言 evidence 为合法 JSON） |
