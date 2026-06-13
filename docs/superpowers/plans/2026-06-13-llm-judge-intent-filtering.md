# LLM Judge Intent Filtering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Filter noise (tool calls, base64, code blocks, JSON payloads, excess turns) from the user intent text sent to the LLM judge, improving classification accuracy and reducing token cost.

**Architecture:** Add three pure-function helpers (`_strip_content_noise`, `_filter_intent_messages`, `_truncate_message`) in `work_relevance.py` and wire them into `extract_user_intent()`. No changes to normalizer output or other downstream consumers.

**Tech Stack:** Python 3.11+, `re` stdlib, existing pytest test suite.

---

## File Structure

| Action | File | Responsibility |
|---|---|---|
| Modify | `workers/analysis_worker/work_relevance.py` | Add `import re`, 3 helpers, modify `extract_user_intent()` |
| Modify | `workers/analysis_worker/tests/test_work_relevance.py` | Add tests for new helpers and modified `extract_user_intent()` |

---

### Task 1: Add `import re` to `work_relevance.py`

**Files:**
- Modify: `workers/analysis_worker/work_relevance.py:1`

- [ ] **Step 1: Add `import re` after existing imports**

Current line 1-4:
```python
from dataclasses import dataclass
from typing import Any

from models import ContextCatalogEntry, NormalizedMessage, TraceCapturedJob, WorkRelevanceAssessment
```

Change to:
```python
import re
from dataclasses import dataclass
from typing import Any

from models import ContextCatalogEntry, NormalizedMessage, TraceCapturedJob, WorkRelevanceAssessment
```

- [ ] **Step 2: Verify existing tests still pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -q`
Expected: All existing tests PASS (no behavior change yet).

---

### Task 2: `_truncate_message` helper + tests

**Files:**
- Modify: `workers/analysis_worker/work_relevance.py` (add after `_request_text_by_roles`, ~line 290)
- Modify: `workers/analysis_worker/tests/test_work_relevance.py` (add tests)

- [ ] **Step 1: Write failing tests**

Append to `tests/test_work_relevance.py`, adding the import at the top:

Add `_truncate_message` to the import line (line 5):
```python
from work_relevance import ANALYZER_VERSION, classify_work_relevance, extract_user_intent, _truncate_message
```

Append these tests at end of file:

```python
def test_truncate_message_keeps_short_text():
    assert _truncate_message("hello world") == "hello world"


def test_truncate_message_truncates_at_limit():
    text = "A" * 900
    result = _truncate_message(text, max_chars=800)
    assert result == "A" * 800 + "[...truncated, 100 chars omitted]"


def test_truncate_message_keeps_text_at_exact_limit():
    text = "A" * 800
    assert _truncate_message(text, max_chars=800) == text
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py::test_truncate_message_keeps_short_text -v`
Expected: FAIL with `ImportError: cannot import name '_truncate_message'`

- [ ] **Step 3: Implement `_truncate_message`**

Add after `_request_text_by_roles` (after line ~290) in `work_relevance.py`:

```python
def _truncate_message(text: str, max_chars: int = 800) -> str:
    if len(text) <= max_chars:
        return text
    omitted = len(text) - max_chars
    return text[:max_chars] + f"[...truncated, {omitted} chars omitted]"
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -k "truncate_message" -v`
Expected: All 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/work_relevance.py workers/analysis_worker/tests/test_work_relevance.py
git commit -m "feat(worker): add _truncate_message helper for intent text filtering"
```

---

### Task 3: `_strip_content_noise` + `_replace_long_json` helpers + tests

**Files:**
- Modify: `workers/analysis_worker/work_relevance.py` (add after `_truncate_message`)
- Modify: `workers/analysis_worker/tests/test_work_relevance.py` (add tests + import)

- [ ] **Step 1: Write failing tests**

Add `_strip_content_noise` to the import line in `tests/test_work_relevance.py`:
```python
from work_relevance import ANALYZER_VERSION, classify_work_relevance, extract_user_intent, _truncate_message, _strip_content_noise
```

Append these tests at end of file:

```python
def test_strip_content_noise_replaces_code_block():
    text = 'Here is my code:\n```python\nprint("hello")\n```\nPlease review it.'
    result = _strip_content_noise(text)
    assert result == "Here is my code:\n[CODE_BLOCK]\nPlease review it."


def test_strip_content_noise_replaces_long_json():
    json_payload = '{"key": "' + "x" * 250 + '"}'
    text = f"The config is {json_payload} please help."
    result = _strip_content_noise(text)
    assert "[JSON_BLOCK]" in result
    assert "please help" in result


def test_strip_content_noise_keeps_short_json():
    text = 'Set the field {"name": "test"} to that value.'
    assert _strip_content_noise(text) == text


def test_strip_content_noise_replaces_base64():
    text = "The image is data:image/png;base64," + "A" * 200 + " please describe."
    result = _strip_content_noise(text)
    assert "[BASE64]" in result
    assert "please describe" in result


def test_strip_content_noise_code_block_takes_priority_over_json():
    json_body = '{"key": "' + "x" * 250 + '"}'
    text = f"```json\n{json_body}\n```"
    result = _strip_content_noise(text)
    assert result == "[CODE_BLOCK]"


def test_strip_content_noise_preserves_plain_text():
    text = "Please help me debug the authentication middleware."
    assert _strip_content_noise(text) == text
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -k "strip_content_noise" -v`
Expected: FAIL with `ImportError: cannot import name '_strip_content_noise'`

- [ ] **Step 3: Implement `_replace_long_json` and `_strip_content_noise`**

Add after `_truncate_message` in `work_relevance.py`:

```python
_CODE_BLOCK_RE = re.compile(r"```[\s\S]*?```")
_BASE64_RE = re.compile(r"[A-Za-z0-9+/=]{100,}")


def _replace_long_json(text: str, min_length: int = 200) -> str:
    result: list[str] = []
    i = 0
    while i < len(text):
        if text[i] != "{":
            result.append(text[i])
            i += 1
            continue
        depth = 0
        j = i
        while j < len(text):
            if text[j] == "{":
                depth += 1
            elif text[j] == "}":
                depth -= 1
                if depth == 0:
                    segment = text[i : j + 1]
                    if len(segment) >= min_length and '"' in segment and ":" in segment:
                        result.append("[JSON_BLOCK]")
                    else:
                        result.append(segment)
                    j += 1
                    break
            j += 1
        else:
            result.append(text[i:])
            break
        i = j
    return "".join(result)


def _strip_content_noise(text: str) -> str:
    text = _CODE_BLOCK_RE.sub("[CODE_BLOCK]", text)
    text = _replace_long_json(text)
    text = _BASE64_RE.sub("[BASE64]", text)
    return text
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -k "strip_content_noise" -v`
Expected: All 6 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/work_relevance.py workers/analysis_worker/tests/test_work_relevance.py
git commit -m "feat(worker): add _strip_content_noise and _replace_long_json helpers"
```

---

### Task 4: `_filter_intent_messages` helper + tests

**Files:**
- Modify: `workers/analysis_worker/work_relevance.py` (add after `_strip_content_noise`)
- Modify: `workers/analysis_worker/tests/test_work_relevance.py` (add tests + import)

- [ ] **Step 1: Write failing tests**

Add `_filter_intent_messages` to the import line in `tests/test_work_relevance.py`:
```python
from work_relevance import ANALYZER_VERSION, classify_work_relevance, extract_user_intent, _truncate_message, _strip_content_noise, _filter_intent_messages
```

Append these tests at end of file:

```python
def _msg(
    role: str,
    text: str,
    direction: str = "request",
    protocol_item_type: str = "openai_chat_message",
) -> NormalizedMessage:
    return NormalizedMessage(
        trace_id="t",
        direction=direction,
        sequence_index=0,
        role=role,
        modality="text",
        content_text=text,
        content_text_hash="h",
        media_url="",
        source_path="request.messages[0]",
        protocol_item_type=protocol_item_type,
        token_count_estimate=1,
        metadata={},
    )


def test_filter_intent_messages_returns_user_text():
    msgs = [
        _msg("user", "hello"),
        _msg("assistant", "world", direction="response"),
    ]
    assert _filter_intent_messages(msgs, {"user"}) == ["hello"]


def test_filter_intent_messages_skips_base64_media():
    msgs = [
        _msg("user", "describe this", protocol_item_type="base64_media"),
        _msg("user", "debug the handler"),
    ]
    assert _filter_intent_messages(msgs, {"user"}) == ["debug the handler"]


def test_filter_intent_messages_skips_base64_media_extracted():
    msgs = [
        _msg("user", "analyze image", protocol_item_type="base64_media_extracted"),
        _msg("user", "fix the bug"),
    ]
    assert _filter_intent_messages(msgs, {"user"}) == ["fix the bug"]


def test_filter_intent_messages_head_and_tail_selection():
    msgs = [_msg("user", f"turn {i}") for i in range(10)]
    result = _filter_intent_messages(msgs, {"user"})
    assert result == ["turn 0", "turn 1", "turn 2", "turn 7", "turn 8", "turn 9"]


def test_filter_intent_messages_keeps_all_when_few():
    msgs = [_msg("user", f"turn {i}") for i in range(4)]
    result = _filter_intent_messages(msgs, {"user"})
    assert result == ["turn 0", "turn 1", "turn 2", "turn 3"]


def test_filter_intent_messages_keeps_all_when_three():
    msgs = [_msg("user", f"turn {i}") for i in range(3)]
    result = _filter_intent_messages(msgs, {"user"})
    assert result == ["turn 0", "turn 1", "turn 2"]
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -k "filter_intent" -v`
Expected: FAIL with `ImportError: cannot import name '_filter_intent_messages'`

- [ ] **Step 3: Implement `_filter_intent_messages`**

Add after `_strip_content_noise` in `work_relevance.py`:

```python
_SKIP_INTENT_TYPES = frozenset({"base64_media", "base64_media_extracted"})


def _filter_intent_messages(
    messages: list[NormalizedMessage],
    roles: set[str],
    max_head: int = 3,
    max_tail: int = 3,
) -> list[str]:
    texts = [
        m.content_text
        for m in messages
        if m.direction == "request"
        and m.role in roles
        and m.protocol_item_type not in _SKIP_INTENT_TYPES
        and m.content_text
    ]
    if len(texts) <= max_head + max_tail:
        return texts
    indices = set(range(max_head)) | set(range(len(texts) - max_tail, len(texts)))
    return [texts[i] for i in sorted(indices)]
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -k "filter_intent" -v`
Expected: All 6 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/work_relevance.py workers/analysis_worker/tests/test_work_relevance.py
git commit -m "feat(worker): add _filter_intent_messages helper for head+tail selection"
```

---

### Task 5: Wire helpers into `extract_user_intent` + integration tests

**Files:**
- Modify: `workers/analysis_worker/work_relevance.py:269-280` (rewrite `extract_user_intent`)
- Modify: `workers/analysis_worker/tests/test_work_relevance.py` (add integration tests)

- [ ] **Step 1: Write integration tests**

Append these tests at end of `tests/test_work_relevance.py`:

```python
def test_extract_user_intent_skips_tool_return_messages():
    msgs = [
        _msg("user", "Call the weather API"),
        _msg("tool", '{"temperature": 22, "humidity": 65, "wind_speed": 10, "forecast": "sunny with light clouds"}', protocol_item_type="openai_chat_message"),
        _msg("user", "What about tomorrow?"),
    ]
    intent = extract_user_intent(msgs)
    assert "call the weather api" in intent.text
    assert "temperature" not in intent.text
    assert "what about tomorrow?" in intent.text


def test_extract_user_intent_strips_code_blocks_from_text():
    code = "def hello():\n    print('world')\n    return True"
    msgs = [message(f"Review this:\n```python\n{code}\n```\nIs it correct?")]
    intent = extract_user_intent(msgs)
    assert "[CODE_BLOCK]" in intent.text
    assert "is it correct?" in intent.text


def test_extract_user_intent_strips_long_json_from_text():
    json_payload = '{"data": "' + "x" * 300 + '", "meta": {"page": 1, "total": 500}}'
    msgs = [message(f"Process this data: {json_payload}")]
    intent = extract_user_intent(msgs)
    assert "[JSON_BLOCK]" in intent.text


def test_extract_user_intent_head_tail_selection():
    msgs = [message(f"Turn {i} message") for i in range(10)]
    intent = extract_user_intent(msgs)
    assert "turn 0 message" in intent.text
    assert "turn 1 message" in intent.text
    assert "turn 2 message" in intent.text
    assert "turn 7 message" in intent.text
    assert "turn 8 message" in intent.text
    assert "turn 9 message" in intent.text
    assert "turn 4" not in intent.text
    assert "turn 5" not in intent.text


def test_extract_user_intent_truncates_long_single_message():
    msgs = [message("A" * 900)]
    intent = extract_user_intent(msgs, max_chars=2000)
    assert intent.text.endswith("chars omitted]")
    assert len(intent.text) < 900
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -k "extract_user_intent_skips or extract_user_intent_strips or extract_user_intent_head or extract_user_intent_truncates" -v`
Expected: FAIL — `tool` role messages still included, code blocks not stripped, no head/tail selection.

- [ ] **Step 3: Rewrite `extract_user_intent` to use new helpers**

Replace `extract_user_intent` (lines 269-280 in `work_relevance.py`) with:

```python
def extract_user_intent(messages: list[NormalizedMessage], max_chars: int = MAX_INTENT_CHARS) -> ExtractedIntent:
    relevant = _filter_intent_messages(messages, {"user"})
    if not relevant:
        relevant = _filter_intent_messages(messages, {"developer", "system"})
    cleaned = [_truncate_message(_strip_content_noise(t)) for t in relevant]
    text = "\n".join(cleaned).lower()
    original_length = len(text)
    truncated = original_length > max_chars
    return ExtractedIntent(
        text=text[:max_chars],
        original_length=original_length,
        truncated=truncated,
    )
```

- [ ] **Step 4: Run ALL tests to verify pass**

Run: `cd workers/analysis_worker && uv run pytest tests/test_work_relevance.py -v`
Expected: All tests PASS, including existing `test_extract_user_intent_*` tests (they use `openai_chat_message` type and short text, unaffected by filters).

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/work_relevance.py workers/analysis_worker/tests/test_work_relevance.py
git commit -m "feat(worker): wire intent filtering into extract_user_intent for LLM judge"
```

---

### Task 6: Full regression + final commit

**Files:** None (verification only)

- [ ] **Step 1: Run full worker test suite**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: All tests PASS.

- [ ] **Step 2: Run full Go test suite**

Run: `cd /Users/roy/codes/new-api-gateway && make test`
Expected: All tests PASS (worker changes don't affect Go tests).

- [ ] **Step 3: Verify all commits are clean**

Run: `git log --oneline -5`
Expected: 4-5 commits for this feature, clean history.
