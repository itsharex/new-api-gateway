# Trace 消息 CAS（Content-Addressed Storage）设计

## 背景与问题

当前架构下，每次 LLM 调用产出一个 trace。worker 把请求/响应里的每条消息解析为 `normalized_messages` 表的一行。在多轮聊天场景下：

- 第 1 轮 trace 写入 1 行 normalized_messages
- 第 2 轮 trace 写入 3 行（含第 1 轮的 user 消息 + 第 1 轮的 assistant 响应 + 本轮 user 消息）
- 第 N 轮 trace 写入 2N-1 行

会话长度为 N 时，normalized_messages 表的行数和 content_text 存储量为 **O(N²)**。绝大部分 content_text 在多轮 trace 间完全重复，但每次都被重新写入。

参考代码：
- `workers/analysis_worker/repository.py:242-272` — 唯一的 normalized_messages 写入点
- `internal/admin/repository.go:1219-1247` — 唯一的生产读路径（admin trace 详情页）
- `migrations/0003_analysis_normalization_usage.sql:1-23` — 当前表结构

## 目标

引入 content-addressed 消息存储：相同 `(role, modality, content_text)` 三元组的消息只在 `messages` 表里存一次，trace 通过 `trace_messages` 关联表引用。将会话长度 N 的 content_text 存储从 O(N²) 降到 O(N)。

## 非目标

- **不**对 evidence 文件层（filesystem/OSS 的 request_body.bin）做去重。该层受 byte-perfect 约束限制，多轮聊天内部增长无法 dedup。这部分作为独立的后续 spec 处理。
- **不**改写异常检测/分析 worker 的消费逻辑。所有现存查询通过兼容视图透明访问新表。
- **不**做历史数据迁移。当前所有 normalized_messages 数据删除，新表只对新 trace 生效。
- **不**改 Go 网关代码。本设计仅影响 worker + DB schema。

## 关键设计决定

| 决定 | 选择 | 理由 |
|---|---|---|
| dedup key | `sha256(role + '\0' + modality + '\0' + content_text)` | 同文本不同角色/模态仍是不同消息；位置/元数据（trace_id、sequence_index、source_path、metadata_json）不进 key |
| 历史数据 | 删除，不 backfill | 历史数据可丢弃；避免 backfill 复杂度 |
| 旧表处理 | 直接 DROP，建立同名视图 | 视图对 admin 读路径完全透明 |
| `messages.occurrence_count` 字段 | 加入 | 监控 dedup 命中率、加速 `repeated_prompt` 异常规则、调试用 |

## Schema 变更

### 新表 `messages`（canonical 消息存储）

```sql
CREATE TABLE messages (
    message_id           BIGSERIAL PRIMARY KEY,
    message_key          TEXT NOT NULL UNIQUE,
    role                 TEXT NOT NULL,
    modality             TEXT NOT NULL DEFAULT 'text',
    content_text         TEXT NOT NULL,
    content_text_hash    TEXT NOT NULL,
    token_count_estimate INTEGER NOT NULL DEFAULT 0,
    first_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    first_trace_id       TEXT NOT NULL,
    occurrence_count     BIGINT NOT NULL DEFAULT 1
);

CREATE INDEX idx_messages_content_hash ON messages(content_text_hash);
CREATE INDEX idx_messages_role_modality ON messages(role, modality);
```

- `message_key` = `sha256(role || '\x00' || modality || '\x00' || content_text)`，由 worker 在写入前计算（沿用现有 `text_hash` 模式，扩展为三元组）
- `content_text_hash` 保留 `sha256(content_text)` 单值，用于 diagnostics 和向后兼容字段名
- `occurrence_count`：每次新 trace 引用此消息时 +1，是 dedup 命中率监控和 `repeated_prompt` 规则的关键索引

### 新表 `trace_messages`（位置关联）

```sql
CREATE TABLE trace_messages (
    trace_id           TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    message_id         BIGINT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    direction          TEXT NOT NULL,
    sequence_index     INTEGER NOT NULL,
    source_path        TEXT NOT NULL DEFAULT '',
    protocol_item_type TEXT NOT NULL DEFAULT '',
    media_url          TEXT NOT NULL DEFAULT '',
    media_object_id    BIGINT,
    metadata_json      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (trace_id, direction, sequence_index, source_path)
);

CREATE INDEX idx_trace_messages_message ON trace_messages(message_id);
CREATE INDEX idx_trace_messages_trace ON trace_messages(trace_id);
```

- 主键沿用旧 `normalized_messages` 的位置约束（`trace_id, direction, sequence_index, source_path`），保证同一 trace 重处理时幂等
- `media_url`、`media_object_id`、`metadata_json` 都是位置/调用相关的字段，留在关联表，**不**进 messages
- `metadata_json` 包含 tool_call_id、function name 等协议特定元数据；同一 canonical 文本在不同 trace 里可能有不同 metadata（例如不同 tool_call_id）

### 兼容视图 `normalized_messages`

```sql
CREATE VIEW normalized_messages AS
SELECT
    tm.trace_id,
    tm.direction,
    tm.sequence_index,
    m.role,
    m.modality,
    m.content_text,
    m.content_text_hash,
    tm.media_url,
    tm.source_path,
    tm.protocol_item_type,
    m.token_count_estimate,
    tm.metadata_json,
    tm.created_at
FROM trace_messages tm
JOIN messages m ON m.message_id = tm.message_id;
```

视图列名与旧表完全一致，`internal/admin/repository.go:1219` 的查询语句无需改动。

## Worker 代码变更

### `workers/analysis_worker/models.py`

- `NormalizedMessage` 数据类新增 `message_key: str` 字段（frozen dataclass 仍可加字段，因为所有构造点都从 worker 内部发起）
- 新增工具函数：

```python
def message_key(role: str, modality: str, content_text: str) -> str:
    payload = f"{role}\x00{modality}\x00{content_text}".encode("utf-8")
    return sha256(payload).hexdigest()
```

- 所有 `NormalizedMessage(...)` 构造点（normalizers.py 内 `_message` / `_part_messages` 等工厂函数）补充 `message_key` 字段计算

### `workers/analysis_worker/repository.py:242-272`

把当前的单条 INSERT 改为两步写入：

```python
for message in messages:
    cursor.execute("""
        INSERT INTO messages (
            message_key, role, modality, content_text,
            content_text_hash, token_count_estimate, first_trace_id
        )
        VALUES (%s,%s,%s,%s,%s,%s,%s)
        ON CONFLICT (message_key)
        DO UPDATE SET occurrence_count = messages.occurrence_count + 1
        RETURNING message_id
    """, (
        message.message_key,
        message.role,
        message.modality,
        message.content_text,
        message.content_text_hash,
        message.token_count_estimate,
        message.trace_id,
    ))
    message_id = cursor.fetchone()[0]

    cursor.execute("""
        INSERT INTO trace_messages (
            trace_id, message_id, direction, sequence_index,
            source_path, protocol_item_type, media_url,
            media_object_id, metadata_json
        )
        VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s::jsonb)
        ON CONFLICT (trace_id, direction, sequence_index, source_path)
        DO NOTHING
    """, (
        message.trace_id,
        message_id,
        message.direction,
        message.sequence_index,
        message.source_path,
        message.protocol_item_type,
        message.media_url,
        message.media_object_id,
        json.dumps(message.metadata, sort_keys=True),
    ))
```

关键语义：
- `messages` 表 INSERT 命中 `ON CONFLICT` 时**只递增 occurrence_count**，不覆盖 content。canonical 消息是不可变的。
- `trace_messages` 表 INSERT 命中主键冲突时 `DO NOTHING`。同一 trace 重处理（重试/重放）不会重复挂关联。
- 两条 SQL 在同一事务内执行（调用方已开启事务）。

**First-writer-wins 语义**：`token_count_estimate`、`modality` 等字段在 `messages` 表里首次写入后不再更新。理论上同一 `(role, modality, content_text)` 三元组在 worker 不同版本下应产出相同字段值；如果未来 token_count 估计算法升级，老消息的 estimate 保持首次写入时的值（不会回填）。这是可接受的：`token_count_estimate` 只是估算，不参与 trace 级别聚合（聚合走 usage_aggregates，来自 LLM 上游返回的真实 token 数）。

### `workers/analysis_worker/tests/test_repository.py`

旧测试通过 `INSERT INTO normalized_messages` 构造 fixture，需要改为 `INSERT INTO messages` + `INSERT INTO trace_messages`，或调用新的 repository 函数。

## Migration 计划

新增 `migrations/0018_message_cas.sql`：

```sql
-- 0018: 消息级 content-addressed storage
-- 旧 normalized_messages 数据全部丢弃；新表只对新 trace 生效

DROP TABLE IF EXISTS normalized_messages CASCADE;

CREATE TABLE messages (
    message_id           BIGSERIAL PRIMARY KEY,
    message_key          TEXT NOT NULL UNIQUE,
    role                 TEXT NOT NULL,
    modality             TEXT NOT NULL DEFAULT 'text',
    content_text         TEXT NOT NULL,
    content_text_hash    TEXT NOT NULL,
    token_count_estimate INTEGER NOT NULL DEFAULT 0,
    first_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    first_trace_id       TEXT NOT NULL,
    occurrence_count     BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX idx_messages_content_hash ON messages(content_text_hash);
CREATE INDEX idx_messages_role_modality ON messages(role, modality);

CREATE TABLE trace_messages (
    trace_id           TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    message_id         BIGINT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    direction          TEXT NOT NULL,
    sequence_index     INTEGER NOT NULL,
    source_path        TEXT NOT NULL DEFAULT '',
    protocol_item_type TEXT NOT NULL DEFAULT '',
    media_url          TEXT NOT NULL DEFAULT '',
    media_object_id    BIGINT,
    metadata_json      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (trace_id, direction, sequence_index, source_path)
);
CREATE INDEX idx_trace_messages_message ON trace_messages(message_id);
CREATE INDEX idx_trace_messages_trace ON trace_messages(trace_id);

CREATE VIEW normalized_messages AS
SELECT
    tm.trace_id,
    tm.direction,
    tm.sequence_index,
    m.role,
    m.modality,
    m.content_text,
    m.content_text_hash,
    tm.media_url,
    tm.source_path,
    tm.protocol_item_type,
    m.token_count_estimate,
    tm.metadata_json,
    tm.created_at
FROM trace_messages tm
JOIN messages m ON m.message_id = tm.message_id;
```

### 部署顺序

用户明确历史数据可丢弃，部署顺序简单：

1. **停服窗口**：暂停 worker（gateway 可继续接受请求，但 trace 累积在 Redis Streams 里）
2. **跑 migration**：DROP + CREATE TABLE + CREATE VIEW
3. **部署新 worker 二进制**
4. **恢复 worker**

`raw_evidence_objects.analysis_results` 等其他表通过 `trace_id` 外键关联 traces，DROP `normalized_messages CASCADE` 不影响它们（normalized_messages 没有被其他表 FK 引用，CASCADE 是冗余保险）。

## 测试影响

| 测试类别 | 影响 | 处理 |
|---|---|---|
| `workers/analysis_worker/tests/test_repository.py` | 直接 INSERT normalized_messages 构造 fixture 失败 | 改为新双表 INSERT 或调用新 repository 函数 |
| `workers/analysis_worker/tests/test_normalizers.py` | 不直接触 DB，验证 NormalizedMessage 字段 | 加 `message_key` 字段断言 |
| `workers/analysis_worker/tests/test_pipeline.py` | 通过完整 pipeline 写入，验证落库 | 视图透明，可能不需改动；需运行确认 |
| `e2e/helpers.py:assert_trace_fields` 等读 normalized_messages | 视图透明 | 无需改动 |
| `internal/admin/repository_test.go` | mock DB 查询，不触真表 | 无需改动 |
| `internal/admin/handlers_test.go` | 同上 | 无需改动 |

## 验证

### 单元测试（worker）

1. 写入 2 条相同 `(role, modality, content_text)` 的消息（来自不同 trace_id）：
   - `messages` 表只有 1 行
   - `occurrence_count = 2`
   - `trace_messages` 表有 2 行
2. 写入相同 `(trace_id, direction, sequence_index, source_path)` 的消息（模拟重试）：
   - 不重复插入，`occurrence_count` 不增长
3. 同 `content_text` 但不同 `role`：
   - `messages` 表 2 行（不合并）

### 集成测试（worker + DB）

复用 `tests/test_pipeline.py` 的 process_trace pipeline，验证：
- 视图 `normalized_messages` 行数与旧表语义一致
- admin 读路径（listNormalizedMessages）返回正确顺序的消息

### E2E

复用 `e2e/run_all.py`：
- 跑一次 chat 流量（多轮），验证 trace 详情页消息显示完整
- 跑两次完全相同请求（模拟 retry），验证 `messages.occurrence_count` 增长

### 监控

部署后第一周观察：

```sql
-- dedup 命中率
SELECT
  COUNT(*) FILTER (WHERE occurrence_count = 1) AS unique_msgs,
  COUNT(*) FILTER (WHERE occurrence_count > 1) AS deduped_msgs,
  SUM(occurrence_count) AS total_references,
  ROUND(
    100.0 * (SUM(occurrence_count) - COUNT(*)) / NULLIF(SUM(occurrence_count), 0),
    2
  ) AS dedup_savings_pct
FROM messages;
```

## 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| worker 重试导致同一 trace 重复写 | trace_messages 主键冲突 | `ON CONFLICT ... DO NOTHING` |
| 并发 worker 处理同一 trace | 同上 | 主键 + DO NOTHING 处理 |
| message_key 计算与 SQL 不一致 | dedup 失效 | worker 单点计算，不在 SQL 层重算 |
| 视图性能（join 开销） | admin 详情页慢 | 单 trace 查询，hit 索引；如真慢可换物化视图或直接读 |
| tool_call_id 等位置元数据丢失语义 | 跨 trace 元数据混淆 | metadata_json 留在 trace_messages，不进 key |

## 后续工作（不在本 spec 范围）

1. **evidence 文件级 CAS**：新增 `evidence_blobs` 表，gateway 的 `putEvidence` 按 sha256 查表复用 `object_ref`。catch retry/replay/相同请求场景。多轮聊天内部增长仍无法 dedup（byte-perfect 约束）。
2. **`repeated_prompt` 异常规则改写**：从全表扫 `normalized_messages.content_text_hash` 改为 `SELECT message_id FROM messages WHERE occurrence_count >= N AND role='user'`。性能大幅提升。
3. **trace 保留策略**：未来引入 N 天保留期时，GC 利用 `occurrence_count` 快速判断 canonical 消息是否可随唯一引用 trace 一起删除。
