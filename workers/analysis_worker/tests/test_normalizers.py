import json

from models import TraceCapturedJob
from normalizers import normalize_json_trace


def job(protocol_family: str, route_pattern: str = "/v1/chat/completions") -> TraceCapturedJob:
    return TraceCapturedJob(
        type="trace_captured",
        trace_id="trace_1",
        route_pattern=route_pattern,
        protocol_family=protocol_family,
        capture_mode="raw_and_normalized",
        employee_no="E10001",
        model_requested="gpt-4.1",
        usage_prompt_tokens=11,
        usage_completion_tokens=7,
        usage_total_tokens=18,
    )


def test_openai_chat_messages_are_normalized():
    request_body = json.dumps({
        "model": "gpt-4.1",
        "messages": [
            {"role": "system", "content": "You are helpful."},
            {"role": "user", "content": "Summarize the incident."}
        ]
    })
    response_body = json.dumps({
        "choices": [
            {"message": {"role": "assistant", "content": "The incident was resolved."}}
        ],
        "usage": {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}
    })

    messages, results = normalize_json_trace(job("openai_chat"), request_body, response_body)

    assert [message.role for message in messages] == ["system", "user", "assistant"]
    assert messages[1].direction == "request"
    assert messages[2].direction == "response"
    assert messages[2].content_text == "The incident was resolved."
    assert results[0].category == "usage_extraction"
    assert results[0].label == "usage_from_gateway_job"


def test_claude_messages_are_normalized():
    request_body = json.dumps({
        "model": "claude-3-5-sonnet",
        "messages": [
            {"role": "user", "content": [{"type": "text", "text": "Review this diff."}]}
        ]
    })
    response_body = json.dumps({
        "content": [{"type": "text", "text": "The diff is safe."}],
        "usage": {"input_tokens": 5, "output_tokens": 4}
    })

    messages, _ = normalize_json_trace(job("claude_messages"), request_body, response_body)

    assert len(messages) == 2
    assert messages[0].content_text == "Review this diff."
    assert messages[1].role == "assistant"
    assert messages[1].content_text == "The diff is safe."


def test_claude_top_level_system_is_normalized():
    request_body = json.dumps({
        "model": "claude-3-5-sonnet",
        "system": "Use the incident response rubric.",
        "messages": [
            {"role": "user", "content": [{"type": "text", "text": "Review this diff."}]}
        ],
    })
    response_body = json.dumps({
        "content": [{"type": "text", "text": "The diff is safe."}],
    })

    messages, _ = normalize_json_trace(job("claude_messages"), request_body, response_body)

    assert [message.role for message in messages] == ["system", "user", "assistant"]
    assert messages[0].direction == "request"
    assert messages[0].content_text == "Use the incident response rubric."
    assert messages[0].source_path == "request.system"


def test_openai_responses_content_blocks_are_normalized():
    request_body = json.dumps({
        "model": "gpt-4.1",
        "input": [
            {
                "type": "message",
                "role": "user",
                "content": [
                    {"type": "input_text", "text": "Summarize the incident."},
                    {"type": "input_text", "text": "Include action items."},
                ],
            }
        ],
    })
    response_body = json.dumps({
        "output": [
            {
                "type": "message",
                "role": "assistant",
                "content": [
                    {"type": "output_text", "text": "The incident was resolved."},
                    {"type": "output_text", "text": "Action items were assigned."},
                ],
            }
        ],
    })

    messages, _ = normalize_json_trace(job("openai_responses", "/v1/responses"), request_body, response_body)

    assert len(messages) == 2
    assert messages[0].direction == "request"
    assert messages[0].role == "user"
    assert messages[0].content_text == "Summarize the incident.\nInclude action items."
    assert messages[1].direction == "response"
    assert messages[1].role == "assistant"
    assert messages[1].content_text == "The incident was resolved.\nAction items were assigned."


def test_openai_responses_input_message_items_preserve_boundaries_and_roles():
    request_body = json.dumps({
        "model": "gpt-4.1",
        "input": [
            {
                "type": "message",
                "role": "developer",
                "content": [
                    {"type": "input_text", "text": "Use the incident response rubric."},
                ],
            },
            {
                "type": "message",
                "role": "user",
                "content": [
                    {"type": "input_text", "text": "Summarize the incident."},
                ],
            },
        ],
    })
    response_body = json.dumps({
        "output": [
            {
                "type": "message",
                "role": "assistant",
                "content": [
                    {"type": "output_text", "text": "The incident was resolved."},
                ],
            }
        ],
    })

    messages, _ = normalize_json_trace(job("openai_responses", "/v1/responses"), request_body, response_body)

    assert len(messages) == 3
    assert [message.direction for message in messages] == ["request", "request", "response"]
    assert [message.role for message in messages] == ["developer", "user", "assistant"]
    assert [message.content_text for message in messages] == [
        "Use the incident response rubric.",
        "Summarize the incident.",
        "The incident was resolved.",
    ]
    assert [message.source_path for message in messages[:2]] == ["request.input[0]", "request.input[1]"]


def test_generic_json_prompt_is_used_for_images():
    request_body = json.dumps({"model": "gpt-image-1", "prompt": "Draw the launch diagram"})
    response_body = json.dumps({"created": 1777366800})

    messages, _ = normalize_json_trace(job("openai_images", "/v1/images/generations"), request_body, response_body)

    assert len(messages) == 1
    assert messages[0].role == "user"
    assert messages[0].content_text == "Draw the launch diagram"
    assert messages[0].protocol_item_type == "generic_prompt"
