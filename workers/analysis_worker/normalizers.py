import json
from typing import Any

from models import AnalysisResult, NormalizedMessage, TraceCapturedJob, text_hash


ANALYZER_VERSION = "normalizer_mvp_2026_04_28"


def normalize_json_trace(
    job: TraceCapturedJob,
    request_body: str,
    response_body: str,
) -> tuple[list[NormalizedMessage], list[AnalysisResult]]:
    request_json = _load_json_object(request_body)
    response_json = _load_json_object(response_body)
    messages: list[NormalizedMessage]
    if job.protocol_family == "openai_chat":
        messages = _normalize_openai_chat(job, request_json, response_json)
    elif job.protocol_family == "openai_responses":
        messages = _normalize_openai_responses(job, request_json, response_json)
    elif job.protocol_family == "claude_messages":
        messages = _normalize_claude_messages(job, request_json, response_json)
    else:
        messages = _normalize_generic_prompt(job, request_json)
    return messages, [_usage_result(job)]


def _load_json_object(body: str) -> dict[str, Any]:
    if not body:
        return {}
    try:
        loaded = json.loads(body)
    except json.JSONDecodeError:
        return {}
    return loaded if isinstance(loaded, dict) else {}


def _normalize_openai_chat(
    job: TraceCapturedJob,
    request_json: dict[str, Any],
    response_json: dict[str, Any],
) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    for index, item in enumerate(request_json.get("messages", [])):
        if not isinstance(item, dict):
            continue
        text = _content_to_text(item.get("content"))
        if text:
            messages.append(_message(job, "request", len(messages), str(item.get("role", "")), text, f"request.messages[{index}]", "openai_chat_message"))
    for index, choice in enumerate(response_json.get("choices", [])):
        if not isinstance(choice, dict):
            continue
        message = choice.get("message")
        if not isinstance(message, dict):
            continue
        text = _content_to_text(message.get("content"))
        if text:
            messages.append(_message(job, "response", len(messages), str(message.get("role", "assistant")), text, f"response.choices[{index}].message", "openai_chat_message"))
    return messages


def _normalize_openai_responses(
    job: TraceCapturedJob,
    request_json: dict[str, Any],
    response_json: dict[str, Any],
) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    request_input = request_json.get("input")
    if isinstance(request_input, list):
        for index, item in enumerate(request_input):
            text = _content_to_text(item)
            if text:
                role = str(item.get("role", "user")) if isinstance(item, dict) else "user"
                messages.append(_message(job, "request", len(messages), role, text, f"request.input[{index}]", "openai_responses_input"))
    else:
        request_text = _content_to_text(request_input)
        if request_text:
            messages.append(_message(job, "request", 0, "user", request_text, "request.input", "openai_responses_input"))
    output = response_json.get("output")
    if isinstance(output, list):
        for index, item in enumerate(output):
            text = _content_to_text(item)
            if text:
                role = str(item.get("role", "assistant")) if isinstance(item, dict) else "assistant"
                messages.append(_message(job, "response", len(messages), role, text, f"response.output[{index}]", "openai_responses_output"))
    return messages


def _normalize_claude_messages(
    job: TraceCapturedJob,
    request_json: dict[str, Any],
    response_json: dict[str, Any],
) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    system_text = _content_to_text(request_json.get("system"))
    if system_text:
        messages.append(_message(job, "request", len(messages), "system", system_text, "request.system", "claude_message"))
    for index, item in enumerate(request_json.get("messages", [])):
        if not isinstance(item, dict):
            continue
        text = _content_to_text(item.get("content"))
        if text:
            messages.append(_message(job, "request", len(messages), str(item.get("role", "")), text, f"request.messages[{index}]", "claude_message"))
    response_text = _content_to_text(response_json.get("content"))
    if response_text:
        messages.append(_message(job, "response", len(messages), "assistant", response_text, "response.content", "claude_message"))
    return messages


def _normalize_generic_prompt(job: TraceCapturedJob, request_json: dict[str, Any]) -> list[NormalizedMessage]:
    for key in ("prompt", "input", "text", "query"):
        text = _content_to_text(request_json.get(key))
        if text:
            return [_message(job, "request", 0, "user", text, f"request.{key}", "generic_prompt")]
    return []


def _content_to_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        parts = [_content_to_text(item) for item in value]
        return "\n".join(part for part in parts if part)
    if isinstance(value, dict):
        if isinstance(value.get("text"), str):
            return value["text"]
        if isinstance(value.get("content"), str):
            return value["content"]
        if isinstance(value.get("content"), list):
            return _content_to_text(value["content"])
        if isinstance(value.get("type"), str) and value["type"] == "output_text" and isinstance(value.get("text"), str):
            return value["text"]
    return ""


def _message(
    job: TraceCapturedJob,
    direction: str,
    sequence_index: int,
    role: str,
    content_text: str,
    source_path: str,
    protocol_item_type: str,
) -> NormalizedMessage:
    return NormalizedMessage(
        trace_id=job.trace_id,
        direction=direction,
        sequence_index=sequence_index,
        role=role,
        modality="text",
        content_text=content_text,
        content_text_hash=text_hash(content_text),
        media_url="",
        source_path=source_path,
        protocol_item_type=protocol_item_type,
        token_count_estimate=max(1, len(content_text.split())),
        metadata={"route_pattern": job.route_pattern, "protocol_family": job.protocol_family},
    )


def _usage_result(job: TraceCapturedJob) -> AnalysisResult:
    label = "usage_from_gateway_job" if job.usage_total_tokens > 0 else "usage_not_available"
    confidence = 1.0 if job.usage_total_tokens > 0 else 0.0
    return AnalysisResult(
        trace_id=job.trace_id,
        analyzer_name="usage_extraction",
        analyzer_version=ANALYZER_VERSION,
        policy_version="",
        category="usage_extraction",
        label=label,
        score=float(job.usage_total_tokens),
        confidence=confidence,
        severity="",
        result={
            "prompt_tokens": job.usage_prompt_tokens,
            "completion_tokens": job.usage_completion_tokens,
            "total_tokens": job.usage_total_tokens,
            "reasoning_tokens": job.usage_reasoning_tokens,
            "cached_tokens": job.usage_cached_tokens,
        },
    )
