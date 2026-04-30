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
    response_events = _load_sse_json_events(response_body)
    if response_events and not response_json:
        response_json = _response_json_from_sse_events(response_events)
    messages: list[NormalizedMessage]
    if job.protocol_family == "openai_chat":
        messages = _normalize_openai_chat(job, request_json, response_json)
    elif job.protocol_family == "openai_responses":
        messages = _normalize_openai_responses(job, request_json, response_json)
    elif job.protocol_family == "claude_messages":
        messages = _normalize_claude_messages(job, request_json, response_json)
    elif job.protocol_family == "gemini":
        messages = _normalize_gemini(job, request_json, response_json)
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
        messages.extend(_part_messages(
            job,
            "request",
            str(item.get("role", "")),
            item.get("content"),
            f"request.messages[{index}]",
            "openai_chat_message",
        ))
    for index, choice in enumerate(response_json.get("choices", [])):
        if not isinstance(choice, dict):
            continue
        message = choice.get("message")
        if not isinstance(message, dict):
            continue
        messages.extend(_part_messages(
            job,
            "response",
            str(message.get("role", "assistant")),
            message.get("content"),
            f"response.choices[{index}].message",
            "openai_chat_message",
        ))
    for sequence_index, message in enumerate(messages):
        messages[sequence_index] = _with_sequence_index(message, sequence_index)
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
            role = str(item.get("role", "user")) if isinstance(item, dict) else "user"
            content = item.get("content") if isinstance(item, dict) and "content" in item else item
            messages.extend(_part_messages(
                job,
                "request",
                role,
                content,
                f"request.input[{index}]",
                "openai_responses_input",
            ))
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
    for sequence_index, message in enumerate(messages):
        messages[sequence_index] = _with_sequence_index(message, sequence_index)
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


def _normalize_gemini(
    job: TraceCapturedJob,
    request_json: dict[str, Any],
    response_json: dict[str, Any],
) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    for index, content in enumerate(request_json.get("contents", [])):
        if not isinstance(content, dict):
            continue
        messages.extend(_part_messages(
            job,
            "request",
            str(content.get("role", "user")),
            content.get("parts"),
            f"request.contents[{index}]",
            "gemini_content_part",
        ))
    for index, candidate in enumerate(response_json.get("candidates", [])):
        if not isinstance(candidate, dict):
            continue
        content = candidate.get("content")
        if not isinstance(content, dict):
            continue
        messages.extend(_part_messages(
            job,
            "response",
            str(content.get("role", "model")),
            content.get("parts"),
            f"response.candidates[{index}].content",
            "gemini_content_part",
        ))
    for sequence_index, message in enumerate(messages):
        messages[sequence_index] = _with_sequence_index(message, sequence_index)
    return messages


def _part_messages(
    job: TraceCapturedJob,
    direction: str,
    role: str,
    content: Any,
    source_path: str,
    protocol_item_type: str,
) -> list[NormalizedMessage]:
    messages: list[NormalizedMessage] = []
    text = _content_to_text(content)
    if text:
        messages.append(_message(job, direction, 0, role, text, source_path, protocol_item_type))
    if isinstance(content, list):
        for index, item in enumerate(content):
            media = _media_message(job, direction, role, item, f"{source_path}.content[{index}]")
            if media:
                messages.append(media)
    elif isinstance(content, dict):
        media = _media_message(job, direction, role, content, source_path)
        if media:
            messages.append(media)
    return messages


def _media_message(
    job: TraceCapturedJob,
    direction: str,
    role: str,
    value: Any,
    source_path: str,
) -> NormalizedMessage | None:
    if not isinstance(value, dict):
        return None
    item_type = value.get("type")
    if item_type == "image_url" and isinstance(value.get("image_url"), dict):
        media_url = value["image_url"].get("url")
        if isinstance(media_url, str) and media_url:
            return _message(job, direction, 0, role, "", source_path, "image_url", modality="image", media_url=media_url)
    if item_type in {"input_image", "image"}:
        media_url = value.get("image_url") or value.get("url")
        if isinstance(media_url, str) and media_url:
            return _message(job, direction, 0, role, "", source_path, "image_url", modality="image", media_url=media_url)
    if item_type == "input_audio" and isinstance(value.get("input_audio"), dict):
        audio = value["input_audio"]
        if isinstance(audio.get("data"), str) or isinstance(audio.get("url"), str):
            return _message(
                job,
                direction,
                0,
                role,
                "",
                source_path,
                "base64_media" if isinstance(audio.get("data"), str) else "media_url",
                modality="audio",
                media_url=audio.get("url", "") if isinstance(audio.get("url"), str) else "",
            )
    if isinstance(value.get("inline_data"), dict):
        inline_data = value["inline_data"]
        mime_type = inline_data.get("mime_type", "")
        modality = "image" if isinstance(mime_type, str) and mime_type.startswith("image/") else "media"
        return _message(job, direction, 0, role, "", source_path, "base64_media", modality=modality)
    return None


def _load_sse_json_events(body: str) -> list[dict[str, Any]]:
    events: list[dict[str, Any]] = []
    data_lines: list[str] = []
    for raw_line in body.splitlines():
        line = raw_line.strip()
        if not line:
            _append_sse_event(events, data_lines)
            data_lines = []
            continue
        if not line.startswith("data:"):
            continue
        if data_lines:
            _append_sse_event(events, data_lines)
            data_lines = []
        data_lines.append(line.removeprefix("data:").strip())
    _append_sse_event(events, data_lines)
    return events


def _append_sse_event(events: list[dict[str, Any]], data_lines: list[str]) -> None:
    if not data_lines:
        return
    data = "\n".join(data_lines)
    if data == "[DONE]":
        return
    try:
        loaded = json.loads(data)
    except json.JSONDecodeError:
        return
    if isinstance(loaded, dict):
        events.append(loaded)


def _response_json_from_sse_events(events: list[dict[str, Any]]) -> dict[str, Any]:
    role = "assistant"
    content_parts: list[str] = []
    for event in events:
        choices = event.get("choices")
        if not isinstance(choices, list):
            continue
        for choice in choices:
            if not isinstance(choice, dict):
                continue
            delta = choice.get("delta")
            if not isinstance(delta, dict):
                continue
            if isinstance(delta.get("role"), str):
                role = delta["role"]
            content = delta.get("content")
            if isinstance(content, str):
                content_parts.append(content)
    if not content_parts:
        return {}
    return {"choices": [{"message": {"role": role, "content": "".join(content_parts)}}]}


def _content_to_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        parts = [_content_to_text(item) for item in value]
        return "\n".join(part for part in parts if part)
    if isinstance(value, dict):
        if value.get("type") in {"image_url", "input_image", "image", "input_audio"} or "inline_data" in value:
            return ""
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
    )


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
