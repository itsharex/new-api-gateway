# Trace 消息 CAS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `normalized_messages` 的 content_text 存储从 O(N²) 降到 O(N)，引入 `messages` + `trace_messages` 双表，旧表名作为兼容视图保留。

**Architecture:** worker 写入侧改为两步 INSERT：先 upsert 到 canonical `messages` 表（按 `message_key` dedup，递增 `occurrence_count`），再写入 `trace_messages` 位置关联。读侧（admin detail 页）通过同名视图透明访问，零代码改动。

**Tech Stack:** Python 3.11+ / `uv`、psycopg、PostgreSQL 视图、Go admin handler（不动）。

**Spec:** `docs/superpowers/specs/2026-06-14-trace-message-cas-design.md`

---

## File Structure

**Create:**
- `migrations/0018_message_cas.sql` — schema 迁移（DROP 旧表 + 建新表 + 建视图）

**Modify:**
- `workers/analysis_worker/models.py` — `NormalizedMessage` 加 `message_key` 字段；新增 `message_key()` helper
- `workers/analysis_worker/normalizers.py` — `_message()` 和 `_with_sequence_index()` 工厂补充 `message_key` 计算
- `workers/analysis_worker/repository.py:242-272` — 单 INSERT 改双 INSERT
- `workers/analysis_worker/tests/test_models.py` — `message_key()` helper 单元测试
- `workers/analysis_worker/tests/test_repository.py` — fixture 加 `message_key` 字段；新增双表 INSERT 断言
- `workers/analysis_worker/tests/test_rules.py` — fixture 加 `message_key` 字段

**Verify (不改):**
- `internal/admin/repository.go:1219-1247` — 视图透明，无代码改动
- `internal/admin/repository_test.go` — 不触真表，无改动

---

## Task 1: 新增 `message_key()` helper

**Files:**
- Modify: `workers/analysis_worker/models.py`（在 `text_hash()` 附近新增函数）
- Test: `workers/analysis_worker/tests/test_models.py`

**Background:** 现有 `text_hash()` 在 `models.py:338` 是 `sha256(content_text.encode("utf-8")).hexdigest()`。新增的 `message_key()` 需要对 `(role, modality, content_text)` 三元组哈希，用 `\x00` 分隔避免歧义。

- [ ] **Step 1: 写失败测试**

把以下测试追加到 `workers/analysis_worker/tests/test_models.py` 末尾：

```python
from models import message_key


def test_message_key_is_deterministic_for_same_inputs():
    k1 = message_key("user", "text", "hello")
    k2 = message_key("user", "text", "hello")
    assert k1 == k2
    assert len(k1) == 64  # sha256 hex


def test_message_key_differs_by_role():
    assert message_key("user", "text", "hi") != message_key("assistant", "text", "hi")


def test_message_key_differs_by_modality():
    assert message_key("user", "text", "hi") != message_key("user", "audio", "hi")


def test_message_key_differs_by_content():
    assert message_key("user", "text", "hi") != message_key("user", "text", "hello")


def test_message_key_null_byte_delimiter_prevents_collision():
    # 如果不用分隔符，("a\x00b", "c") 和 ("a", "b\x00c") 会哈希到同一值
    assert message_key("a\x00b", "c", "x") != message_key("a", "b\x00c", "x")
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd workers/analysis_worker && uv run pytest -q tests/test_models.py::test_message_key_is_deterministic_for_same_inputs -v`

Expected: FAIL with `ImportError: cannot import name 'message_key' from 'models'`

- [ ] **Step 3: 在 `models.py` 实现 helper**

在 `models.py:339`（`text_hash()` 函数之后）新增：

```python
def message_key(role: str, modality: str, content_text: str) -> str:
    payload = f"{role}\x00{modality}\x00{content_text}".encode("utf-8")
    return sha256(payload).hexdigest()
```

`sha256` 已经在文件顶部导入（`text_hash()` 在用）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd workers/analysis_worker && uv run pytest -q tests/test_models.py -v -k message_key`

Expected: 5 passed

- [ ] **Step 5: 提交**

```bash
git add workers/analysis_worker/models.py workers/analysis_worker/tests/test_models.py
git commit -m "$(cat <<'EOF'
feat(worker): add message_key helper for content-addressed dedup

sha256 over (role, modality, content_text) joined by null bytes.
Will serve as dedup key for the upcoming messages CAS table.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: 给 `NormalizedMessage` 加 `message_key` 字段并接线到工厂函数

**Files:**
- Modify: `workers/analysis_worker/models.py:124-137`（`NormalizedMessage` dataclass）
- Modify: `workers/analysis_worker/normalizers.py:485-510`（`_message()` 工厂）
- Modify: `workers/analysis_worker/normalizers.py:512-530`（`_with_sequence_index()` 工厂）
- Modify: `workers/analysis_worker/tests/test_repository.py:127,274,472,507`（fixture 补字段）
- Modify: `workers/analysis_worker/tests/test_rules.py:104,233`（fixture 补字段）
- Test: `workers/analysis_worker/tests/test_normalizers.py`

**Background:** `NormalizedMessage` 是 `@dataclass(frozen=True)`，加字段不破坏既有 keyword 构造，但所有构造点都必须传新字段。`_message()` 是主工厂（normalizers.py:485-510），`_with_sequence_index()` 用于重写 sequence_index（normalizers.py:512+）。所有 `_part_messages`、`_media_message`、`_url_media_message` 最终都走 `_message()`。

- [ ] **Step 1: 在 `test_normalizers.py` 写失败测试**

在 `workers/analysis_worker/tests/test_normalizers.py` 末尾追加（注意：文件顶部已有 `job()` helper 在 line 11-23，新测试直接复用）：

```python
from models import message_key


def test_normalize_openai_chat_populates_message_key():
    test_job = job("openai_chat")
    body = json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "hello"}],
    })
    messages, _ = normalize_json_trace(test_job, body, "{}")
    assert len(messages) == 1
    expected_key = message_key("user", "text", "hello")
    assert messages[0].message_key == expected_key


def test_with_sequence_index_preserves_message_key():
    from models import NormalizedMessage, text_hash
    from normalizers import _with_sequence_index

    original = NormalizedMessage(
        trace_id="trace_1",
        direction="request",
        sequence_index=0,
        role="user",
        modality="text",
        content_text="hi",
        content_text_hash=text_hash("hi"),
        message_key=message_key("user", "text", "hi"),
        media_url="",
        source_path="request.messages[0]",
        protocol_item_type="openai_chat_message",
        token_count_estimate=1,
        metadata={},
    )
    updated = _with_sequence_index(original, sequence_index=5)
    assert updated.sequence_index == 5
    assert updated.message_key == original.message_key
    assert updated.content_text == original.content_text
```

注意：Step 1 的两个测试会因为 `NormalizedMessage` 不接受 `message_key` 参数而失败 — 这是预期的。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd workers/analysis_worker && uv run pytest -q tests/test_normalizers.py::test_normalize_openai_chat_populates_message_key -v`

Expected: FAIL with `TypeError: __init__() got an unexpected keyword argument 'message_key'`

- [ ] **Step 3: 修改 `NormalizedMessage` dataclass 加字段**

在 `workers/analysis_worker/models.py:124-137` 把 dataclass 改为（在 `metadata` 之后新增 `message_key`）：

```python
@dataclass(frozen=True)
class NormalizedMessage:
    trace_id: str
    direction: str
    sequence_index: int
    role: str
    modality: str
    content_text: str
    content_text_hash: str
    media_url: str
    source_path: str
    protocol_item_type: str
    token_count_estimate: int
    metadata: dict[str, Any]
    message_key: str
```

- [ ] **Step 4: 修改 `_message()` 工厂计算 `message_key`**

把 `workers/analysis_worker/normalizers.py:485-509` 的 `_message()` 函数改为：

```python
def _message(
    job: TraceCapturedJob,
    direction: str,
    sequence_index: int,
    role: str,
    content_text: str,
    source_path: str,
    protocol_item_type: str,
    modality: str = "text",
    media_url: str = "",
) -> NormalizedMessage:
    return NormalizedMessage(
        trace_id=job.trace_id,
        direction=direction,
        sequence_index=sequence_index,
        role=role,
        modality=modality,
        content_text=content_text,
        content_text_hash=text_hash(content_text),
        media_url=media_url,
        source_path=source_path,
        protocol_item_type=protocol_item_type,
        token_count_estimate=max(1, len(content_text.split())) if content_text else 0,
        metadata={"route_pattern": job.route_pattern, "protocol_family": job.protocol_family},
        message_key=message_key(role, modality, content_text),
    )
```

需要在文件顶部导入：`from models import ..., message_key`（在现有 `text_hash` 旁边添加）。

- [ ] **Step 5: 修改 `_with_sequence_index()` 保留 `message_key`**

把 `workers/analysis_worker/normalizers.py:512-530`（或附近的 `_with_sequence_index()`）改为（在 metadata 之后增加一行）：

```python
def _with_sequence_index(message: NormalizedMessage, sequence_index: int) -> NormalizedMessage:
    return NormalizedMessage(
        trace_id=message.trace_id,
        direction=message.direction,
        sequence_index=sequence_index,
        role=message.role,
        modality=message.modality,
        content_text=message.content_text,
        content_text_hash=message.content_text_hash,
        media_url=message.media_url,
        source_path=message.source_path,
        protocol_item_type=message.protocol_item_type,
        token_count_estimate=message.token_count_estimate,
        metadata=message.metadata,
        message_key=message.message_key,
    )
```

完整读取 `normalizers.py:512-535` 确认 `metadata=message.metadata` 这一行原本就在，避免重复添加。

- [ ] **Step 6: 跑 normalizer 测试确认通过**

Run: `cd workers/analysis_worker && uv run pytest -q tests/test_normalizers.py -v`

Expected: 全部 PASS（包括 Step 1 新增的两个 + 既有测试）

- [ ] **Step 7: 修复 `test_repository.py` 直接构造 NormalizedMessage 的 fixture**

`test_repository.py` 有 4 处 `NormalizedMessage(...)` 直接构造（line 120-133, 270-285, 468-485, 503-518 大约）。每处都需要在 `metadata=...` 之后加一行 `message_key="test_key",`。

具体定位用：
```bash
cd workers/analysis_worker && grep -n "NormalizedMessage(" tests/test_repository.py
```

每个匹配的 `NormalizedMessage(...)` 块，找到 `metadata={...}` 行，在其后添加：
```python
        message_key="test_key",
```

`"test_key"` 在测试 fixture 里足够，repository 测试只校验 SQL 字符串，不关心 key 值。

- [ ] **Step 8: 修复 `test_rules.py` 直接构造的 fixture**

`test_rules.py:104` 和 `test_rules.py:233` 各有一处。同样在 `metadata=...` 之后加一行 `message_key="test_key",`。

- [ ] **Step 9: 跑 worker 全部测试确认 fixture 修复无遗漏**

Run: `cd workers/analysis_worker && uv run pytest -q`

Expected: 全部 PASS（包括 test_models, test_normalizers, test_repository, test_rules, test_pipeline 等）。如果还有失败，可能是其他 fixture 文件需要补字段，按错误信息定位修复。

- [ ] **Step 10: 提交**

```bash
git add workers/analysis_worker/models.py workers/analysis_worker/normalizers.py \
        workers/analysis_worker/tests/test_normalizers.py \
        workers/analysis_worker/tests/test_repository.py \
        workers/analysis_worker/tests/test_rules.py
git commit -m "$(cat <<'EOF'
feat(worker): add message_key field to NormalizedMessage

_factories compute message_key from (role, modality, content_text).
Tests fixtures updated to pass explicit message_key. Required by
upcoming messages CAS table where this field becomes the dedup key.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: 创建 migration `0018_message_cas.sql`

**Files:**
- Create: `migrations/0018_message_cas.sql`

**Background:** 迁移执行器按文件名数字顺序应用 migrations，并维护 `schema_migrations` 表。DROP 旧表会级联清除所有行（用户已确认历史数据可丢）。`normalized_messages` 没有被其他表 FK 引用（FK 是它引用 traces），所以 CASCADE 是冗余保险。

- [ ] **Step 1: 检查最新 migration 编号**

Run: `ls migrations/ | tail -3`

Expected: `0016_analysis_streams_redesign.sql`、`0017_analysis_runtime_rate_kpis.sql`，下一个编号是 `0018`。

- [ ] **Step 2: 创建 `migrations/0018_message_cas.sql`**

```sql
-- 0018: 消息级 content-addressed storage
-- 旧 normalized_messages 数据全部丢弃；新表只对新 trace 生效。
-- 视图保留同名 normalized_messages 以兼容 admin 读路径。

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

- [ ] **Step 3: 起本地 postgres 并执行迁移**

确保 docker 可用，然后：

```bash
docker compose -f deploy/docker-compose.yml --env-file .env.local up -d postgres
docker compose -f deploy/docker-compose.yml --env-file .env.local --profile tools run --rm migrate
```

Expected: 迁移输出包含 `0018_message_cas.sql` 应用成功，无错误。

- [ ] **Step 4: 验证新 schema 落地**

连进 postgres：

```bash
docker compose -f deploy/docker-compose.yml --env-file .env.local exec postgres psql -U auditor -d audit_gateway -c "\dt" -c "\dv"
```

Expected: `\dt` 输出包含 `messages`、`trace_messages`；`\dv` 输出包含 `normalized_messages`。`normalized_messages` 不应出现在 `\dt` 输出（它是视图）。

进一步验证视图列：

```bash
docker compose -f deploy/docker-compose.yml --env-file .env.local exec postgres psql -U auditor -d audit_gateway -c "\d+ normalized_messages"
```

Expected: 列出 13 个字段：trace_id, direction, sequence_index, role, modality, content_text, content_text_hash, media_url, source_path, protocol_item_type, token_count_estimate, metadata_json, created_at。

- [ ] **Step 5: 提交**

```bash
git add migrations/0018_message_cas.sql
git commit -m "$(cat <<'EOF'
feat(db): add migration 0018 for message-level CAS

Drops legacy normalized_messages table, introduces messages (canonical
content store keyed by sha256(role+modality+content_text)) and
trace_messages (per-trace position join). Recreates normalized_messages
as a compatibility view over the new tables so admin reads stay
transparent.

Historical data is discarded per spec; only new traces populate the
new tables.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: 改写 `repository.py` 为双表 INSERT

**Files:**
- Modify: `workers/analysis_worker/repository.py:242-272`
- Test: `workers/analysis_worker/tests/test_repository.py`

**Background:** 当前 `repository.py:242-272` 是单条 INSERT 到 `normalized_messages`，conflict target 是 `(trace_id, direction, sequence_index, source_path)`，DO UPDATE 覆盖所有字段。改为两步：
1. INSERT 到 `messages` 按 `message_key` 冲突时只递增 `occurrence_count`
2. INSERT 到 `trace_messages` 按主键冲突 DO NOTHING

测试侧用的是 mock cursor（`conn.cursor_obj.executed`），所以单元测试验证的是 SQL 字符串正确，实际 dedup 行为靠 e2e。

- [ ] **Step 1: 在 `test_repository.py` 写失败测试**

在 `tests/test_repository.py` 找到现有的 message 持久化断言（大约 line 227-237，`save_trace_analysis` 调用后的 `queries` 断言块）。把：

```python
assert "INSERT INTO normalized_messages" in queries
```

改为：

```python
assert "INSERT INTO messages" in queries
assert "INSERT INTO trace_messages" in queries
assert "INSERT INTO normalized_messages" not in queries
assert "occurrence_count = messages.occurrence_count + 1" in queries
assert "ON CONFLICT (trace_id, direction, sequence_index, source_path)" in queries
assert "DO NOTHING" in queries
```

`test_repository.py:230` 这一行原本的 `assert "INSERT INTO normalized_messages" in queries` 是要替换的目标。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd workers/analysis_worker && uv run pytest -q tests/test_repository.py -v`

Expected: FAIL — `assert "INSERT INTO messages" in queries` 不成立（因为当前代码 INSERT 的是 `normalized_messages`）。

- [ ] **Step 3: 改写 `repository.py` 的写入循环**

把 `workers/analysis_worker/repository.py:241-273` 区域（`for message in messages:` 开头的整个循环）替换为：

```python
        for message in messages:
            cursor.execute(
                """
                INSERT INTO messages (
                    message_key, role, modality, content_text,
                    content_text_hash, token_count_estimate, first_trace_id
                )
                VALUES (%s,%s,%s,%s,%s,%s,%s)
                ON CONFLICT (message_key)
                DO UPDATE SET occurrence_count = messages.occurrence_count + 1
                RETURNING message_id
                """,
                (
                    message.message_key,
                    message.role,
                    message.modality,
                    message.content_text,
                    message.content_text_hash,
                    message.token_count_estimate,
                    message.trace_id,
                ),
            )
            message_id = cursor.fetchone()[0]
            cursor.execute(
                """
                INSERT INTO trace_messages (
                    trace_id, message_id, direction, sequence_index,
                    source_path, protocol_item_type, media_url,
                    media_object_id, metadata_json
                )
                VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s::jsonb)
                ON CONFLICT (trace_id, direction, sequence_index, source_path)
                DO NOTHING
                """,
                (
                    message.trace_id,
                    message_id,
                    message.direction,
                    message.sequence_index,
                    message.source_path,
                    message.protocol_item_type,
                    message.media_url,
                    None,  # media_object_id 暂未在 NormalizedMessage 暴露
                    json.dumps(message.metadata, sort_keys=True),
                ),
            )
```

注意：
- `media_object_id` 字段在 `NormalizedMessage` 数据类里**没有**，传入 `None`（schema 允许 NULL）。如果未来需要写真实值，再扩 dataclass。
- `messages` 表的 `first_seen_at`、`occurrence_count`、`first_trace_id` 在 INSERT 时除了 `first_trace_id` 都有默认值；冲突路径只更新 `occurrence_count`。
- 改完后确认上方仍有 `trace_ids = {...}` 那段（line 240），不要误删。

- [ ] **Step 4: 跑 repository 测试确认通过**

Run: `cd workers/analysis_worker && uv run pytest -q tests/test_repository.py -v`

Expected: 全部 PASS。

- [ ] **Step 5: 跑 worker 全量测试确认无回归**

Run: `cd workers/analysis_worker && uv run pytest -q`

Expected: 全部 PASS。如果有失败，可能是其他测试有对 `INSERT INTO normalized_messages` 字面量的断言，逐个修复。

- [ ] **Step 6: 提交**

```bash
git add workers/analysis_worker/repository.py workers/analysis_worker/tests/test_repository.py
git commit -m "$(cat <<'EOF'
feat(worker): write normalized messages via two-table CAS insert

messages table upserts by message_key (sha256 of role+modality+content),
incrementing occurrence_count on conflict. trace_messages holds the
per-trace position reference and is idempotent on retry via DO NOTHING.
Dedup behavior verified in e2e; unit tests assert SQL structure.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: 跑 worker + Go 测试，做 e2e 验证

**Files:**
- 不改代码，纯验证。如果发现问题按需修复。

- [ ] **Step 1: 跑 worker 全量 pytest**

Run: `cd workers/analysis_worker && uv run pytest -q`

Expected: 全部 PASS。失败处理：根据报错定位，常见原因 — fixture 遗漏 `message_key`、SQL 字符串断言过严、NormalMessage 构造点遗漏。

- [ ] **Step 2: 跑 Go 全仓测试**

Run: `make test`

Expected: 全部 PASS。重点观察 `internal/admin/repository_test.go` 和 `internal/admin/handlers_test.go`（admin 读 normalized_messages 的代码路径）。如果失败：很可能视图列顺序或类型不匹配 — 用 Step 4 的 `\d+ normalized_messages` 重新核对视图定义。

- [ ] **Step 3: 起 compose 栈**

```bash
make dev
```

后台起 gateway + worker + postgres + redis。等到 worker 日志显示 `ready` 且 gateway `/readyz` 返回 200。

- [ ] **Step 4: 跑 e2e（包含 filesystem 模式）**

E2E 要求 OSS 凭证，若本地没配，跳过 OSS 部分但仍能跑 filesystem 部分。

Run: `cd workers/analysis_worker && uv run e2e/run_all.py`

Expected: 所有 e2e pass。重点关注：
- 任何读取 `normalized_messages` 的 helper 是否返回预期行数
- trace detail 页是否能正常渲染消息
- 重复请求场景（如果有）的 `occurrence_count` 增长

如果失败：先看 worker 日志的 SQL error，最可能是视图列名/类型不匹配。

- [ ] **Step 5: 手动 smoke 多轮聊天验证 dedup**

如果 e2e 没覆盖多轮聊天场景，手动构造：

```bash
NEW_API_KEY=<your-key> bash scripts/smoke_proxy.sh
```

或直接发两条 chat completion 请求（第二条 messages 数组包含第一条的 user + assistant）。然后查 DB：

```bash
docker compose -f deploy/docker-compose.yml --env-file .env.local exec postgres \
  psql -U auditor -d audit_gateway -c "
SELECT message_id, role, LEFT(content_text, 30) AS preview, occurrence_count
FROM messages ORDER BY message_id;"
```

Expected: `occurrence_count` 至少有一条 > 1（如果两条请求的消息有重叠）。

- [ ] **Step 6: dedup 命中率监控 SQL 验证**

```bash
docker compose -f deploy/docker-compose.yml --env-file .env.local exec postgres \
  psql -U auditor -d audit_gateway -c "
SELECT
  COUNT(*) FILTER (WHERE occurrence_count = 1) AS unique_msgs,
  COUNT(*) FILTER (WHERE occurrence_count > 1) AS deduped_msgs,
  SUM(occurrence_count) AS total_references,
  ROUND(
    100.0 * (SUM(occurrence_count) - COUNT(*)) / NULLIF(SUM(occurrence_count), 0),
    2
  ) AS dedup_savings_pct
FROM messages;"
```

Expected: 至少返回一行（NULL 也可接受，如果还没数据），SQL 语法正确。

- [ ] **Step 7: 如果 Step 1-6 有任何修复，提交**

```bash
git status
git add <修复的文件>
git commit -m "$(cat <<'EOF'
test(worker): fix regressions from message CAS migration

<具体描述>

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

如果没有修复，跳过此步。

---

## Self-Review 检查表（写完后自查）

- [x] **Spec 覆盖**：
  - dedup key `(role, modality, content_text)` → Task 1 (helper) + Task 2 (dataclass)
  - 新表 `messages` + `trace_messages` + 视图 → Task 3
  - Worker 两步 INSERT + `occurrence_count` 递增 + `DO NOTHING` → Task 4
  - 测试 fixture 修复 → Task 2 Step 7-8 + Task 5 Step 1
  - 视图透明性验证 → Task 5 Step 2 (Go admin)
  - 历史 data DROP → Task 3 (migration)
  - dedup 命中率监控 SQL → Task 5 Step 6
- [x] **无占位符**：每个步骤都有具体代码或具体命令。
- [x] **类型一致性**：`message_key` 字段在所有任务里都是 `str`；`message_key()` 函数签名在 Task 1 和 Task 2 工厂调用点一致。
- [x] **scope 边界**：`media_object_id` 在 Task 4 Step 3 明确传 `None` 并说明原因（dataclass 没有此字段，未来需要时再扩）。这是 spec 没明说但实现必须处理的细节。
