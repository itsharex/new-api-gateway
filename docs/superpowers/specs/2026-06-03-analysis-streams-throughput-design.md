# Analysis Streams Throughput Redesign

Date: 2026-06-03

## Goal

重构 analysis 服务的消费与执行模型，优先提升高峰期抗压能力和单条 trace 的完成时延。

本设计的核心目标是：

- 将当前串行 `BLPOP -> 全链路同步处理` 改为基于 Redis Streams 的批量并发消费。
- 将慢步骤从主分析路径中拆出，避免少数重任务拖慢普通 trace。
- 让 core 分析结果尽快可用，即使 enrichment 失败也不影响基础结果查询。
- 将核心队列与性能指标暴露到管理后台界面，而不仅是 `readyz` 或 Prometheus 文本。

## Non-Goals

- 不兼容现有 `analysis_jobs` Redis list 协议。
- 不保留历史 analysis 数据，也不提供旧数据回填方案。
- 不在本阶段引入 Kafka、SQS 或额外消息基础设施。
- 不在本阶段把 gateway 改造成完整 outbox publisher。
- 不在本阶段重构 admin UI 的整体视觉或导航结构，只新增与 analysis 运行状态相关的能力。

## Current Problem

当前 analysis worker 采用单进程串行模型：

```text
BLPOP one item
  -> read evidence
  -> normalize
  -> work relevance
  -> anomaly detection
  -> usage aggregate upsert
  -> optional media rewrite
  -> optional LLM judge
  -> commit
```

这会带来几个吞吐瓶颈：

- 单条慢任务会阻塞整个消费循环。
- Redis list 没有 ack / claim 语义，worker 崩溃时消息恢复能力差。
- `usage_aggregates` 热点 upsert 与历史窗口查询会放大数据库争用。
- 媒体提取与 LLM judge 这类慢步骤和 core 路径耦合，导致普通 trace 的 p95 时延被拉高。
- 现有监控只能粗略看到 queue depth 和 worker heartbeat，无法在后台界面查询核心运行指标。

## User-Confirmed Requirements

- 优先目标是 `吞吐量`，同时兼顾高峰抗压和单条处理时延。
- 允许调整 Redis 队列模型，不局限于现有 list。
- 允许在 analysis 侧做结构性重构，但尽量不引入新的基础设施组件。
- 慢阶段不应破坏前一阶段已经提交的核心结果。
- 不考虑历史数据兼容与保留问题。
- 核心队列与性能指标必须可以在管理后台界面查询。

## Approaches Considered

### 1. Streams 单阶段并发

将 Redis list 替换为 Streams + consumer group，并将 worker 改成批量拉取和本地并发执行，但仍保持“单阶段、整条任务一次完成”。

优点：

- 相比当前实现改动最小。
- 可立刻获得批量消费、ack 和 crash recovery 能力。
- 实现成本低于多阶段拆分。

缺点：

- 重任务仍会占住 worker 槽位。
- 普通 trace 的时延改善有限。
- LLM judge、媒体派生、热点聚合仍会污染主路径。

### 2. Streams 快慢双车道

按任务特征在入队前把请求分流到 `fast` 和 `heavy` 两条 stream，分别消费。

优点：

- 高峰时容易通过扩容 `fast` lane 保住大多数流量。

缺点：

- 分流依赖 gateway 侧的早期判断，误分流成本高。
- 很多慢特征只能在读取正文或完成 normalize 后才能准确识别。
- 会把队列决策复杂度前置到 gateway。

### 3. Recommended: Streams 两阶段 core / enrichment

所有 trace 先进入 `core` stream；只有 core 判定“需要慢增强”的任务才进入 `enrichment` stream。

优点：

- 普通 trace 可以尽快完成核心分析。
- 少量慢任务不会拖慢主路径。
- 更适合“高峰抗压 + 单条时延”双目标。
- 不要求 gateway 过早准确判断重任务。

缺点：

- 需要显式定义阶段边界、状态机和幂等策略。
- 需要新增运行态表与后台运行指标接口。

## Decision

采用方案 3：Redis Streams 两阶段模型。

本设计的关键决策如下：

- gateway 将 trace 投递到 `analysis.core` stream。
- core worker 负责快速生成基础事实与基础分析结果。
- enrichment worker 只处理慢增强逻辑，不得回写 core 已提交的基础事实。
- `usage_aggregates` 从 core 热路径中移除，改为由单独 rollup 过程异步生成。
- 管理后台新增 analysis runtime 页面与 API，支持查询实时队列状态和历史性能趋势。

## High-Level Architecture

```text
gateway
  -> insert trace summary
  -> XADD analysis.core

analysis core worker group
  -> claim / batch read
  -> load trace + evidence
  -> normalize
  -> core rules
  -> write core facts/results
  -> mark core complete
  -> optionally XADD analysis.enrichment
  -> XACK analysis.core

analysis enrichment worker group
  -> claim / batch read
  -> load trace + derived inputs
  -> llm judge / media derivation / slow enrichment
  -> append enrichment results
  -> mark enrichment complete
  -> XACK analysis.enrichment

rollup job
  -> read trace_usage_facts
  -> rebuild usage_aggregates / baseline_cache

admin runtime APIs
  -> live snapshot from Redis + analysis_tasks
  -> historical charts from analysis_runtime_samples
```

## Queue Topology

系统使用三条 Redis Streams：

- `analysis.core`
  - 所有 trace 的入口队列
  - consumer group: `analysis-core-workers`
- `analysis.enrichment`
  - 仅承载需要慢增强的 trace
  - consumer group: `analysis-enrichment-workers`
- `analysis.dlq`
  - 存放超过重试上限或不可恢复的任务
  - 不要求实时消费

每条 stream 都使用 consumer group、pending entries list 和 `XAUTOCLAIM` 机制。

## Message Contract

不再沿用当前肥大的 `TraceCapturedJob` 结构。新的 stream message 只承载调度所需最小字段：

- `trace_id`
- `stage`
- `enqueued_at`
- `attempt`
- `hints`

`hints` 只允许放极轻量且稳定的字段，例如：

- `protocol_family`
- `request_body_size_bucket`
- `response_body_size_bucket`
- `has_response_body`
- `capture_mode`

业务全量上下文由 worker 根据 `trace_id` 从数据库和 evidence store 回查。

## Stage Boundaries and Data Ownership

### Core 阶段拥有的数据

core 阶段负责写入不可变基础事实和快速可得结果：

- `traces` 摘要状态
- `normalized_messages`
- `trace_usage_facts`
- `analysis_results` 中 `stage = core` 的结果
- `usage_anomalies` 中仅依赖快速规则的记录
- `coverage_alerts`
- 轻量级派生任务排队，例如基于外链 URL 的 `media_snapshot_jobs`

### Enrichment 阶段拥有的数据

enrichment 阶段只允许追加慢增强结果：

- `analysis_results` 中 `stage = enrichment` 的结果
- `usage_anomalies` 中依赖 enrichment 结果的新告警
- `raw_evidence_objects` 中的派生对象
- `traces` 上的 enrichment 摘要状态

### Hard Rules

- 原始 evidence 必须不可变。
- enrichment 不得覆盖 core 已提交的 `normalized_messages`、`trace_usage_facts` 或 core 分析结果。
- 如果 LLM judge 与 heuristic 结论不同，必须并存保存，不能原地覆盖。
- 状态只能单向推进，不能从完成态回退到处理中。

## Data Model

### `traces`

保留 trace 级摘要信息，但将分析状态拆成双阶段字段：

- `core_status`: `pending | processing | completed | failed`
- `enrichment_required`: boolean
- `enrichment_status`: `not_required | pending | processing | completed | failed`
- `core_queued_at`
- `core_started_at`
- `core_completed_at`
- `enrichment_queued_at`
- `enrichment_started_at`
- `enrichment_completed_at`
- `last_analysis_error_code`

admin API 可以基于以上字段导出展示用的 `analysis_status`，例如：

- core 未完成时显示 `pending` 或 `processing`
- core 完成且 enrichment 不需要时显示 `completed`
- core 完成且 enrichment 处理中时显示 `enriching`
- core 完成且 enrichment 失败时显示 `completed_with_enrichment_failure`

### `analysis_tasks`

新增运行态表，每个 `(trace_id, stage)` 仅保留一条任务记录。

字段：

- `trace_id`
- `stage`: `core | enrichment`
- `status`: `queued | leased | succeeded | failed_retryable | failed_terminal`
- `attempt_count`
- `max_attempts`
- `lease_owner`
- `lease_expires_at`
- `stream_name`
- `stream_message_id`
- `queued_at`
- `started_at`
- `completed_at`
- `last_error_code`
- `last_error_message`
- `updated_at`

约束：

- unique `(trace_id, stage)`

### `analysis_results`

保留现有统一结果表，但补充明确的来源与阶段字段：

- `stage`: `core | enrichment`
- `producer`: 例如 `heuristic_work_relevance`, `llm_judge`, `usage_extraction`
- `result_key`: 例如 `work_relevance_primary`, `llm_judge_secondary`

唯一键：

- `(trace_id, stage, producer, result_key)`

### `trace_usage_facts`

新增单 trace 用量事实表，用于替代 core 热路径里的 `usage_aggregates` upsert。

字段至少包含：

- `trace_id`
- `token_fingerprint`
- `username`
- `model`
- `route_pattern`
- `protocol_family`
- `request_started_at`
- `request_count`
- `success_count`
- `error_count`
- `stream_count`
- `prompt_tokens`
- `completion_tokens`
- `cached_tokens`
- `total_tokens`
- `reasoning_tokens`
- `request_body_bytes`
- `response_body_bytes`

唯一键：

- `(trace_id)`

### `raw_evidence_objects`

继续承载 evidence 元数据，但需要区分原始对象和派生对象：

- `variant`: `original | sanitized | derived_media`
- `derived_from_object_ref`: nullable

这允许 enrichment 生成脱敏副本或媒体派生物，而不是改写原始 request body。

### `analysis_runtime_samples`

新增运行指标采样表，供 admin UI 查询趋势图。

字段：

- `sampled_at`
- `stage`
- `queue_depth`
- `pending_count`
- `leased_count`
- `oldest_pending_age_seconds`
- `throughput_per_minute`
- `queue_wait_p50_ms`
- `queue_wait_p95_ms`
- `processing_p50_ms`
- `processing_p95_ms`
- `retryable_fail_count`
- `terminal_fail_count`
- `active_consumers`

## Status Machine

### Task 状态机

`analysis_tasks.status` 采用：

- `queued`
- `leased`
- `succeeded`
- `failed_retryable`
- `failed_terminal`

允许的推进路径：

- `queued -> leased -> succeeded`
- `queued -> leased -> failed_retryable`
- `failed_retryable -> leased -> succeeded`
- `failed_retryable -> leased -> failed_terminal`

不允许从 `succeeded` 回退。

### Trace 摘要状态机

`traces.core_status`：

- `pending -> processing -> completed`
- `pending -> processing -> failed`

`traces.enrichment_status`：

- `not_required`
- `pending -> processing -> completed`
- `pending -> processing -> failed`

## Idempotency and Commit Model

worker 必须按“先租约、后写库、最后 ack”的顺序运行。

### Core / Enrichment 通用流程

1. 读取 stream message
2. 尝试将 `analysis_tasks.status` 从 `queued` 或 `failed_retryable` 置为 `leased`
3. 只有成功拿到租约的 worker 才继续处理
4. 在数据库事务内写入该阶段的结果
5. 将 task 标记为 `succeeded`
6. 更新 `traces` 摘要状态
7. 事务提交成功后执行 `XACK`

如果 worker 在第 4-6 步之间崩溃：

- Redis pending entry 仍保留
- 其他 worker 可在租约超时后通过 `XAUTOCLAIM` 接管
- 幂等唯一键保证重复执行不会产生重复结果或重复累计

## Worker Concurrency Model

### Core Worker

core worker 使用：

- `XREADGROUP COUNT N BLOCK T`
- 本地固定并发池
- 长生命周期 PostgreSQL 连接池

推荐第一版暴露以下配置：

- `ANALYSIS_CORE_READ_COUNT`
- `ANALYSIS_CORE_MAX_INFLIGHT`
- `ANALYSIS_CORE_LEASE_SECONDS`
- `ANALYSIS_CORE_RETRY_LIMIT`

目标是让大多数普通 trace 在 core 阶段快速完成，不因少数慢任务排队。

### Enrichment Worker

enrichment worker 与 core worker 独立部署、独立调参：

- `ANALYSIS_ENRICHMENT_READ_COUNT`
- `ANALYSIS_ENRICHMENT_MAX_INFLIGHT`
- `ANALYSIS_ENRICHMENT_LEASE_SECONDS`
- `ANALYSIS_ENRICHMENT_RETRY_LIMIT`
- `ANALYSIS_ENRICHMENT_LLM_MAX_CONCURRENCY`

其并发通常显著低于 core。

## Error Handling

### `retryable`

示例：

- Redis 短暂抖动
- PostgreSQL 瞬时失败
- evidence store 超时
- LLM judge timeout

处理：

- task 标记为 `failed_retryable`
- 增加 `attempt_count`
- 重新入队原 stage stream
- 使用指数退避

### `failed_terminal`

示例：

- trace 缺关键字段
- evidence 引用非法
- JSON 或协议格式明确不可解析

处理：

- task 标记为 `failed_terminal`
- 写入错误码与错误信息
- 投递到 `analysis.dlq`
- 不再自动重试

### `degraded_success`

仅适用于 enrichment：

- core 已成功完成
- enrichment 失败或超时

处理：

- `core_status = completed`
- `enrichment_status = failed`
- 不回滚 core 结果

## Aggregate and Baseline Derivation

为了提高 core 热路径吞吐，`usage_aggregates` 与 `baseline_cache` 不再由每条 trace 在线同步更新。

改为：

- core 只写 `trace_usage_facts`
- 独立 rollup 过程按固定频率重建或增量更新：
  - `usage_aggregates`
  - `baseline_cache`

第一版推荐每分钟执行一次 rollup，优先保证：

- core worker 不触碰热点聚合行
- core worker 不执行历史窗口统计查询
- baseline 刷新延迟小于 1 分钟即可接受

## Admin API and UI Runtime Metrics

### API Design

新增以下 admin API：

- `GET /admin/api/analysis-runtime`
  - 返回当前实时快照
- `GET /admin/api/analysis-runtime/history?stage=core&range=1h`
  - 返回历史趋势点
- `GET /admin/api/analysis-runtime/consumers?stage=core`
  - 返回 consumer 维度状态

### Live Snapshot Sources

实时快照由后端聚合：

- Redis Streams:
  - `XLEN`
  - `XPENDING`
  - `XINFO GROUPS`
  - `XINFO CONSUMERS`
- PostgreSQL:
  - `analysis_tasks`
  - `traces`

前端不直接读取 `/readyz` 或 `/metrics`。

### UI Design

沿用现有 admin UI 骨架：

- 在 `overview` 页面新增“分析运行”摘要卡片
- 新增独立视图 `analysis-runtime`

#### Overview 必显卡片

- core queue depth
- core oldest pending age
- core leased count
- core throughput
- core queue wait p95
- core processing p95
- enrichment backlog

#### Runtime 页面结构

- stage 筛选：`core | enrichment`
- range 筛选：`15m | 1h | 24h`
- 顶部 KPI 卡片
- 趋势图 1：`queue depth` 与 `oldest pending age`
- 趋势图 2：`queue wait p95` 与 `processing p95`
- consumer 表：
  - `worker_id`
  - `last_seen_at`
  - `leased_count`
  - `idle_seconds`
  - `last_error_code`

## Observability

### Runtime KPIs

必须采集并展示：

- `queue_depth`
- `oldest_pending_age_seconds`
- `leased_count`
- `success_rate`
- `retryable_fail_rate`
- `terminal_fail_rate`
- `queue_wait_p50_ms`
- `queue_wait_p95_ms`
- `processing_p50_ms`
- `processing_p95_ms`
- `throughput_per_minute`
- `active_consumers`
- `llm_judge_timeout_rate`

### Readiness Semantics

- core 阶段 degraded 时，analysis readiness 应降级
- enrichment 阶段 degraded 时，只应标记增强能力异常，不应把整个系统判死

## Testing Strategy

### Unit Tests

- 状态机推进
- 租约获取与过期接管
- 幂等写入
- 错误分类
- derived evidence 只追加不覆盖

### Integration Tests

- `XREADGROUP` + `XACK`
- `XAUTOCLAIM` 接管超时任务
- 同一消息重复执行不产生重复结果
- core 成功后投递 enrichment

### Load Tests

- 大量普通 trace + 少量慢任务混跑
- 验证 core backlog、queue wait p95 和 processing p95
- 验证 enrichment 积压不显著影响 core 完成时延

### Fault Injection

- worker 在写库前后崩溃
- PostgreSQL 短暂超时
- Redis 重连
- LLM judge 长时间超时

## Rollout

本设计不提供兼容旧模型的迁移路径，采用干净切换：

1. 停止 gateway 与旧 worker
2. 清空旧 Redis `analysis_jobs` 与相关旧 stream
3. 重建 analysis 相关 schema
4. 部署支持 `XADD analysis.core` 的新 gateway
5. 启动 core worker
6. 验证后台 runtime 页面与关键指标
7. 启动 enrichment worker
8. 启动 rollup 过程

## Implementation Phases

### Phase 1: Foundations

- 建新 schema
- 定义 `analysis_tasks`
- 引入双阶段 trace 摘要状态
- 将原始 evidence 改为不可变模型

### Phase 2: Core Streams

- gateway 切到 `analysis.core`
- core worker 改成 Streams + batch + concurrency
- core 只写基础事实和基础结果

### Phase 3: Enrichment Streams

- 拆出 LLM judge、媒体派生等慢路径
- 引入 `analysis.enrichment`
- 完成 UI 的 enrichment 指标展示

### Phase 4: Rollup and Tuning

- 将聚合与 baseline 迁出 core 热路径
- 增加 runtime sample 采集
- 调整并发、批量大小、租约时间和 retry 限制

## Summary

本设计用 Streams 两阶段消费替代当前串行 Redis list worker，并通过“core 快速提交、enrichment 慢速追加、aggregate 异步派生”的边界划分，把吞吐、时延、恢复能力和后台可观测性放到同一个一致模型里。

这允许系统在不引入新基础设施的前提下，优先优化：

- 高峰期不被少数慢任务拖垮
- 普通 trace 更快可见核心分析结果
- worker 崩溃后可恢复消费
- 管理后台直接查询核心队列和性能指标
