# LLM Judge Intent Filtering Design

## Problem

The LLM judge receives a `bundle` whose `intent.text` field contains all `role=user, direction=request` messages concatenated together (lowercase, truncated to 4000 chars). This raw text includes noise that hurts classification accuracy and wastes tokens:

1. Tool call results (`role=tool`/`function`) or tool parameters embedded in user messages
2. Base64 media payloads that survived text extraction
3. Long code blocks, JSON blocks, and pasted documents
4. Multi-turn conversation accumulation — all turns mixed together
5. System prompt fallback text when user messages are absent

## Approach

Pure rule-based filtering inside `extract_user_intent()`. No changes to the normalizer output — other downstream consumers (keyword matching, anomaly detection) continue to see unfiltered `NormalizedMessage` data.

## Design

### Layer 1: Message Type Filter

New internal function replaces `_request_text_by_roles` for intent extraction:

- **Skip**: `role` in `{tool, function}`; `protocol_item_type` in `{base64_media, base64_media_extracted}`
- **Keep**: `role` in `{user}` (primary), `{system, developer}` (fallback when no user text)

### Layer 2: Content Pattern Filter

Applied to each surviving message's `content_text` before truncation:

| Pattern | Regex | Replacement |
|---|---|---|
| Code block | `` ```[\s\S]*?``` `` | `[CODE_BLOCK]` |
| JSON block | Heuristic: `{`-starting segment containing `:` and `"`, length > 200 chars | `[JSON_BLOCK]` |
| Base64 string | `[A-Za-z0-9+/=]{100,}` | `[BASE64]` |

Processing order: code block first (prevents nested matching), then JSON, then base64.

### Layer 3: Turn Truncation

From the filtered user message list, keep **first 3 + last 3**, deduplicated (preserving original order).

- 10 messages → indices [0, 1, 2, 7, 8, 9]
- 5 messages → all kept (overlap between head and tail covers everything)
- 3 or fewer → all kept

### Layer 4: Per-Message Length Truncation

Each message exceeding 800 chars is truncated to first 800 chars + `[...truncated, N chars omitted]`.

### Final Assembly

Filtered messages are joined with `\n`, lowercased, then subject to the existing `MAX_INTENT_CHARS = 4000` global cap.

## Scope

- Modified functions: `extract_user_intent()`, add new `_filter_intent_messages()` and `_strip_content_noise()` helpers
- New test file or extended tests in `test_work_relevance.py`
- No changes to `_build_llm_bundle`, `_request_text_by_roles`, normalizer, or other downstream consumers

## Impact

- LLM judge token usage should drop significantly for tool-heavy and multi-turn traces
- Classification accuracy should improve (less noise diluting the actual user intent signal)
- Zero runtime overhead (regex is cheaper than the HTTP call that follows)
- No behavior change for traces that don't trigger LLM judge
