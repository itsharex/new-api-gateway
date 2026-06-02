import httpx
import pytest

from llm_judge import LLMJudgeClient, LLMJudgeUnavailable


def test_posts_openai_compatible_chat_completion_request_with_json_instructions(monkeypatch):
    recorded = {}

    class FakeResponse:
        def raise_for_status(self):
            return None

        def json(self):
            return {
                "choices": [
                    {
                        "message": {
                            "content": '{"decision":"allow"}',
                        }
                    }
                ]
            }

    def fake_post(url, headers=None, json=None, timeout=None):
        recorded["url"] = url
        recorded["headers"] = headers
        recorded["json"] = json
        recorded["timeout"] = timeout
        return FakeResponse()

    monkeypatch.setattr(httpx, "post", fake_post)

    client = LLMJudgeClient(
        base_url="https://judge.example.com/",
        model="judge-model",
        api_key="secret-token",
        timeout_seconds=12.5,
        max_tokens=800,
    )

    result = client.judge({"trace_id": "trace_1", "score": 0.91})

    assert result == {"decision": "allow"}
    assert recorded["url"] == "https://judge.example.com/chat/completions"
    assert recorded["headers"] == {
        "Content-Type": "application/json",
        "Authorization": "Bearer secret-token",
    }
    assert recorded["timeout"] == 12.5
    assert recorded["json"]["model"] == "judge-model"
    assert recorded["json"]["temperature"] == 0
    assert recorded["json"]["max_tokens"] == 800
    assert recorded["json"]["messages"][0]["role"] == "system"
    assert "JSON" in recorded["json"]["messages"][0]["content"]
    assert "untrusted" in recorded["json"]["messages"][0]["content"]
    assert recorded["json"]["messages"][1]["role"] == "user"
    assert recorded["json"]["messages"][1]["content"] == '{"score": 0.91, "trace_id": "trace_1"}'


def test_accepts_json_wrapped_in_markdown_fence(monkeypatch):
    class FakeResponse:
        def raise_for_status(self):
            return None

        def json(self):
            return {
                "choices": [
                    {
                        "message": {
                            "content": "```json\n{\"decision\":\"deny\"}\n```",
                        }
                    }
                ]
            }

    monkeypatch.setattr(httpx, "post", lambda *args, **kwargs: FakeResponse())

    client = LLMJudgeClient(base_url="https://judge.example.com", model="judge-model")

    result = client.judge({"trace_id": "trace_2"})

    assert result == {"decision": "deny"}


def test_raises_unavailable_on_timeout(monkeypatch):
    def fake_post(*args, **kwargs):
        raise httpx.TimeoutException("timed out")

    monkeypatch.setattr(httpx, "post", fake_post)

    client = LLMJudgeClient(base_url="https://judge.example.com", model="judge-model")

    with pytest.raises(LLMJudgeUnavailable) as exc_info:
        client.judge({"trace_id": "trace_timeout"})

    assert exc_info.value.error_type == "timeout"
    assert "timed out" in exc_info.value.message


def test_raises_unavailable_on_invalid_json(monkeypatch):
    class FakeResponse:
        def raise_for_status(self):
            return None

        def json(self):
            return {
                "choices": [
                    {
                        "message": {
                            "content": "not json",
                        }
                    }
                ]
            }

    monkeypatch.setattr(httpx, "post", lambda *args, **kwargs: FakeResponse())

    client = LLMJudgeClient(base_url="https://judge.example.com", model="judge-model")

    with pytest.raises(LLMJudgeUnavailable) as exc_info:
        client.judge({"trace_id": "trace_invalid_json"})

    assert exc_info.value.error_type == "invalid_json"
    assert "not json" in exc_info.value.message
