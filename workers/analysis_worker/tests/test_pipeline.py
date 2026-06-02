import json
import math
import subprocess
import sys
from pathlib import Path

import pytest

from evidence import FilesystemEvidenceStore
from llm_judge import LLMJudgeClient
from llm_judge import LLMJudgeUnavailable
from main import create_llm_judge_from_env, llm_judge_metadata, process_job_line
from models import AnalysisContext, ContextCatalogEntry


class RecordingRepository:
    def __init__(self):
        self.messages = []
        self.results = []
        self.aggregates = []
        self.anomalies = []
        self.coverage_alerts = []
        self.analysis_context = AnalysisContext()
        self.context_requests = []

    def analysis_context_for(self, job):
        self.context_requests.append(job.trace_id)
        return self.analysis_context

    def save_trace_analysis(self, messages, results, aggregates, anomalies=(), coverage_alerts=()):
        self.messages.extend(messages)
        self.results.extend(results)
        self.aggregates.extend(aggregates)
        self.anomalies.extend(anomalies)
        self.coverage_alerts.extend(coverage_alerts)


class RecordingContextRepository:
    def __init__(self, contexts=None):
        self.contexts = list(contexts or [])

    def list_active_contexts(self):
        return self.contexts


class StubJudge:
    def __init__(self, result=None, error=None):
        self.result = result or {}
        self.error = error
        self.calls = []

    def judge(self, bundle):
        self.calls.append(bundle)
        if self.error is not None:
            raise self.error
        return self.result


def test_process_job_line_reads_evidence_normalizes_and_persists(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_1"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text(json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "Summarize incident"}]
    }), encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text(json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "Incident resolved"}}],
        "usage": {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}
    }), encoding="utf-8")
    repo = RecordingRepository()
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_1",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": "file:///raw/2026/04/28/trace_1/request_body.bin",
        "response_raw_ref": "file:///raw/2026/04/28/trace_1/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_prompt_tokens": 11,
        "usage_completion_tokens": 7,
        "usage_total_tokens": 18,
        "usage_reasoning_tokens": 2,
        "usage_cached_tokens": 3,
        "token_fingerprint": "tkfp_raw",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "alice",
        "status_code": 200,
        "upstream_status_code": 200,
        "stream": False,
        "request_started_at": "2026-04-28T13:45:22Z",
        "request_body_size": 128,
        "response_body_size": 256
    })

    response = process_job_line(line, FilesystemEvidenceStore(tmp_path), repo)

    assert response["accepted_trace_id"] == "trace_1"
    assert response["normalized_message_count"] == 2
    assert response["analysis_result_count"] == 2
    assert len(repo.messages) == 2
    assert len(repo.results) == 2
    assert [result.category for result in repo.results] == ["usage_extraction", "work_relevance"]
    assert [aggregate.bucket_size for aggregate in repo.aggregates] == ["hour", "day"]
    assert repo.aggregates[0].prompt_tokens == 11
    assert repo.aggregates[0].completion_tokens == 7
    assert repo.aggregates[0].cached_tokens == 3
    assert repo.aggregates[0].reasoning_tokens == 2
    assert repo.aggregates[0].total_tokens == 18
    assert repo.aggregates[0].request_body_bytes == 128
    assert repo.aggregates[0].response_body_bytes == 256


def test_process_job_line_works_with_minimal_dependencies():
    class MinimalEvidenceStore:
        def __init__(self, payloads):
            self.payloads = payloads

        def read_text(self, ref):
            return self.payloads[ref]

    class MinimalRepository:
        def __init__(self):
            self.messages = []
            self.results = []
            self.aggregates = []
            self.anomalies = []
            self.coverage_alerts = []

        def save_trace_analysis(self, messages, results, aggregates, anomalies=(), coverage_alerts=()):
            self.messages.extend(messages)
            self.results.extend(results)
            self.aggregates.extend(aggregates)
            self.anomalies.extend(anomalies)
            self.coverage_alerts.extend(coverage_alerts)

    request_ref = "file:///raw/2026/04/28/trace_no_deps/request_body.bin"
    response_ref = "file:///raw/2026/04/28/trace_no_deps/response_body.bin"
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_no_deps",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": request_ref,
        "response_raw_ref": response_ref,
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 18,
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
    })
    evidence_store = MinimalEvidenceStore({
        request_ref: json.dumps({
            "model": "gpt-4.1",
            "messages": [{"role": "user", "content": "Debug the new-api gateway route tests"}],
        }),
        response_ref: json.dumps({
            "choices": [{"message": {"role": "assistant", "content": "Check the route registry test."}}],
            "usage": {"total_tokens": 18},
        }),
    })
    repo = MinimalRepository()

    response = process_job_line(line, evidence_store, repo)

    assert response["worker_status"] == "processed"
    assert response["work_relevance_count"] == 1
    assert response["media_assets_extracted"] == 0
    work_result = next(result for result in repo.results if result.category == "work_relevance")
    assert work_result.analyzer_version == "work_relevance_mvp_2026_04_28"
    assert work_result.result["evidence"][0]["source"] == "keyword"


def test_process_job_line_reconstructs_sse_response(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_stream"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text(json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "Stream this"}],
    }), encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text("\n".join([
        'data: {"choices":[{"delta":{"role":"assistant","content":"hello"}}]}',
        'data: {"choices":[{"delta":{"content":" world"}}]}',
        "data: [DONE]",
        "",
    ]), encoding="utf-8")
    repo = RecordingRepository()
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_stream",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": "file:///raw/2026/04/28/trace_stream/request_body.bin",
        "response_raw_ref": "file:///raw/2026/04/28/trace_stream/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 10,
        "status_code": 200,
        "upstream_status_code": 200,
        "stream": True,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(line, FilesystemEvidenceStore(tmp_path), repo)

    assert response["normalized_message_count"] == 2
    assert any(message.direction == "response" and message.content_text == "hello world" for message in repo.messages)


def test_process_job_line_persists_work_relevance_result(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_work"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text(json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "Debug the new-api gateway route tests"}]
    }), encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text(json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "Check the route registry test."}}],
        "usage": {"total_tokens": 100}
    }), encoding="utf-8")
    repo = RecordingRepository()
    contexts = RecordingContextRepository([ContextCatalogEntry(
        id=1,
        context_type="repo",
        name="new-api-gateway",
        description="Audit gateway",
        keywords=["new-api", "gateway"],
        aliases=[],
        owner="platform",
        expected_task_categories=["coding", "debugging"],
        expected_models=["gpt-4.1"],
        expected_usage_level="normal",
        active=True,
    )])
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_work",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": "file:///raw/2026/04/28/trace_work/request_body.bin",
        "response_raw_ref": "file:///raw/2026/04/28/trace_work/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 100,
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(line, FilesystemEvidenceStore(tmp_path), repo, contexts)

    assert response["work_relevance_count"] == 1
    work_results = [result for result in repo.results if result.category == "work_relevance"]
    assert len(work_results) == 1
    assert work_results[0].label == "debugging"
    assert work_results[0].result["work_related_score"] >= 0.9
    assert work_results[0].result["decision"] == "work_related"
    assert work_results[0].result["recommended_action"] == "allow"


def test_process_job_line_returns_degraded_llm_fallback_metadata(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_conflict"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text(json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "In relay, rewrite my resume bullet about debugging this route."}]
    }), encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text(json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "Use a stronger business impact framing."}}],
        "usage": {"total_tokens": 1200}
    }), encoding="utf-8")
    repo = RecordingRepository()
    contexts = RecordingContextRepository([ContextCatalogEntry(
        id=1,
        context_type="repo",
        name="new-api-gateway",
        description="Audit gateway",
        keywords=["new-api", "gateway"],
        aliases=["relay"],
        owner="platform",
        expected_task_categories=["coding", "debugging"],
        expected_models=["gpt-4.1"],
        expected_usage_level="normal",
        active=True,
    )])
    judge = StubJudge(error=LLMJudgeUnavailable("timeout", "judge timed out"))
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_conflict",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": "file:///raw/2026/04/28/trace_conflict/request_body.bin",
        "response_raw_ref": "file:///raw/2026/04/28/trace_conflict/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 1200,
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(
        line,
        FilesystemEvidenceStore(tmp_path),
        repo,
        contexts,
        llm_judge=judge,
    )

    assert response["llm_judge_status"] == "degraded"
    assert response["llm_judge_error_type"] == "timeout"
    assert response["llm_judge_fallback_count"] == 1
    work_result = next(result for result in repo.results if result.category == "work_relevance")
    assert work_result.result["recommended_action"] == "review_conflict"
    assert work_result.result["evidence"][-1] == {
        "kind": "llm_unavailable",
        "category": "timeout",
        "weight": 0.0,
        "source": "llm_judge",
        "snippet": "timeout",
        "reason": "LLM judge unavailable due to timeout.",
    }


def test_llm_judge_metadata_reads_kind_and_category_contract():
    class FakeWorkRelevance:
        evidence = [
            {
                "kind": "llm_unavailable",
                "category": "timeout",
                "source": "llm_judge",
                "snippet": "should-not-be-used",
            }
        ]

    assert llm_judge_metadata(FakeWorkRelevance()) == {
        "llm_judge_status": "degraded",
        "llm_judge_error_type": "timeout",
        "llm_judge_fallback_count": 1,
    }


def test_create_llm_judge_from_env_returns_none_when_config_absent(monkeypatch):
    monkeypatch.delenv("LLM_JUDGE_BASE_URL", raising=False)
    monkeypatch.delenv("LLM_JUDGE_MODEL", raising=False)
    monkeypatch.delenv("LLM_JUDGE_API_KEY", raising=False)
    monkeypatch.delenv("LLM_JUDGE_TIMEOUT_SECONDS", raising=False)

    assert create_llm_judge_from_env() is None


@pytest.mark.parametrize(
    "env_overrides",
    [
        {"LLM_JUDGE_API_KEY": "secret-key"},
        {"LLM_JUDGE_TIMEOUT_SECONDS": "12.5"},
        {"LLM_JUDGE_API_KEY": "secret-key", "LLM_JUDGE_TIMEOUT_SECONDS": "12.5"},
    ],
)
def test_create_llm_judge_from_env_rejects_partial_configuration(monkeypatch, env_overrides):
    monkeypatch.delenv("LLM_JUDGE_BASE_URL", raising=False)
    monkeypatch.delenv("LLM_JUDGE_MODEL", raising=False)
    monkeypatch.delenv("LLM_JUDGE_API_KEY", raising=False)
    monkeypatch.delenv("LLM_JUDGE_TIMEOUT_SECONDS", raising=False)
    for key, value in env_overrides.items():
        monkeypatch.setenv(key, value)

    with pytest.raises(SystemExit, match="LLM_JUDGE"):
        create_llm_judge_from_env()


def test_create_llm_judge_from_env_rejects_base_url_without_model(monkeypatch):
    monkeypatch.setenv("LLM_JUDGE_BASE_URL", "https://judge.example.com")

    with pytest.raises(SystemExit, match="LLM_JUDGE_BASE_URL and LLM_JUDGE_MODEL must be set when any LLM_JUDGE_\\* variable is configured"):
        create_llm_judge_from_env()


def test_create_llm_judge_from_env_rejects_model_without_base_url(monkeypatch):
    monkeypatch.delenv("LLM_JUDGE_BASE_URL", raising=False)
    monkeypatch.setenv("LLM_JUDGE_MODEL", "judge-model")

    with pytest.raises(SystemExit, match="LLM_JUDGE_BASE_URL and LLM_JUDGE_MODEL must be set when any LLM_JUDGE_\\* variable is configured"):
        create_llm_judge_from_env()


def test_create_llm_judge_from_env_rejects_invalid_timeout(monkeypatch):
    monkeypatch.setenv("LLM_JUDGE_BASE_URL", "https://judge.example.com")
    monkeypatch.setenv("LLM_JUDGE_MODEL", "judge-model")
    monkeypatch.setenv("LLM_JUDGE_TIMEOUT_SECONDS", "not-a-number")

    with pytest.raises(SystemExit, match="LLM_JUDGE_TIMEOUT_SECONDS must be a valid number"):
        create_llm_judge_from_env()


@pytest.mark.parametrize("timeout_raw", ["0", "-1", str(math.inf), str(math.nan)])
def test_create_llm_judge_from_env_rejects_non_positive_or_non_finite_timeout(monkeypatch, timeout_raw):
    monkeypatch.setenv("LLM_JUDGE_BASE_URL", "https://judge.example.com")
    monkeypatch.setenv("LLM_JUDGE_MODEL", "judge-model")
    monkeypatch.setenv("LLM_JUDGE_TIMEOUT_SECONDS", timeout_raw)

    with pytest.raises(SystemExit, match="LLM_JUDGE_TIMEOUT_SECONDS must be a finite positive number"):
        create_llm_judge_from_env()


def test_create_llm_judge_from_env_builds_expected_client(monkeypatch):
    monkeypatch.setenv("LLM_JUDGE_BASE_URL", "https://judge.example.com/")
    monkeypatch.setenv("LLM_JUDGE_MODEL", "judge-model")
    monkeypatch.setenv("LLM_JUDGE_API_KEY", "secret-key")
    monkeypatch.setenv("LLM_JUDGE_TIMEOUT_SECONDS", "12.5")

    client = create_llm_judge_from_env()

    assert isinstance(client, LLMJudgeClient)
    assert client.base_url == "https://judge.example.com"
    assert client.model == "judge-model"
    assert client.api_key == "secret-key"
    assert client.timeout_seconds == 12.5


def test_create_llm_judge_from_env_accepts_valid_integer_timeout(monkeypatch):
    monkeypatch.setenv("LLM_JUDGE_BASE_URL", "https://judge.example.com/")
    monkeypatch.setenv("LLM_JUDGE_MODEL", "judge-model")
    monkeypatch.setenv("LLM_JUDGE_TIMEOUT_SECONDS", "7")

    client = create_llm_judge_from_env()

    assert isinstance(client, LLMJudgeClient)
    assert client.timeout_seconds == 7.0


def test_process_job_line_persists_anomaly_and_coverage_alert(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_gap"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text("{}", encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text("{}", encoding="utf-8")
    repo = RecordingRepository()
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_gap",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "",
        "request_raw_ref": "file:///raw/2026/04/28/trace_gap/request_body.bin",
        "response_raw_ref": "file:///raw/2026/04/28/trace_gap/response_body.bin",
        "request_content_type": "application/json",
        "response_content_type": "application/json",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 25001,
        "token_fingerprint": "tkfp_raw",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "",
        "status_code": 200,
        "upstream_status_code": 200,
        "stream": False,
        "request_started_at": "2026-04-28T13:45:22Z",
        "request_body_size": 2,
        "response_body_size": 2
    })

    response = process_job_line(line, FilesystemEvidenceStore(tmp_path), repo)

    assert response["worker_status"] == "processed"
    assert response["work_relevance_count"] == 1
    assert response["anomaly_count"] == 5
    assert response["coverage_alert_count"] == 1
    assert [alert.anomaly_type for alert in repo.anomalies] == [
        "identity_unresolved_success",
        "high_trace_tokens",
        "short_window_token_spike",
        "off_hours_high_usage",
        "unknown_high_cost",
    ]
    assert [alert.alert_code for alert in repo.coverage_alerts] == ["normalization_gap"]


def test_process_job_line_uses_repository_analysis_context(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_context"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text(json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "Summarize incident"}]
    }), encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text(json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "Incident resolved"}}],
        "usage": {"total_tokens": 18}
    }), encoding="utf-8")
    repo = RecordingRepository()
    repo.analysis_context = AnalysisContext(daily_tokens_before=99000, daily_token_limit=100000)
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_context",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": "file:///raw/2026/04/28/trace_context/request_body.bin",
        "response_raw_ref": "file:///raw/2026/04/28/trace_context/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 2000,
        "token_fingerprint": "tkfp_raw",
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(line, FilesystemEvidenceStore(tmp_path), repo)

    assert repo.context_requests == ["trace_context"]
    assert response["anomaly_count"] == 2
    assert [alert.anomaly_type for alert in repo.anomalies] == [
        "daily_token_limit_exceeded",
        "off_hours_high_usage",
    ]
    assert repo.anomalies[0].observed_value == 101000


def test_process_job_line_handles_malformed_timestamp_with_fallback_anomaly_window(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_bad_time"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text("{}", encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text("{}", encoding="utf-8")
    repo = RecordingRepository()
    repo.analysis_context = AnalysisContext(daily_tokens_before=100000)
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_bad_time",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": "file:///raw/2026/04/28/trace_bad_time/request_body.bin",
        "response_raw_ref": "file:///raw/2026/04/28/trace_bad_time/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 1,
        "token_fingerprint": "tkfp_raw",
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "not-a-timestamp",
    })

    response = process_job_line(line, FilesystemEvidenceStore(tmp_path), repo)

    assert response["worker_status"] == "processed"
    assert [aggregate.bucket_start for aggregate in repo.aggregates] == [
        "1970-01-01T00:00:00+00:00",
        "1970-01-01T00:00:00+00:00",
    ]
    daily_alerts = [
        alert for alert in repo.anomalies if alert.anomaly_type == "daily_token_limit_exceeded"
    ]
    assert len(daily_alerts) == 1
    assert daily_alerts[0].window_start == "1970-01-01T00:00:00+00:00"
    assert daily_alerts[0].window_end == "1970-01-01T00:01:00+00:00"


def test_process_job_line_detects_non_work_personal_use_high_cost(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_personal"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text(json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "Write a funny birthday party toast for my friend."}]
    }), encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text(json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "Here is a toast."}}],
        "usage": {"total_tokens": 25000}
    }), encoding="utf-8")
    repo = RecordingRepository()
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_personal",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": "file:///raw/2026/04/28/trace_personal/request_body.bin",
        "response_raw_ref": "file:///raw/2026/04/28/trace_personal/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 25000,
        "token_fingerprint": "tkfp_raw",
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(line, FilesystemEvidenceStore(tmp_path), repo, RecordingContextRepository())

    assert response["anomaly_count"] == 4
    assert [alert.anomaly_type for alert in repo.anomalies] == [
        "high_trace_tokens",
        "short_window_token_spike",
        "off_hours_high_usage",
        "non_work_personal_use",
    ]


def test_process_job_line_persists_low_token_non_work_alert(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_low_personal"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text(json.dumps({
        "model": "gpt-4.1",
        "messages": [{"role": "user", "content": "Write a birthday toast for my friend."}]
    }), encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text(json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "Here is a toast."}}],
        "usage": {"total_tokens": 120}
    }), encoding="utf-8")
    repo = RecordingRepository()
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_low_personal",
        "route_pattern": "/v1/chat/completions",
        "protocol_family": "openai_chat",
        "capture_mode": "raw_and_normalized",
        "username": "alice",
        "request_raw_ref": "file:///raw/2026/04/28/trace_low_personal/request_body.bin",
        "response_raw_ref": "file:///raw/2026/04/28/trace_low_personal/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 120,
        "token_fingerprint": "tkfp_raw",
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T02:45:22Z",
    })

    response = process_job_line(line, FilesystemEvidenceStore(tmp_path), repo, RecordingContextRepository())

    assert response["anomaly_count"] == 1
    work_results = [r for r in repo.results if r.category == "work_relevance"]
    assert work_results[0].result["decision"] == "non_work_related"
    assert work_results[0].result["recommended_action"] == "alert_non_work"
    assert repo.anomalies[0].anomaly_type == "non_work_personal_use"


def test_contract_example_processes_from_stdin_without_services(monkeypatch):
    worker_dir = Path(__file__).parents[1]
    monkeypatch.delenv("EVIDENCE_STORAGE_DIR", raising=False)
    monkeypatch.delenv("POSTGRES_DSN", raising=False)

    completed = subprocess.run(
        [sys.executable, "main.py"],
        cwd=worker_dir,
        input=(worker_dir / "contract_example.json").read_text(encoding="utf-8"),
        text=True,
        capture_output=True,
        check=False,
    )

    assert completed.returncode == 0, completed.stderr
    response = json.loads(completed.stdout)
    assert response["worker_status"] == "processed"
    assert response["work_relevance_count"] == 1
    assert response["analysis_result_count"] == 2
    assert response["anomaly_count"] == 0
    assert response["coverage_alert_count"] == 0


def test_modified_contract_trace_id_without_services_requires_config(monkeypatch):
    worker_dir = Path(__file__).parents[1]
    monkeypatch.delenv("EVIDENCE_STORAGE_DIR", raising=False)
    monkeypatch.delenv("POSTGRES_DSN", raising=False)
    modified_contract = json.loads((worker_dir / "contract_example.json").read_text(encoding="utf-8"))
    modified_contract["username"] = "bob"

    completed = subprocess.run(
        [sys.executable, "main.py"],
        cwd=worker_dir,
        input=json.dumps(modified_contract),
        text=True,
        capture_output=True,
        check=False,
    )

    assert completed.returncode != 0
    assert "config" in completed.stderr.lower()


def test_arbitrary_stdin_without_services_requires_config(monkeypatch):
    worker_dir = Path(__file__).parents[1]
    monkeypatch.delenv("EVIDENCE_STORAGE_DIR", raising=False)
    monkeypatch.delenv("POSTGRES_DSN", raising=False)

    completed = subprocess.run(
        [sys.executable, "main.py"],
        cwd=worker_dir,
        input=json.dumps({
            "type": "trace_captured",
            "trace_id": "trace_prod",
            "route_pattern": "/v1/chat/completions",
            "protocol_family": "openai_chat",
            "capture_mode": "raw_only",
            "username": "alice",
        }),
        text=True,
        capture_output=True,
        check=False,
    )

    assert completed.returncode != 0
    assert "config" in completed.stderr.lower()


def test_process_redis_once_records_idle_heartbeat(monkeypatch):
    from main import process_redis_once

    class FakeRedisClient:
        def blpop(self, list_name, timeout):
            assert list_name == "analysis_jobs"
            assert timeout == 1
            return None

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            assert url == "redis://user:secret@localhost:6379/0"
            assert decode_responses is True
            return FakeRedisClient()

    class FakeHeartbeatRepository:
        calls = []

        def __init__(self, connection):
            self.connection = connection

        def record(self, **kwargs):
            self.calls.append(kwargs)

    class FakeConnection:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    exit_code = process_redis_once(
        "redis://user:secret@localhost:6379/0",
        "analysis_jobs",
        FilesystemEvidenceStore("/tmp/evidence-unused"),
        "postgres://unused",
        1,
        connection_factory=lambda dsn: FakeConnection(),
    )

    assert exit_code == 0
    assert FakeHeartbeatRepository.calls[0]["worker_id"] == "worker-test"
    assert FakeHeartbeatRepository.calls[0]["status"] == "idle"
    assert FakeHeartbeatRepository.calls[0]["queue_name"] == "analysis_jobs"
    assert FakeHeartbeatRepository.calls[0]["metadata"] == {"poll_result": "idle"}


def test_process_redis_once_records_degraded_llm_metadata_in_heartbeat(monkeypatch):
    from main import process_redis_once

    class FakeRedisClient:
        def blpop(self, list_name, timeout):
            return (list_name, "{\"trace_id\":\"trace-conflict\"}")

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return FakeRedisClient()

    class FakeHeartbeatRepository:
        calls = []

        def __init__(self, connection):
            self.connection = connection

        def record(self, **kwargs):
            self.calls.append(kwargs)

    class FakeConnection:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

    sentinel_judge = object()

    def fake_process_job_line(*args, **kwargs):
        assert kwargs["llm_judge"] is sentinel_judge
        return {
            "accepted_trace_id": "trace-conflict",
            "worker_status": "processed",
            "llm_judge_status": "degraded",
            "llm_judge_error_type": "timeout",
            "llm_judge_fallback_count": 1,
        }

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.process_job_line", fake_process_job_line)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    exit_code = process_redis_once(
        "redis://localhost:6379/0",
        "analysis_jobs",
        FilesystemEvidenceStore("/tmp/evidence-unused"),
        "postgres://unused",
        1,
        connection_factory=lambda dsn: FakeConnection(),
        llm_judge=sentinel_judge,
    )

    assert exit_code == 0
    assert FakeHeartbeatRepository.calls[0]["status"] == "processed"
    assert FakeHeartbeatRepository.calls[0]["metadata"] == {
        "trace_id": "trace-conflict",
        "llm_judge_status": "degraded",
        "llm_judge_error_type": "timeout",
        "llm_judge_fallback_count": 1,
    }


def test_process_redis_loop_records_degraded_llm_metadata_in_heartbeat(monkeypatch):
    from main import process_redis_loop

    handlers = {}

    class FakeRedisClient:
        def __init__(self):
            self.calls = 0

        def blpop(self, list_name, timeout):
            self.calls += 1
            if self.calls == 1:
                return (list_name, "{\"trace_id\":\"trace-conflict\"}")
            handlers["SIGTERM"](None, None)
            return None

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return FakeRedisClient()

    class FakeHeartbeatRepository:
        calls = []

        def __init__(self, connection):
            self.connection = connection

        def record(self, **kwargs):
            self.calls.append(kwargs)

    class FakeConnection:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

    def fake_signal(sig, handler):
        handlers[sig.name] = handler

    sentinel_judge = object()

    def fake_process_job_line(*args, **kwargs):
        assert kwargs["llm_judge"] is sentinel_judge
        return {
            "accepted_trace_id": "trace-conflict",
            "worker_status": "processed",
            "llm_judge_status": "degraded",
            "llm_judge_error_type": "timeout",
            "llm_judge_fallback_count": 1,
        }

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.psycopg.connect", lambda dsn: FakeConnection())
    monkeypatch.setattr("main.process_job_line", fake_process_job_line)
    monkeypatch.setattr("main.signal.signal", fake_signal)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    exit_code = process_redis_loop(
        "redis://localhost:6379/0",
        "analysis_jobs",
        FilesystemEvidenceStore("/tmp/evidence-unused"),
        "postgres://unused",
        1,
        llm_judge=sentinel_judge,
    )

    assert exit_code == 0
    assert FakeHeartbeatRepository.calls[0]["status"] == "processed"
    assert FakeHeartbeatRepository.calls[0]["metadata"] == {
        "trace_id": "trace-conflict",
        "llm_judge_status": "degraded",
        "llm_judge_error_type": "timeout",
        "llm_judge_fallback_count": 1,
    }


def test_process_redis_once_records_error_heartbeat_without_exception_message(monkeypatch):
    from main import process_redis_once

    class FakeRedisClient:
        def blpop(self, list_name, timeout):
            return (list_name, "{\"trace_id\":\"trace-secret\"}")

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return FakeRedisClient()

    class FakeHeartbeatRepository:
        calls = []

        def __init__(self, connection):
            self.connection = connection

        def record(self, **kwargs):
            self.calls.append(kwargs)

    class FakeConnection:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

    def fail_process_job_line(*args, **kwargs):
        raise RuntimeError("secret evidence ref raw/2026/04/28/trace-secret/request_body.bin")

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.process_job_line", fail_process_job_line)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    try:
        process_redis_once(
            "redis://localhost:6379/0",
            "analysis_jobs",
            FilesystemEvidenceStore("/tmp/evidence-unused"),
            "postgres://unused",
            1,
            connection_factory=lambda dsn: FakeConnection(),
        )
    except RuntimeError:
        pass
    else:
        raise AssertionError("expected RuntimeError")

    assert FakeHeartbeatRepository.calls[0]["worker_id"] == "worker-test"
    assert FakeHeartbeatRepository.calls[0]["status"] == "error"
    assert FakeHeartbeatRepository.calls[0]["error_count"] == 1
    assert FakeHeartbeatRepository.calls[0]["metadata"] == {"error_type": "RuntimeError"}


def test_process_redis_once_preserves_job_error_when_error_heartbeat_fails(monkeypatch):
    from main import process_redis_once

    class FakeRedisClient:
        def blpop(self, list_name, timeout):
            return (list_name, "{\"trace_id\":\"trace-secret\"}")

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return FakeRedisClient()

    class FakeHeartbeatRepository:
        def __init__(self, connection):
            self.connection = connection

        def record(self, **kwargs):
            self.connection.events.append("record")
            raise RuntimeError("heartbeat failure")

    class FakeConnection:
        def __init__(self):
            self.events = []

        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

        def rollback(self):
            self.events.append("rollback")

    connection = FakeConnection()

    def fail_process_job_line(*args, **kwargs):
        raise ValueError("original failure")

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.process_job_line", fail_process_job_line)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    try:
        process_redis_once(
            "redis://localhost:6379/0",
            "analysis_jobs",
            FilesystemEvidenceStore("/tmp/evidence-unused"),
            "postgres://unused",
            1,
            connection_factory=lambda dsn: connection,
        )
    except ValueError as exc:
        assert str(exc) == "original failure"
    except RuntimeError as exc:
        raise AssertionError(f"heartbeat exception masked original: {exc}") from exc
    else:
        raise AssertionError("expected ValueError")

    assert connection.events == ["rollback", "record"]


def test_main_passes_created_llm_judge_to_process_redis_once(monkeypatch):
    from main import main

    sentinel_judge = object()
    calls = {}

    class FakeArgs:
        offline_batch = False
        redis_once = True
        redis_url = "redis://localhost:6379/0"
        redis_list = "analysis_jobs"
        redis_timeout_seconds = 7
        postgres_dsn = "postgres://db"

    monkeypatch.setattr("main.argparse.ArgumentParser.parse_args", lambda self: FakeArgs())
    monkeypatch.setattr("main.create_evidence_store", lambda: "evidence-store")
    monkeypatch.setattr("main.create_llm_judge_from_env", lambda: sentinel_judge)
    monkeypatch.setattr("main.process_contract_stdin", lambda: (_ for _ in ()).throw(AssertionError("unexpected contract mode")))

    def fake_process_redis_once(redis_url, redis_list, evidence_store, postgres_dsn, timeout_seconds, **kwargs):
        calls.update({
            "redis_url": redis_url,
            "redis_list": redis_list,
            "evidence_store": evidence_store,
            "postgres_dsn": postgres_dsn,
            "timeout_seconds": timeout_seconds,
            "llm_judge": kwargs["llm_judge"],
        })
        return 0

    monkeypatch.setattr("main.process_redis_once", fake_process_redis_once)
    monkeypatch.setenv("EVIDENCE_STORAGE_BACKEND", "filesystem")
    monkeypatch.setenv("EVIDENCE_STORAGE_DIR", "/tmp/evidence-unused")

    assert main() == 0
    assert calls["llm_judge"] is sentinel_judge
    assert calls["redis_url"] == "redis://localhost:6379/0"
    assert calls["redis_list"] == "analysis_jobs"
    assert calls["evidence_store"] == "evidence-store"
    assert calls["postgres_dsn"] == "postgres://db"
    assert calls["timeout_seconds"] == 7


def test_main_passes_created_llm_judge_to_process_redis_loop(monkeypatch):
    from main import main

    sentinel_judge = object()
    calls = {}

    class FakeArgs:
        offline_batch = False
        redis_once = False
        redis_url = "redis://localhost:6379/0"
        redis_list = "analysis_jobs"
        redis_timeout_seconds = 9
        postgres_dsn = "postgres://db"

    monkeypatch.setattr("main.argparse.ArgumentParser.parse_args", lambda self: FakeArgs())
    monkeypatch.setattr("main.create_evidence_store", lambda: "evidence-store")
    monkeypatch.setattr("main.create_llm_judge_from_env", lambda: sentinel_judge)
    monkeypatch.setattr("main.process_contract_stdin", lambda: (_ for _ in ()).throw(AssertionError("unexpected contract mode")))

    def fake_process_redis_loop(redis_url, redis_list, evidence_store, postgres_dsn, timeout_seconds, **kwargs):
        calls.update({
            "redis_url": redis_url,
            "redis_list": redis_list,
            "evidence_store": evidence_store,
            "postgres_dsn": postgres_dsn,
            "timeout_seconds": timeout_seconds,
            "llm_judge": kwargs["llm_judge"],
        })
        return 0

    monkeypatch.setattr("main.process_redis_loop", fake_process_redis_loop)
    monkeypatch.setenv("EVIDENCE_STORAGE_BACKEND", "filesystem")
    monkeypatch.setenv("EVIDENCE_STORAGE_DIR", "/tmp/evidence-unused")

    assert main() == 0
    assert calls["llm_judge"] is sentinel_judge
    assert calls["redis_url"] == "redis://localhost:6379/0"
    assert calls["redis_list"] == "analysis_jobs"
    assert calls["evidence_store"] == "evidence-store"
    assert calls["postgres_dsn"] == "postgres://db"
    assert calls["timeout_seconds"] == 9
