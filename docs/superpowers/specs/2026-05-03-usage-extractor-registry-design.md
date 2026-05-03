# Usage Extractor Registry 设计

## 背景

当前 `stream.go` 的 `sseUsageExtractor.parsePayload()` 和 `minimal.go` 的 `extractResponseUsage()` 使用单一巨型函数 + fallback 链处理所有 API 格式（OpenAI Chat、OpenAI Responses、Anthropic）。每新增一种格式都要往同一个 struct 加字段、往同一串 `if xxx == 0` 链加分支，容易互相干扰。

路由注册中已有 `ProtocolFamily` 字段（`openai_chat`、`openai_responses`、`claude_messages`、`gemini` 等），且贯穿整个请求生命周期，可直接作为分发 key。

## 目标

- 按请求路径（`ProtocolFamily`）分发到格式专属的 usage 提取器
- 每种格式独立文件，独立 struct，不共享 JSON tag
- 新增格式只需：加一个文件 + 注册一行
- 覆盖 OpenAI Chat、OpenAI Responses、OpenAI Images、Anthropic、Gemini 五种格式，其余走 generic
- 同步补充 e2e 测试覆盖各格式的 usage 提取

## 支持的 API 格式

| Extractor | ProtocolFamily | SSE usage 位置 | 非流式 usage 位置 | 请求 model 来源 |
|---|---|---|---|---|
| openaiChat | `openai_chat`, `openai_completions` | 顶层 `usage.prompt_tokens` 等 | 同左 | `body.model` |
| openaiResponses | `openai_responses` | `response.usage.input_tokens` 等 | 同左 | `body.model` |
| openaiImages | `openai_images` | 顶层 `usage.input_tokens` 等（`details` 含 `image_tokens`+`text_tokens`） | 同左 | `body.model` |
| claudeMessages | `claude_messages` | `message.usage.input_tokens`（message_start）+ `usage.output_tokens`（message_delta） | 顶层 `usage.input_tokens` 等（含 `cache_read_input_tokens`） | `body.model` |
| gemini | `gemini` | `usageMetadata.promptTokenCount` 等 | 同左 | URL 路径（`/v1/models/{model}:generateContent`） |
| generic | 其他所有 | 不解析 | 尝试顶层 `usage`，取不到返回零值 | `body.model` |

## 架构

### 新增文件

```
internal/gateway/
  usage.go                    # 接口定义 + 注册表 + extractorFor() + minimalUsage 定义
  usage_openai_chat.go        # openaiChatExtractor + 测试
  usage_openai_responses.go   # openaiResponsesExtractor + 测试
  usage_openai_images.go      # openaiImagesExtractor + 测试
  usage_claude.go             # claudeMessagesExtractor + 测试
  usage_gemini.go             # geminiExtractor + 测试
  usage_generic.go             # genericExtractor + 测试
```

### 接口定义

```go
type usageExtractor interface {
    extractSSE(payload []byte) (minimalUsage, string)  // SSE data 行 → (usage, model)
    extractResponse(body []byte) (minimalUsage, string) // 完整响应体 → (usage, model)
    extractRequest(path string, body []byte) string      // 请求路径+体 → model
}
```

### 注册表

```go
var extractors = map[string]usageExtractor{}

func registerExtractor(families []string, ext usageExtractor) {
    for _, f := range families {
        extractors[f] = ext
    }
}

func extractorFor(family string) usageExtractor {
    if ext, ok := extractors[family]; ok {
        return ext
    }
    return extractors["_generic"]
}
```

各 extractor 文件在 `init()` 中调用 `registerExtractor`。

## 与现有代码集成

### stream.go 改造

`sseUsageExtractor` 新增 `ext usageExtractor` 字段：

```go
type sseUsageExtractor struct {
    w   io.Writer
    ext usageExtractor
    // usage, model, buf, prefix 不变
}
```

`parsePayload()` 改为：
```go
func (e *sseUsageExtractor) parsePayload(payload []byte) {
    u, m := e.ext.extractSSE(payload)
    if u.TotalTokens > 0 {
        e.usage = u
    }
    if m != "" {
        e.model = m
    }
}
```

`Write()`、行缓冲逻辑、透传逻辑不变。

构造时传入 extractor：
```go
func newSSEUsageExtractor(w io.Writer, ext usageExtractor) *sseUsageExtractor { ... }
```

### minimal.go 改造

- 删除 `extractResponseUsage()` 和 `extractResponseModel()`
- 保留 `modelFromEngineEmbeddingPath()`（路径相关，与格式无关）
- `minimalUsage` 定义移到 `usage.go`

### proxy.go 改动点

**流式路径**（`serveStreamingResponse`）：
```go
// before:
usageExtractor := newSSEUsageExtractor(clientWriter)
// after:
usageExtractor := newSSEUsageExtractor(clientWriter, extractorFor(record.entry.ProtocolFamily))
```

**非流式路径**：
```go
// before:
u := extractResponseUsage(responseBody)
m := extractResponseModel(responseBody)
// after:
ext := extractorFor(record.entry.ProtocolFamily)
u, m := ext.extractResponse(responseBody)
```

**请求 model 提取**：
```go
// before:
record.modelRequested = extractRequestModel(req.URL.Path, requestBody)
// after:
ext := extractorFor(record.entry.ProtocolFamily)
record.modelRequested = ext.extractRequest(req.URL.Path, requestBody)
```

### 不动的部分

- `stream.go`：`copyStreamToClientAndCapture`、`teeStream`、`flushWriter`、`isStreamingResponse`
- `routes/registry.go`：路由注册不变
- `routes/` 整个包不变

## 错误处理

- extractor 解析方法内部静默忽略 JSON 解析错误（SSE 中不是每条 data 都是 usage 事件）
- `extractorFor()` 找不到匹配 family 时返回 `genericExtractor`
- `ProtocolFamily` 为空或 `"unknown"` 也走 `genericExtractor`

## E2E 测试补充

现有 `e2e/test_gateway_capture.py` 仅覆盖 `chat/completions` 和 `responses` 的非流式请求，且**未验证 usage token 数**。

### 新增验证项

在 `TRACE_FIELDS` 中新增查询字段：`usage_total_tokens, usage_prompt_tokens, usage_completion_tokens, model_upstream`。

在 `assert_traces` 中为每种格式增加断言：
- `usage_total_tokens > 0`：所有文本类格式必须提取到 token 数
- `usage_prompt_tokens > 0`：prompt tokens 非零
- `usage_completion_tokens > 0`：completion tokens 非零（图片类格式可为 0）
- `model_upstream` 非空：上游返回的 model 应被提取

### 新增测试场景

在 `test_gateway_capture.py` 中新增发送函数：

1. **`send_chat_completions_stream()`** — `stream: true`，验证流式 chat 的 usage 提取
2. **`send_responses_stream()`** — `stream: true`，验证流式 Responses API 的 usage 提取
3. **`send_anthropic_messages()`** — `/v1/messages`，验证 Anthropic 格式（需要上游支持 Anthropic 代理）
4. **`send_images_generations()`** — `/v1/images/generations`，验证图片生成 API 的 usage 提取（如果模型支持）

每个场景验证：
- 响应状态码 200
- `x-audit-trace-id` header 存在
- 数据库中 trace 记录的 `usage_total_tokens > 0`
- 数据库中 trace 记录的 `model_upstream` 非空

对于 Anthropic/Gemini 场景，如果上游不支持对应的 API 格式，测试应标记为 skip 而非 fail。

### 流式场景的特殊处理

流式请求不读取完整响应体，只验证最终 trace 记录中的 usage。测试流程：
1. 发送 `stream: true` 请求，读取 SSE 流直到 `[DONE]`
2. 从 response header 获取 `x-audit-trace-id`
3. 等待 trace 入库
4. 查询数据库断言 usage

## 迁移策略

一次性切换，不做渐进迁移。新代码通过所有现有单元测试 + 新增单元测试 + e2e 测试后替换旧代码。

## 文件变更清单

| 操作 | 文件 |
|---|---|
| 新增 | `internal/gateway/usage.go` |
| 新增 | `internal/gateway/usage_openai_chat.go` |
| 新增 | `internal/gateway/usage_openai_responses.go` |
| 新增 | `internal/gateway/usage_openai_images.go` |
| 新增 | `internal/gateway/usage_claude.go` |
| 新增 | `internal/gateway/usage_gemini.go` |
| 新增 | `internal/gateway/usage_generic.go` |
| 新增 | `internal/gateway/usage_openai_chat_test.go` |
| 新增 | `internal/gateway/usage_openai_responses_test.go` |
| 新增 | `internal/gateway/usage_openai_images_test.go` |
| 新增 | `internal/gateway/usage_claude_test.go` |
| 新增 | `internal/gateway/usage_gemini_test.go` |
| 新增 | `internal/gateway/usage_generic_test.go` |
| 新增 | `internal/gateway/usage_test.go`（注册表分发测试） |
| 修改 | `internal/gateway/stream.go`（parsePayload 委托 + 构造函数加参数） |
| 修改 | `internal/gateway/minimal.go`（删除两个导出函数，保留 modelFromEngineEmbeddingPath） |
| 修改 | `internal/gateway/proxy.go`（构造 extractor 时传入 ProtocolFamily） |
| 修改 | `internal/gateway/stream_test.go`（适配新构造函数签名） |
| 修改 | `internal/gateway/minimal_test.go`（迁移到对应 extractor 测试） |
| 修改 | `e2e/test_gateway_capture.py`（新增 usage 断言 + 流式场景 + 新格式场景） |
