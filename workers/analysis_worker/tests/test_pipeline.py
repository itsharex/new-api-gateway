import json
import math
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

import pytest

from evidence import FilesystemEvidenceStore
from llm_judge import LLMJudgeClient
from llm_judge import LLMJudgeUnavailable
from main import create_llm_judge_from_env, llm_judge_metadata, process_job_line
from models import AnalysisContext, ContextCatalogEntry
from work_relevance import ANALYZER_VERSION


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
    assert work_result.analyzer_version == ANALYZER_VERSION
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
    assert not any(
        alert.anomaly_type in {
            "low_work_relevance_high_cost",
            "non_work_high_risk",
            "non_work_job_search",
            "non_work_personal_use",
            "non_work_side_business",
            "identity_unresolved_success",
            "daily_token_limit_exceeded",
            "short_window_token_spike",
            "unknown_high_cost",
            "work_nonwork_conflict",
        }
        for alert in repo.anomalies
    )


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
    assert repo.anomalies == []
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


def test_create_llm_judge_from_env_allows_missing_api_key(monkeypatch):
    monkeypatch.setenv("LLM_JUDGE_BASE_URL", "https://judge.example.com/")
    monkeypatch.setenv("LLM_JUDGE_MODEL", "judge-model")
    monkeypatch.delenv("LLM_JUDGE_API_KEY", raising=False)
    monkeypatch.delenv("LLM_JUDGE_TIMEOUT_SECONDS", raising=False)

    client = create_llm_judge_from_env()

    assert isinstance(client, LLMJudgeClient)
    assert client.api_key is None
    assert client.timeout_seconds == 20.0


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
    assert response["anomaly_count"] == 0
    assert response["coverage_alert_count"] == 1
    assert [alert.anomaly_type for alert in repo.anomalies] == []
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
    repo.analysis_context = AnalysisContext(trace_effective_tokens_p95=30000.0)
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
        "usage_prompt_tokens": 30000,
        "usage_completion_tokens": 12000,
        "usage_total_tokens": 42000,
        "token_fingerprint": "tkfp_raw",
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(line, FilesystemEvidenceStore(tmp_path), repo)

    assert repo.context_requests == ["trace_context"]
    assert response["anomaly_count"] == 0
    assert repo.anomalies == []


def test_process_job_line_handles_malformed_timestamp_with_fallback_anomaly_window(tmp_path: Path):
    evidence_dir = tmp_path / "raw" / "2026" / "04" / "28" / "trace_bad_time"
    evidence_dir.mkdir(parents=True)
    (evidence_dir / "request_body.bin").write_text("{}", encoding="utf-8")
    (evidence_dir / "response_body.bin").write_text("{}", encoding="utf-8")
    repo = RecordingRepository()
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
        "usage_prompt_tokens": 30000,
        "usage_completion_tokens": 12000,
        "usage_total_tokens": 42000,
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
    assert [alert.anomaly_type for alert in repo.anomalies] == ["high_trace_tokens"]
    assert repo.anomalies[0].window_start == "1970-01-01T00:00:00+00:00"
    assert repo.anomalies[0].window_end == "1970-01-01T00:01:00+00:00"


def test_process_job_line_detects_non_work_use_high_cost(tmp_path: Path):
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

    assert response["anomaly_count"] == 1
    assert [alert.anomaly_type for alert in repo.anomalies] == [
        "non_work_use",
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
    assert repo.anomalies[0].anomaly_type == "non_work_use"


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

    client = object()

    class FakeRedisClient:
        pass

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            assert url == "redis://user:secret@localhost:6379/0"
            assert decode_responses is True
            return client

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

    def fake_run_core_once(redis_client, connection, stage_processor, worker_id, **kwargs):
        assert redis_client is client
        assert worker_id == "worker-test"
        assert kwargs["stream_name"] == "analysis.core"
        return None

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.run_core_once", fake_run_core_once)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    exit_code = process_redis_once(
        "redis://user:secret@localhost:6379/0",
        "analysis.core",
        FilesystemEvidenceStore("/tmp/evidence-unused"),
        "postgres://unused",
        1,
        connection_factory=lambda dsn: FakeConnection(),
    )

    assert exit_code == 0
    assert FakeHeartbeatRepository.calls[0]["worker_id"] == "worker-test"
    assert FakeHeartbeatRepository.calls[0]["status"] == "idle"
    assert FakeHeartbeatRepository.calls[0]["queue_name"] == "analysis.core"
    assert FakeHeartbeatRepository.calls[0]["metadata"] == {"poll_result": "idle"}


def test_process_redis_once_records_deferred_heartbeat_when_message_is_left_pending(monkeypatch):
    from main import process_redis_once

    client = object()

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return client

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

    def fake_run_core_once(redis_client, connection, stage_processor, worker_id, **kwargs):
        return {
            "worker_status": "deferred",
            "poll_result": "active_lease",
            "trace_id": "trace-deferred",
        }

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.run_core_once", fake_run_core_once)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    exit_code = process_redis_once(
        "redis://localhost:6379/0",
        "analysis.core",
        FilesystemEvidenceStore("/tmp/evidence-unused"),
        "postgres://unused",
        1,
        connection_factory=lambda dsn: FakeConnection(),
    )

    assert exit_code == 0
    assert FakeHeartbeatRepository.calls[0]["status"] == "deferred"
    assert FakeHeartbeatRepository.calls[0]["queue_name"] == "analysis.core"
    assert FakeHeartbeatRepository.calls[0]["metadata"] == {
        "poll_result": "active_lease",
        "trace_id": "trace-deferred",
    }


def test_process_redis_once_uses_enrichment_processor_and_group(monkeypatch):
    from main import process_redis_once

    client = object()
    calls = {}

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return client

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

    def fake_run_core_once(redis_client, connection, stage_processor, worker_id, **kwargs):
        calls["processor_type"] = type(stage_processor).__name__
        calls["group_name"] = kwargs["group_name"]
        calls["stream_name"] = kwargs["stream_name"]
        return None

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.run_core_once", fake_run_core_once)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    exit_code = process_redis_once(
        "redis://localhost:6379/0",
        "analysis.enrichment",
        FilesystemEvidenceStore("/tmp/evidence-unused"),
        "postgres://unused",
        1,
        connection_factory=lambda dsn: FakeConnection(),
    )

    assert exit_code == 0
    assert calls == {
        "processor_type": "EnrichmentStageProcessor",
        "group_name": "analysis-enrichment-workers",
        "stream_name": "analysis.enrichment",
    }


def test_process_redis_once_records_degraded_llm_metadata_in_heartbeat(monkeypatch):
    from main import process_redis_once

    client = object()

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return client

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

    def fake_run_core_once(redis_client, connection, stage_processor, worker_id, **kwargs):
        assert redis_client is client
        assert stage_processor.llm_judge is sentinel_judge
        assert stage_processor.redis_client is client
        return {
            "accepted_trace_id": "trace-conflict",
            "worker_status": "processed",
            "llm_judge_status": "degraded",
            "llm_judge_error_type": "timeout",
            "llm_judge_fallback_count": 1,
        }

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.run_core_once", fake_run_core_once)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    exit_code = process_redis_once(
        "redis://localhost:6379/0",
        "analysis.core",
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
    client = object()

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return client

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

    class FakePool:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

        def connection(self):
            return FakeConnection()

    def fake_signal(sig, handler):
        handlers[sig.name] = handler

    sentinel_judge = object()
    calls = {"count": 0}

    def fake_run_core_batch_once(**kwargs):
        calls["count"] += 1
        assert kwargs["redis_client"] is client
        assert kwargs["llm_judge"] is sentinel_judge
        handlers["SIGTERM"](None, None)
        return [{
            "accepted_trace_id": "trace-conflict",
            "worker_status": "processed",
            "llm_judge_status": "degraded",
            "llm_judge_error_type": "timeout",
            "llm_judge_fallback_count": 1,
        }]

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.ConnectionPool", lambda *args, **kwargs: FakePool())
    monkeypatch.setattr("main.run_core_batch_once", fake_run_core_batch_once)
    monkeypatch.setattr("main.signal.signal", fake_signal)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    exit_code = process_redis_loop(
        "redis://localhost:6379/0",
        "analysis.core",
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


def test_process_redis_loop_records_error_heartbeat_when_core_batch_all_fail(monkeypatch):
    from main import process_redis_loop

    handlers = {}
    client = object()

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return client

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

    class FakePool:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

        def connection(self):
            return FakeConnection()

    def fake_signal(sig, handler):
        handlers[sig.name] = handler

    def fake_run_core_batch_once(**kwargs):
        handlers["SIGTERM"](None, None)
        return [
            {"worker_status": "error", "trace_id": "trace-a", "error_type": "RuntimeError"},
            {"worker_status": "error", "trace_id": "trace-b", "error_type": "ValueError"},
        ]

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.ConnectionPool", lambda *args, **kwargs: FakePool())
    monkeypatch.setattr("main.run_core_batch_once", fake_run_core_batch_once)
    monkeypatch.setattr("main.signal.signal", fake_signal)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    exit_code = process_redis_loop(
        "redis://localhost:6379/0",
        "analysis.core",
        FilesystemEvidenceStore("/tmp/evidence-unused"),
        "postgres://unused",
        1,
    )

    assert exit_code == 0
    assert FakeHeartbeatRepository.calls[0]["status"] == "error"
    assert FakeHeartbeatRepository.calls[0]["error_count"] == 2
    assert FakeHeartbeatRepository.calls[0]["metadata"] == {
        "error_type": "ValueError",
        "trace_id": "trace-b",
    }


def test_process_redis_loop_uses_enrichment_batch_config_and_llm_concurrency_cap(monkeypatch):
    from main import process_redis_loop

    handlers = {}
    client = object()
    calls = {}

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return client

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

    class FakePool:
        def __init__(self, *args, **kwargs):
            calls["pool_kwargs"] = kwargs

        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

        def connection(self):
            return FakeConnection()

    def fake_signal(sig, handler):
        handlers[sig.name] = handler

    sentinel_judge = object()

    def fake_run_core_batch_once(**kwargs):
        calls["batch_kwargs"] = kwargs
        handlers["SIGTERM"](None, None)
        return [{
            "accepted_trace_id": "trace-enrichment",
            "worker_status": "processed",
        }]

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.ConnectionPool", FakePool)
    monkeypatch.setattr("main.run_core_batch_once", fake_run_core_batch_once)
    monkeypatch.setattr("main.signal.signal", fake_signal)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")
    monkeypatch.setenv("ANALYSIS_ENRICHMENT_READ_COUNT", "7")
    monkeypatch.setenv("ANALYSIS_ENRICHMENT_MAX_INFLIGHT", "5")
    monkeypatch.setenv("ANALYSIS_ENRICHMENT_LEASE_SECONDS", "123")
    monkeypatch.setenv("ANALYSIS_ENRICHMENT_RETRY_LIMIT", "9")
    monkeypatch.setenv("ANALYSIS_ENRICHMENT_LLM_MAX_CONCURRENCY", "3")

    exit_code = process_redis_loop(
        "redis://localhost:6379/0",
        "analysis.enrichment",
        FilesystemEvidenceStore("/tmp/evidence-unused"),
        "postgres://unused",
        1,
        llm_judge=sentinel_judge,
    )

    assert exit_code == 0
    assert calls["pool_kwargs"]["min_size"] == 1
    assert calls["pool_kwargs"]["max_size"] == 3
    assert calls["batch_kwargs"]["redis_client"] is client
    assert calls["batch_kwargs"]["llm_judge"] is sentinel_judge
    assert calls["batch_kwargs"]["stream_name"] == "analysis.enrichment"
    assert calls["batch_kwargs"]["group_name"] == "analysis-enrichment-workers"
    assert calls["batch_kwargs"]["read_count"] == 7
    assert calls["batch_kwargs"]["max_inflight"] == 3
    assert calls["batch_kwargs"]["lease_seconds"] == 123
    assert calls["batch_kwargs"]["max_attempts"] == 9
    assert FakeHeartbeatRepository.calls[0]["status"] == "processed"


def test_enrichment_stage_skips_secondary_result_without_llm_request(monkeypatch):
    from enrichment_stage import EnrichmentStageProcessor

    class FakeCursor:
        def __init__(self):
            self.executed = []
            self._next_row = None

        def execute(self, query, params):
            self.executed.append((query, params))
            normalized = " ".join(query.split())
            if "FROM analysis_results" in normalized:
                self._next_row = (False,)
                return
            if "FROM traces" in normalized and "WHERE trace_id = %s" in normalized:
                self._next_row = (
                    "trace_media_only",
                    "/v1/chat/completions",
                    "openai_chat",
                    "raw_and_normalized",
                    "alice",
                    "",
                    "",
                    "",
                    "",
                    "gpt-5.4",
                    0,
                    0,
                    0,
                    0,
                    0,
                    "fp",
                    "fp",
                    1,
                    "alice",
                    "resolved",
                    "",
                    "",
                    200,
                    200,
                    False,
                    "2026-06-04T09:00:00+00:00",
                    0,
                    0,
                )
                return
            if normalized.startswith("UPDATE traces SET enrichment_status = 'completed'"):
                self._next_row = None
                return
            raise AssertionError(f"unexpected query: {normalized}")

        def fetchone(self):
            return self._next_row

        def fetchall(self):
            return []

    class FakeConnection:
        def __init__(self):
            self.cursor_obj = FakeCursor()
            self.commit_calls = 0

        def cursor(self):
            return self.cursor_obj

        def commit(self):
            self.commit_calls += 1

    connection = FakeConnection()
    processor = EnrichmentStageProcessor(
        connection=connection,
        evidence_store=FilesystemEvidenceStore("/tmp/evidence-unused"),
        llm_judge=None,
    )

    result = processor.process("trace_media_only")

    assert result == {
        "accepted_trace_id": "trace_media_only",
        "worker_status": "processed",
        "analysis_result_count": 0,
        "media_assets_extracted": 0,
    }
    queries = "\n".join(query for query, _ in connection.cursor_obj.executed)
    assert "INSERT INTO analysis_results" not in queries


def test_enrichment_stage_skips_secondary_result_when_llm_is_not_actually_used(monkeypatch):
    from enrichment_stage import EnrichmentStageProcessor

    class FakeCursor:
        def __init__(self):
            self.executed = []
            self._next_row = None

        def execute(self, query, params=None):
            self.executed.append((query, params))
            normalized = " ".join(query.split())
            if "FROM analysis_results" in normalized:
                self._next_row = (True,)
                return
            if "FROM context_catalog" in normalized:
                self._next_row = None
                return
            if "FROM traces" in normalized and "WHERE trace_id = %s" in normalized:
                self._next_row = (
                    "trace_needs_judge",
                    "/v1/chat/completions",
                    "openai_chat",
                    "raw_and_normalized",
                    "alice",
                    "",
                    "",
                    "",
                    "",
                    "gpt-5.4",
                    0,
                    0,
                    0,
                    0,
                    0,
                    "fp",
                    "fp",
                    1,
                    "alice",
                    "resolved",
                    "",
                    "",
                    200,
                    200,
                    False,
                    "2026-06-04T09:00:00+00:00",
                    0,
                    0,
                )
                return
            if normalized.startswith("UPDATE traces SET enrichment_status = 'completed'"):
                self._next_row = None
                return
            raise AssertionError(f"unexpected query: {normalized}")

        def fetchone(self):
            return self._next_row

        def fetchall(self):
            return []

    class FakeConnection:
        def __init__(self):
            self.cursor_obj = FakeCursor()

        def cursor(self):
            return self.cursor_obj

        def commit(self):
            return None

    class FakeEvidenceStore:
        def read_text(self, _ref):
            return ""

    def fake_normalize_json_trace(*args, **kwargs):
        return [], []

    def fake_classify_work_relevance(*args, **kwargs):
        return type("Assessment", (), {
            "evidence": [{"source": "keyword", "kind": "work_task"}],
        })()

    monkeypatch.setattr("enrichment_stage.normalize_json_trace", fake_normalize_json_trace)
    monkeypatch.setattr("enrichment_stage.classify_work_relevance", fake_classify_work_relevance)

    connection = FakeConnection()
    processor = EnrichmentStageProcessor(
        connection=connection,
        evidence_store=FakeEvidenceStore(),
        llm_judge=object(),
    )

    result = processor.process("trace_needs_judge")

    assert result == {
        "accepted_trace_id": "trace_needs_judge",
        "worker_status": "processed",
        "analysis_result_count": 0,
        "media_assets_extracted": 0,
    }
    queries = "\n".join(query for query, _ in connection.cursor_obj.executed)
    assert "INSERT INTO analysis_results" not in queries


def test_process_redis_once_records_error_heartbeat_without_exception_message(monkeypatch):
    from main import process_redis_once

    client = object()

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return client

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

    def fail_run_core_once(*args, **kwargs):
        raise RuntimeError("secret evidence ref raw/2026/04/28/trace-secret/request_body.bin")

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.run_core_once", fail_run_core_once)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    try:
        process_redis_once(
            "redis://localhost:6379/0",
            "analysis.core",
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

    client = object()

    class FakeRedisModule:
        @staticmethod
        def from_url(url, decode_responses):
            return client

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

    def fail_run_core_once(*args, **kwargs):
        raise ValueError("original failure")

    monkeypatch.setattr("main.redis.Redis", FakeRedisModule)
    monkeypatch.setattr("main.HeartbeatRepository", FakeHeartbeatRepository)
    monkeypatch.setattr("main.run_core_once", fail_run_core_once)
    monkeypatch.setenv("ANALYSIS_WORKER_ID", "worker-test")

    try:
        process_redis_once(
            "redis://localhost:6379/0",
            "analysis.core",
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
        redis_list = "analysis.core"
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
    assert calls["redis_list"] == "analysis.core"
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
        redis_list = "analysis.core"
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
    assert calls["redis_list"] == "analysis.core"
    assert calls["evidence_store"] == "evidence-store"
    assert calls["postgres_dsn"] == "postgres://db"
    assert calls["timeout_seconds"] == 9


def test_process_core_stream_message_marks_trace_completed_and_acks(monkeypatch):
    from core_stage import CoreStageProcessor
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    class FakeCursor:
        def __init__(self, connection):
            self.connection = connection
            self.executed = []
            self._next_row = None

        def execute(self, query, params):
            self.executed.append((query, params))
            normalized = " ".join(query.split())
            if normalized.startswith("INSERT INTO analysis_tasks"):
                trace_id, stage, max_attempts, stream_name, stream_message_id, queued_at = params
                key = (trace_id, stage)
                existing = self.connection.tasks.get(key)
                if existing is None:
                    self.connection.tasks[key] = {
                        "trace_id": trace_id,
                        "stage": stage,
                        "status": "queued",
                        "attempt_count": 0,
                        "max_attempts": max_attempts,
                        "lease_owner": "",
                        "lease_expires_at": "",
                        "stream_name": stream_name,
                        "stream_message_id": stream_message_id,
                        "queued_at": queued_at,
                        "started_at": "",
                        "completed_at": "",
                        "last_error_code": "",
                        "last_error_message": "",
                        "updated_at": "2026-06-03T10:00:00+00:00",
                    }
                else:
                    existing["stream_name"] = stream_name
                    existing["stream_message_id"] = stream_message_id
                self._next_row = None
                return
            if normalized.startswith("UPDATE analysis_tasks SET status = 'leased'"):
                worker_id, lease_seconds, trace_id, stage = params
                key = (trace_id, stage)
                task = self.connection.tasks.get(key)
                if not task or task["status"] not in {"queued", "failed_retryable"}:
                    self._next_row = None
                    return
                task["status"] = "leased"
                task["attempt_count"] += 1
                task["lease_owner"] = worker_id
                task["lease_expires_at"] = f"+{lease_seconds}s"
                task["started_at"] = task["started_at"] or "2026-06-03T10:00:01+00:00"
                task["updated_at"] = "2026-06-03T10:00:01+00:00"
                self._next_row = (
                    task["trace_id"],
                    task["stage"],
                    task["status"],
                    task["attempt_count"],
                    task["max_attempts"],
                    task["lease_owner"],
                    task["lease_expires_at"],
                    task["stream_name"],
                    task["stream_message_id"],
                    task["queued_at"],
                    task["started_at"],
                    task["completed_at"],
                    task["last_error_code"],
                    task["last_error_message"],
                    task["updated_at"],
                )
                return
            if normalized.startswith("UPDATE analysis_tasks SET status = 'succeeded'"):
                trace_id, stage, worker_id = params
                task = self.connection.tasks[(trace_id, stage)]
                task["status"] = "succeeded"
                task["completed_at"] = "2026-06-03T10:00:02+00:00"
                task["lease_owner"] = ""
                task["lease_expires_at"] = ""
                task["updated_at"] = "2026-06-03T10:00:02+00:00"
                assert worker_id == "worker-1"
                self._next_row = (1,)
                return
            if "FROM traces" in normalized and "WHERE trace_id = %s" in normalized:
                trace = self.connection.traces[params[0]]
                self._next_row = (
                    trace["trace_id"],
                    trace["route_pattern"],
                    trace["protocol_family"],
                    trace["capture_mode"],
                    trace["username_snapshot"],
                    trace["request_raw_ref"],
                    trace["request_headers_ref"],
                    trace["response_raw_ref"],
                    trace["response_headers_ref"],
                    trace["model_requested"],
                    trace["usage_prompt_tokens"],
                    trace["usage_completion_tokens"],
                    trace["usage_total_tokens"],
                    trace["usage_reasoning_tokens"],
                    trace["usage_cached_tokens"],
                    trace["token_fingerprint"],
                    trace["fingerprint_display"],
                    trace["new_api_token_id_snapshot"],
                    trace["token_name_snapshot"],
                    trace["identity_resolution_status"],
                    trace["client_ip_hash"],
                    trace["user_agent_hash"],
                    trace["status_code"],
                    trace["upstream_status_code"],
                    trace["stream"],
                    trace["request_started_at"],
                    trace["request_body_size"],
                    trace["response_body_size"],
                )
                return
            if "FROM media_snapshot_jobs" in normalized:
                trace_id = params[0]
                self._next_row = (1,) if trace_id in self.connection.media_snapshot_jobs else None
                return
            if normalized.startswith("UPDATE traces SET core_status = 'completed'"):
                _error_code, enrichment_required, enrichment_status, _queued_flag, trace_id = params
                trace = self.connection.traces[trace_id]
                trace["core_status"] = "completed"
                trace["enrichment_required"] = enrichment_required
                trace["enrichment_status"] = enrichment_status
                trace["enrichment_queued_at"] = None
                self._next_row = None
                return
            if normalized.startswith("UPDATE traces SET enrichment_required = TRUE,"):
                queued_at, trace_id = params
                trace = self.connection.traces[trace_id]
                trace["enrichment_required"] = True
                trace["enrichment_status"] = "pending"
                trace["enrichment_queued_at"] = queued_at
                self._next_row = None
                return
            self._next_row = None

        def fetchone(self):
            return self._next_row

    class FakeConnection:
        def __init__(self):
            self.tasks = {}
            self.media_snapshot_jobs = {"trace_1"}
            self.traces = {
                "trace_1": {
                    "trace_id": "trace_1",
                    "route_pattern": "/v1/chat/completions",
                    "protocol_family": "openai_chat",
                    "capture_mode": "raw_and_normalized",
                    "username_snapshot": "alice",
                    "request_raw_ref": "file:///tmp/request.bin",
                    "request_headers_ref": "",
                    "response_raw_ref": "file:///tmp/response.bin",
                    "response_headers_ref": "",
                    "model_requested": "gpt-4.1",
                    "usage_prompt_tokens": 11,
                    "usage_completion_tokens": 7,
                    "usage_total_tokens": 18,
                    "usage_reasoning_tokens": 0,
                    "usage_cached_tokens": 0,
                    "token_fingerprint": "tkfp",
                    "fingerprint_display": "tkfp_display",
                    "new_api_token_id_snapshot": 42,
                    "token_name_snapshot": "alice",
                    "identity_resolution_status": "resolved",
                    "client_ip_hash": "",
                    "user_agent_hash": "",
                    "status_code": 200,
                    "upstream_status_code": 200,
                    "stream": False,
                    "request_started_at": "2026-06-03T10:00:00+00:00",
                    "request_body_size": 128,
                    "response_body_size": 256,
                    "core_status": "pending",
                    "enrichment_required": False,
                    "enrichment_status": "not_required",
                    "enrichment_queued_at": None,
                }
            }
            self.cursor_obj = FakeCursor(self)
            self.commit_calls = 0

        def cursor(self):
            return self.cursor_obj

        def commit(self):
            self.commit_calls += 1

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []
            self.kwargs = kwargs

        def read_one(self, count=1, block_ms=5000):
            assert count == 1
            assert block_ms == 5000
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-0",
                "envelope": StreamEnvelope(
                    trace_id="trace_1",
                    stage=AnalysisStage.CORE,
                    enqueued_at="2026-06-03T10:00:00+00:00",
                ),
            })()

        def ack(self, message_id):
            self.acked.append(("analysis.core", message_id))

    consumer = FakeConsumer()
    connection = FakeConnection()
    consumer_factory_calls = {}

    def fake_process_job_line(*args, **kwargs):
        return {"accepted_trace_id": "trace_1", "worker_status": "processed"}

    def fake_consumer_factory(*args, **kwargs):
        consumer_factory_calls["kwargs"] = kwargs
        return consumer

    monkeypatch.setattr("main.StreamConsumer", fake_consumer_factory)
    monkeypatch.setattr("main.publish_stream_message", lambda *args, **kwargs: "1748944471000-9")
    monkeypatch.setattr("core_stage.default_process_job_line", fake_process_job_line)

    processor = CoreStageProcessor(
        connection=connection,
        evidence_store=FilesystemEvidenceStore("/tmp/evidence-unused"),
        redis_client=None,
    )

    result = run_core_once(
        redis_client=object(),
        connection=connection,
        stage_processor=processor,
        worker_id="worker-1",
    )

    assert result["accepted_trace_id"] == "trace_1"
    assert result["worker_status"] == "processed"
    assert result["enrichment_required"] is True
    assert result["enrichment_reasons"] == []
    assert consumer.acked == [("analysis.core", "1748944471000-0")]
    assert consumer_factory_calls["kwargs"]["group_name"] == "analysis-core-workers"
    assert consumer_factory_calls["kwargs"]["reclaim_idle_ms"] == 300000
    task = connection.tasks[("trace_1", "core")]
    assert task["status"] == "succeeded"
    assert task["stream_message_id"] == "1748944471000-0"
    assert task["queued_at"] == "2026-06-03T10:00:00+00:00"
    assert connection.traces["trace_1"]["core_status"] == "completed"
    assert connection.traces["trace_1"]["enrichment_required"] is True
    assert connection.traces["trace_1"]["enrichment_status"] == "pending"
    assert connection.traces["trace_1"]["enrichment_queued_at"] == datetime.fromtimestamp(
        1748944471,
        tz=timezone.utc,
    ).isoformat()


def test_process_enrichment_stream_message_marks_trace_completed_and_acks(monkeypatch):
    from enrichment_stage import EnrichmentStageProcessor
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    class FakeCursor:
        def __init__(self, connection):
            self.connection = connection
            self.executed = []
            self._next_row = None

        def execute(self, query, params):
            self.executed.append((query, params))
            normalized = " ".join(query.split())
            if normalized.startswith("INSERT INTO analysis_tasks"):
                trace_id, stage, max_attempts, stream_name, stream_message_id, queued_at = params
                key = (trace_id, stage)
                existing = self.connection.tasks.get(key)
                if existing is None:
                    self.connection.tasks[key] = {
                        "trace_id": trace_id,
                        "stage": stage,
                        "status": "queued",
                        "attempt_count": 0,
                        "max_attempts": max_attempts,
                        "lease_owner": "",
                        "lease_expires_at": "",
                        "stream_name": stream_name,
                        "stream_message_id": stream_message_id,
                        "queued_at": queued_at or "2026-06-03T10:00:00+00:00",
                        "started_at": "",
                        "completed_at": "",
                        "last_error_code": "",
                        "last_error_message": "",
                        "updated_at": "2026-06-03T10:00:00+00:00",
                    }
                else:
                    existing["stream_name"] = stream_name
                    existing["stream_message_id"] = stream_message_id
                self._next_row = None
                return
            if normalized.startswith("UPDATE analysis_tasks SET status = 'leased'"):
                worker_id, lease_seconds, trace_id, stage = params
                key = (trace_id, stage)
                task = self.connection.tasks.get(key)
                if not task or task["status"] not in {"queued", "failed_retryable"}:
                    self._next_row = None
                    return
                task["status"] = "leased"
                task["attempt_count"] += 1
                task["lease_owner"] = worker_id
                task["lease_expires_at"] = f"+{lease_seconds}s"
                task["started_at"] = task["started_at"] or "2026-06-03T10:00:01+00:00"
                task["updated_at"] = "2026-06-03T10:00:01+00:00"
                self._next_row = (
                    task["trace_id"],
                    task["stage"],
                    task["status"],
                    task["attempt_count"],
                    task["max_attempts"],
                    task["lease_owner"],
                    task["lease_expires_at"],
                    task["stream_name"],
                    task["stream_message_id"],
                    task["queued_at"],
                    task["started_at"],
                    task["completed_at"],
                    task["last_error_code"],
                    task["last_error_message"],
                    task["updated_at"],
                )
                return
            if normalized.startswith("UPDATE analysis_tasks SET status = 'succeeded'"):
                trace_id, stage, worker_id = params
                task = self.connection.tasks[(trace_id, stage)]
                task["status"] = "succeeded"
                task["completed_at"] = "2026-06-03T10:00:03+00:00"
                task["lease_owner"] = ""
                task["lease_expires_at"] = ""
                task["updated_at"] = "2026-06-03T10:00:03+00:00"
                assert worker_id == "worker-1"
                self._next_row = (1,)
                return
            if "FROM traces" in normalized and "WHERE trace_id = %s" in normalized:
                trace = self.connection.traces[params[0]]
                self._next_row = (
                    trace["trace_id"],
                    trace["route_pattern"],
                    trace["protocol_family"],
                    trace["capture_mode"],
                    trace["username_snapshot"],
                    trace["request_raw_ref"],
                    trace["request_headers_ref"],
                    trace["response_raw_ref"],
                    trace["response_headers_ref"],
                    trace["model_requested"],
                    trace["usage_prompt_tokens"],
                    trace["usage_completion_tokens"],
                    trace["usage_total_tokens"],
                    trace["usage_reasoning_tokens"],
                    trace["usage_cached_tokens"],
                    trace["token_fingerprint"],
                    trace["fingerprint_display"],
                    trace["new_api_token_id_snapshot"],
                    trace["token_name_snapshot"],
                    trace["identity_resolution_status"],
                    trace["client_ip_hash"],
                    trace["user_agent_hash"],
                    trace["status_code"],
                    trace["upstream_status_code"],
                    trace["stream"],
                    trace["request_started_at"],
                    trace["request_body_size"],
                    trace["response_body_size"],
                )
                return
            if normalized.startswith("UPDATE traces SET enrichment_status = 'completed'"):
                trace_id = params[0]
                trace = self.connection.traces[trace_id]
                trace["enrichment_status"] = "completed"
                trace["enrichment_completed_at"] = "2026-06-03T10:00:03+00:00"
                self._next_row = None
                return
            self._next_row = None

        def fetchone(self):
            return self._next_row

    class FakeConnection:
        def __init__(self):
            self.tasks = {}
            self.traces = {
                "trace_enrichment": {
                    "trace_id": "trace_enrichment",
                    "route_pattern": "/v1/chat/completions",
                    "protocol_family": "openai_chat",
                    "capture_mode": "raw_and_normalized",
                    "username_snapshot": "alice",
                    "request_raw_ref": "file:///tmp/request.bin",
                    "request_headers_ref": "",
                    "response_raw_ref": "file:///tmp/response.bin",
                    "response_headers_ref": "",
                    "model_requested": "gpt-4.1",
                    "usage_prompt_tokens": 11,
                    "usage_completion_tokens": 7,
                    "usage_total_tokens": 18,
                    "usage_reasoning_tokens": 0,
                    "usage_cached_tokens": 0,
                    "token_fingerprint": "tkfp",
                    "fingerprint_display": "tkfp_display",
                    "new_api_token_id_snapshot": 42,
                    "token_name_snapshot": "alice",
                    "identity_resolution_status": "resolved",
                    "client_ip_hash": "",
                    "user_agent_hash": "",
                    "status_code": 200,
                    "upstream_status_code": 200,
                    "stream": False,
                    "request_started_at": "2026-06-03T10:00:00+00:00",
                    "request_body_size": 128,
                    "response_body_size": 256,
                    "enrichment_status": "pending",
                    "enrichment_completed_at": None,
                }
            }
            self.cursor_obj = FakeCursor(self)

        def cursor(self):
            return self.cursor_obj

        def commit(self):
            return None

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []
            self.kwargs = kwargs

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.enrichment",
                "message_id": "1748944471000-9",
                "envelope": StreamEnvelope(
                    trace_id="trace_enrichment",
                    stage=AnalysisStage.ENRICHMENT,
                    enqueued_at="2026-06-03T10:00:00+00:00",
                ),
            })()

        def ack(self, message_id):
            self.acked.append(("analysis.enrichment", message_id))

    consumer = FakeConsumer()
    connection = FakeConnection()
    consumer_factory_calls = {}

    def fake_consumer_factory(*args, **kwargs):
        consumer_factory_calls["kwargs"] = kwargs
        return consumer

    def fake_process_enrichment(*args, **kwargs):
        return {
            "accepted_trace_id": "trace_enrichment",
            "worker_status": "processed",
            "analysis_result_count": 1,
        }

    monkeypatch.setattr("main.StreamConsumer", fake_consumer_factory)

    processor = EnrichmentStageProcessor(
        connection=connection,
        evidence_store=FilesystemEvidenceStore("/tmp/evidence-unused"),
        redis_client=None,
        process_enrichment_fn=fake_process_enrichment,
    )

    result = run_core_once(
        redis_client=object(),
        connection=connection,
        stage_processor=processor,
        worker_id="worker-1",
        stream_name="analysis.enrichment",
        group_name="analysis-enrichment-workers",
    )

    assert result == {
        "accepted_trace_id": "trace_enrichment",
        "worker_status": "processed",
        "analysis_result_count": 1,
    }
    assert consumer.acked == [("analysis.enrichment", "1748944471000-9")]
    assert consumer_factory_calls["kwargs"]["group_name"] == "analysis-enrichment-workers"
    task = connection.tasks[("trace_enrichment", "enrichment")]
    assert task["status"] == "succeeded"
    assert connection.traces["trace_enrichment"]["enrichment_status"] == "completed"
    assert connection.traces["trace_enrichment"]["enrichment_completed_at"] == "2026-06-03T10:00:03+00:00"


def test_default_process_enrichment_reraises_llm_timeout_without_persisting_secondary_result(monkeypatch):
    from enrichment_stage import default_process_enrichment

    class FakeCursor:
        def __init__(self, connection):
            self.connection = connection
            self._next_row = None

        def execute(self, query, params):
            normalized = " ".join(query.split())
            if "SELECT COALESCE((result_json->>'llm_judge_requested')::boolean, false)" in normalized:
                self._next_row = (True,)
                return
            if "FROM traces" in normalized and "WHERE trace_id = %s" in normalized:
                trace = self.connection.traces[params[0]]
                self._next_row = (
                    trace["trace_id"],
                    trace["route_pattern"],
                    trace["protocol_family"],
                    trace["capture_mode"],
                    trace["username_snapshot"],
                    trace["request_raw_ref"],
                    trace["request_headers_ref"],
                    trace["response_raw_ref"],
                    trace["response_headers_ref"],
                    trace["model_requested"],
                    trace["usage_prompt_tokens"],
                    trace["usage_completion_tokens"],
                    trace["usage_total_tokens"],
                    trace["usage_reasoning_tokens"],
                    trace["usage_cached_tokens"],
                    trace["token_fingerprint"],
                    trace["fingerprint_display"],
                    trace["new_api_token_id_snapshot"],
                    trace["token_name_snapshot"],
                    trace["identity_resolution_status"],
                    trace["client_ip_hash"],
                    trace["user_agent_hash"],
                    trace["status_code"],
                    trace["upstream_status_code"],
                    trace["stream"],
                    trace["request_started_at"],
                    trace["request_body_size"],
                    trace["response_body_size"],
                )
                return
            self._next_row = None

        def fetchone(self):
            return self._next_row

    class FakeConnection:
        def __init__(self):
            self.traces = {
                "trace_enrichment_timeout": {
                    "trace_id": "trace_enrichment_timeout",
                    "route_pattern": "/v1/chat/completions",
                    "protocol_family": "openai_chat",
                    "capture_mode": "raw_and_normalized",
                    "username_snapshot": "alice",
                    "request_raw_ref": "file:///tmp/request.bin",
                    "request_headers_ref": "",
                    "response_raw_ref": "file:///tmp/response.bin",
                    "response_headers_ref": "",
                    "model_requested": "gpt-4.1",
                    "usage_prompt_tokens": 11,
                    "usage_completion_tokens": 7,
                    "usage_total_tokens": 1200,
                    "usage_reasoning_tokens": 0,
                    "usage_cached_tokens": 0,
                    "token_fingerprint": "tkfp",
                    "fingerprint_display": "tkfp_display",
                    "new_api_token_id_snapshot": 42,
                    "token_name_snapshot": "alice",
                    "identity_resolution_status": "resolved",
                    "client_ip_hash": "",
                    "user_agent_hash": "",
                    "status_code": 200,
                    "upstream_status_code": 200,
                    "stream": False,
                    "request_started_at": "2026-06-03T10:00:00+00:00",
                    "request_body_size": 128,
                    "response_body_size": 256,
                }
            }
            self.cursor_obj = FakeCursor(self)

        def cursor(self):
            return self.cursor_obj

    class FakeEvidenceStore:
        def read_text(self, ref):
            if ref.endswith("request.bin"):
                return json.dumps({
                    "model": "gpt-4.1",
                    "messages": [{
                        "role": "user",
                        "content": "In relay, rewrite my resume bullet about debugging this route.",
                    }],
                })
            return json.dumps({
                "choices": [{
                    "message": {
                        "role": "assistant",
                        "content": "I can help tighten the wording.",
                    }
                }],
                "usage": {"total_tokens": 1200},
            })

    class FakeContextRepository:
        def __init__(self, connection):
            self.connection = connection

        def list_active_contexts(self):
            return [ContextCatalogEntry(
                id=1,
                context_type="repo",
                name="new-api-gateway",
                description="Audit gateway",
                keywords=["new-api", "gateway", "audit"],
                aliases=["relay"],
                owner="platform",
                expected_task_categories=["coding", "debugging", "documentation"],
                expected_models=["gpt-4.1"],
                expected_usage_level="normal",
                active=True,
            )]

    saved_results = []

    def capture_save(self, messages, results, aggregates, anomalies=(), coverage_alerts=()):
        saved_results.append({
            "messages": list(messages),
            "results": list(results),
            "aggregates": list(aggregates),
            "anomalies": list(anomalies),
            "coverage_alerts": list(coverage_alerts),
        })

    monkeypatch.setattr("enrichment_stage.PostgresContextRepository", FakeContextRepository)
    monkeypatch.setattr("repository.PostgresAnalysisRepository.save_trace_analysis", capture_save)

    with pytest.raises(LLMJudgeUnavailable, match="judge timed out"):
        default_process_enrichment(
            "trace_enrichment_timeout",
            connection=FakeConnection(),
            evidence_store=FakeEvidenceStore(),
            llm_judge=StubJudge(error=LLMJudgeUnavailable("timeout", "judge timed out")),
        )

    assert saved_results == []


def test_core_stage_marks_enrichment_pending_when_llm_judge_is_degraded():
    from core_stage import CoreStageProcessor

    class FakeCursor:
        def __init__(self, connection):
            self.connection = connection
            self._next_row = None

        def execute(self, query, params):
            normalized = " ".join(query.split())
            if "FROM traces" in normalized and "WHERE trace_id = %s" in normalized:
                trace = self.connection.traces[params[0]]
                self._next_row = (
                    trace["trace_id"],
                    trace["route_pattern"],
                    trace["protocol_family"],
                    trace["capture_mode"],
                    trace["username_snapshot"],
                    trace["request_raw_ref"],
                    trace["request_headers_ref"],
                    trace["response_raw_ref"],
                    trace["response_headers_ref"],
                    trace["model_requested"],
                    trace["usage_prompt_tokens"],
                    trace["usage_completion_tokens"],
                    trace["usage_total_tokens"],
                    trace["usage_reasoning_tokens"],
                    trace["usage_cached_tokens"],
                    trace["token_fingerprint"],
                    trace["fingerprint_display"],
                    trace["new_api_token_id_snapshot"],
                    trace["token_name_snapshot"],
                    trace["identity_resolution_status"],
                    trace["client_ip_hash"],
                    trace["user_agent_hash"],
                    trace["status_code"],
                    trace["upstream_status_code"],
                    trace["stream"],
                    trace["request_started_at"],
                    trace["request_body_size"],
                    trace["response_body_size"],
                )
                return
            if "FROM media_snapshot_jobs" in normalized:
                self._next_row = None
                return
            if "FROM analysis_results" in normalized:
                self._next_row = (1,)
                return
            if normalized.startswith("UPDATE traces SET core_status = 'completed'"):
                _error_code, enrichment_required, enrichment_status, _queued_flag, trace_id = params
                trace = self.connection.traces[trace_id]
                trace["core_status"] = "completed"
                trace["enrichment_required"] = enrichment_required
                trace["enrichment_status"] = enrichment_status
                trace["enrichment_queued_at"] = None
                self._next_row = None
                return
            self._next_row = None

        def fetchone(self):
            return self._next_row

    class FakeConnection:
        def __init__(self):
            self.traces = {
                "trace_degraded": {
                    "trace_id": "trace_degraded",
                    "route_pattern": "/v1/chat/completions",
                    "protocol_family": "openai_chat",
                    "capture_mode": "raw_and_normalized",
                    "username_snapshot": "alice",
                    "request_raw_ref": "file:///tmp/request.bin",
                    "request_headers_ref": "",
                    "response_raw_ref": "file:///tmp/response.bin",
                    "response_headers_ref": "",
                    "model_requested": "gpt-4.1",
                    "usage_prompt_tokens": 11,
                    "usage_completion_tokens": 7,
                    "usage_total_tokens": 18,
                    "usage_reasoning_tokens": 0,
                    "usage_cached_tokens": 0,
                    "token_fingerprint": "tkfp",
                    "fingerprint_display": "tkfp_display",
                    "new_api_token_id_snapshot": 42,
                    "token_name_snapshot": "alice",
                    "identity_resolution_status": "resolved",
                    "client_ip_hash": "",
                    "user_agent_hash": "",
                    "status_code": 200,
                    "upstream_status_code": 200,
                    "stream": False,
                    "request_started_at": "2026-06-03T10:00:00+00:00",
                    "request_body_size": 128,
                    "response_body_size": 256,
                    "core_status": "pending",
                    "enrichment_required": False,
                    "enrichment_status": "not_required",
                    "enrichment_queued_at": None,
                }
            }
            self.cursor_obj = FakeCursor(self)
            self.commit_calls = 0

        def cursor(self):
            return self.cursor_obj

        def commit(self):
            self.commit_calls += 1

    connection = FakeConnection()

    def fake_process_job_line(*args, **kwargs):
        return {
            "accepted_trace_id": "trace_degraded",
            "worker_status": "processed",
            "llm_judge_status": "degraded",
        }

    processor = CoreStageProcessor(
        connection=connection,
        evidence_store=FilesystemEvidenceStore("/tmp/evidence-unused"),
        redis_client=object(),
        process_job_line_fn=fake_process_job_line,
    )

    result = processor.process("trace_degraded")

    assert result["llm_judge_status"] == "degraded"
    assert connection.traces["trace_degraded"]["enrichment_required"] is True
    assert connection.traces["trace_degraded"]["enrichment_status"] == "pending"
    assert connection.traces["trace_degraded"]["enrichment_queued_at"] is None


def test_core_stage_marks_enrichment_pending_when_heuristics_request_llm_review():
    from core_stage import CoreStageProcessor

    class FakeCursor:
        def __init__(self, connection):
            self.connection = connection
            self._next_row = None

        def execute(self, query, params):
            normalized = " ".join(query.split())
            if "FROM traces" in normalized and "WHERE trace_id = %s" in normalized:
                trace = self.connection.traces[params[0]]
                self._next_row = (
                    trace["trace_id"],
                    trace["route_pattern"],
                    trace["protocol_family"],
                    trace["capture_mode"],
                    trace["username_snapshot"],
                    trace["request_raw_ref"],
                    trace["request_headers_ref"],
                    trace["response_raw_ref"],
                    trace["response_headers_ref"],
                    trace["model_requested"],
                    trace["usage_prompt_tokens"],
                    trace["usage_completion_tokens"],
                    trace["usage_total_tokens"],
                    trace["usage_reasoning_tokens"],
                    trace["usage_cached_tokens"],
                    trace["token_fingerprint"],
                    trace["fingerprint_display"],
                    trace["new_api_token_id_snapshot"],
                    trace["token_name_snapshot"],
                    trace["identity_resolution_status"],
                    trace["client_ip_hash"],
                    trace["user_agent_hash"],
                    trace["status_code"],
                    trace["upstream_status_code"],
                    trace["stream"],
                    trace["request_started_at"],
                    trace["request_body_size"],
                    trace["response_body_size"],
                )
                return
            if "FROM media_snapshot_jobs" in normalized:
                self._next_row = None
                return
            if normalized.startswith("UPDATE traces SET core_status = 'completed'"):
                _error_code, enrichment_required, enrichment_status, _queued_flag, trace_id = params
                trace = self.connection.traces[trace_id]
                trace["core_status"] = "completed"
                trace["enrichment_required"] = enrichment_required
                trace["enrichment_status"] = enrichment_status
                trace["enrichment_queued_at"] = None
                self._next_row = None
                return
            self._next_row = None

        def fetchone(self):
            return self._next_row

    class FakeConnection:
        def __init__(self):
            self.traces = {
                "trace_conflict": {
                    "trace_id": "trace_conflict",
                    "route_pattern": "/v1/chat/completions",
                    "protocol_family": "openai_chat",
                    "capture_mode": "raw_and_normalized",
                    "username_snapshot": "alice",
                    "request_raw_ref": "file:///tmp/request.bin",
                    "request_headers_ref": "",
                    "response_raw_ref": "file:///tmp/response.bin",
                    "response_headers_ref": "",
                    "model_requested": "gpt-4.1",
                    "usage_prompt_tokens": 11,
                    "usage_completion_tokens": 7,
                    "usage_total_tokens": 18,
                    "usage_reasoning_tokens": 0,
                    "usage_cached_tokens": 0,
                    "token_fingerprint": "tkfp",
                    "fingerprint_display": "tkfp_display",
                    "new_api_token_id_snapshot": 42,
                    "token_name_snapshot": "alice",
                    "identity_resolution_status": "resolved",
                    "client_ip_hash": "",
                    "user_agent_hash": "",
                    "status_code": 200,
                    "upstream_status_code": 200,
                    "stream": False,
                    "request_started_at": "2026-06-03T10:00:00+00:00",
                    "request_body_size": 128,
                    "response_body_size": 256,
                    "core_status": "pending",
                    "enrichment_required": False,
                    "enrichment_status": "not_required",
                    "enrichment_queued_at": None,
                }
            }
            self.cursor_obj = FakeCursor(self)

        def cursor(self):
            return self.cursor_obj

        def commit(self):
            return None

    connection = FakeConnection()

    def fake_process_job_line(*args, **kwargs):
        assert kwargs["allow_llm"] is False
        return {
            "accepted_trace_id": "trace_conflict",
            "worker_status": "processed",
            "enrichment_required": True,
            "enrichment_reasons": ["llm_judge"],
        }

    processor = CoreStageProcessor(
        connection=connection,
        evidence_store=FilesystemEvidenceStore("/tmp/evidence-unused"),
        redis_client=object(),
        llm_judge=object(),
        process_job_line_fn=fake_process_job_line,
    )

    result = processor.process("trace_conflict")

    assert result["enrichment_required"] is True
    assert connection.traces["trace_conflict"]["enrichment_required"] is True
    assert connection.traces["trace_conflict"]["enrichment_status"] == "pending"
    assert connection.traces["trace_conflict"]["enrichment_queued_at"] is None


def test_run_core_batch_once_processes_multiple_messages(monkeypatch):
    from main import run_core_batch_once
    from models import AnalysisStage, StreamEnvelope

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_batch(self, count=1, block_ms=5000):
            return [
                type("Msg", (), {
                    "stream_name": "analysis.core",
                    "message_id": "1748944471000-0",
                    "envelope": StreamEnvelope(trace_id="trace_1", stage=AnalysisStage.CORE),
                })(),
                type("Msg", (), {
                    "stream_name": "analysis.core",
                    "message_id": "1748944471000-1",
                    "envelope": StreamEnvelope(trace_id="trace_2", stage=AnalysisStage.CORE),
                })(),
            ]

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakePoolConnection:
        def __init__(self, trace_id):
            self.trace_id = trace_id
            self.connection = type("Conn", (), {
                "commit": lambda self: None,
                "rollback": lambda self: None,
            })()

        def __enter__(self):
            return self.connection

        def __exit__(self, exc_type, exc, tb):
            return False

    class FakePool:
        def __init__(self):
            self.calls = 0

        def connection(self):
            self.calls += 1
            return FakePoolConnection(self.calls)

    class FakeStageProcessor:
        def __init__(self, connection, trace_id):
            self.connection = connection
            self.trace_id = trace_id

        def process(self, trace_id):
            return {"accepted_trace_id": trace_id, "worker_status": "processed"}

    task_store_calls = []

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.connection = connection
            self.worker_id = worker_id

        def insert_task(self, **kwargs):
            task_store_calls.append(("insert", kwargs["trace_id"]))

        def claim_task(self, **kwargs):
            task_store_calls.append(("claim", kwargs["trace_id"]))
            return type("Task", (), {"attempt_count": 1, "max_attempts": 5})()

        def mark_succeeded(self, **kwargs):
            task_store_calls.append(("succeeded", kwargs["trace_id"]))

    consumer = FakeConsumer()
    pool = FakePool()

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", FakeTaskStore)
    monkeypatch.setattr(
        "main.build_stage_processor",
        lambda stream_name, **kwargs: ("analysis-core-workers", FakeStageProcessor(kwargs["connection"], stream_name)),
    )
    monkeypatch.setattr("main.mark_trace_stage_processing", lambda connection, trace_id, stage: None)

    results = run_core_batch_once(
        redis_client=object(),
        connection_pool=pool,
        evidence_store=FilesystemEvidenceStore("/tmp/evidence-unused"),
        storage_backend="filesystem",
        llm_judge=None,
        worker_id="worker-1",
        stream_name="analysis.core",
        group_name="analysis-core-workers",
        read_count=2,
        max_inflight=2,
    )

    assert [result["accepted_trace_id"] for result in results] == ["trace_1", "trace_2"]
    assert consumer.acked == ["1748944471000-0", "1748944471000-1"]
    assert task_store_calls == [
        ("insert", "trace_1"),
        ("claim", "trace_1"),
        ("succeeded", "trace_1"),
        ("insert", "trace_2"),
        ("claim", "trace_2"),
        ("succeeded", "trace_2"),
    ]


def test_run_core_batch_once_clamps_read_count_to_max_inflight(monkeypatch):
    from main import run_core_batch_once

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.read_calls = []

        def read_batch(self, count=1, block_ms=5000):
            self.read_calls.append((count, block_ms))
            return []

    consumer = FakeConsumer()

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)

    results = run_core_batch_once(
        redis_client=object(),
        connection_pool=object(),
        evidence_store=FilesystemEvidenceStore("/tmp/evidence-unused"),
        storage_backend="filesystem",
        llm_judge=None,
        worker_id="worker-1",
        stream_name="analysis.core",
        group_name="analysis-core-workers",
        read_count=5,
        max_inflight=2,
    )

    assert results == []
    assert consumer.read_calls == [(2, 5000)]


def test_run_core_batch_once_skips_worker_sleep_for_future_retry_messages(monkeypatch):
    from datetime import datetime, timedelta, timezone

    from main import run_core_batch_once
    from models import AnalysisStage, StreamEnvelope

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_batch(self, count=1, block_ms=5000):
            retry_not_before = (datetime.now(timezone.utc) + timedelta(milliseconds=10)).isoformat()
            return [
                type("Msg", (), {
                    "stream_name": "analysis.core",
                    "message_id": "1748944471000-delayed",
                    "envelope": StreamEnvelope(
                        trace_id="trace_delayed",
                        stage=AnalysisStage.CORE,
                        hints={"retry_not_before": retry_not_before},
                    ),
                })(),
                type("Msg", (), {
                    "stream_name": "analysis.core",
                    "message_id": "1748944471000-ready",
                    "envelope": StreamEnvelope(trace_id="trace_ready", stage=AnalysisStage.CORE),
                })(),
            ]

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakePoolConnection:
        def __init__(self):
            self.connection = type("Conn", (), {
                "commit": lambda self: None,
                "rollback": lambda self: None,
            })()

        def __enter__(self):
            return self.connection

        def __exit__(self, exc_type, exc, tb):
            return False

    class FakePool:
        def connection(self):
            return FakePoolConnection()

    class FakeStageProcessor:
        def process(self, trace_id):
            return {"accepted_trace_id": trace_id, "worker_status": "processed"}

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.connection = connection

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 1, "max_attempts": 5})()

        def mark_succeeded(self, **kwargs):
            return True

    consumer = FakeConsumer()

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", FakeTaskStore)
    monkeypatch.setattr(
        "main.build_stage_processor",
        lambda stream_name, **kwargs: ("analysis-core-workers", FakeStageProcessor()),
    )
    monkeypatch.setattr("main.mark_trace_stage_processing", lambda connection, trace_id, stage: None)
    monkeypatch.setattr(
        "main.maybe_sleep_for_retry_backoff",
        lambda message: (_ for _ in ()).throw(AssertionError("batch path must not sleep in worker threads")),
    )

    results = run_core_batch_once(
        redis_client=object(),
        connection_pool=FakePool(),
        evidence_store=FilesystemEvidenceStore("/tmp/evidence-unused"),
        storage_backend="filesystem",
        llm_judge=None,
        worker_id="worker-1",
        stream_name="analysis.core",
        group_name="analysis-core-workers",
        read_count=2,
        max_inflight=1,
    )

    assert [result["accepted_trace_id"] for result in results] == ["trace_delayed", "trace_ready"]
    assert consumer.acked == ["1748944471000-ready", "1748944471000-delayed"]


def test_run_core_once_marks_processing_before_stage_processor_runs(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    events = []

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-processing",
                "envelope": StreamEnvelope(trace_id="trace_processing", stage=AnalysisStage.CORE),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)
            events.append("ack")

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.connection = connection

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 1, "max_attempts": 5})()

        def mark_succeeded(self, **kwargs):
            events.append("mark_succeeded")

    class FakeConnection:
        def commit(self):
            events.append("commit")

    class StageProcessor:
        def process(self, trace_id):
            events.append("process")
            return {"accepted_trace_id": trace_id, "worker_status": "processed"}

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: FakeConsumer())
    monkeypatch.setattr("main.AnalysisTaskStore", FakeTaskStore)
    monkeypatch.setattr(
        "main.mark_trace_stage_processing",
        lambda connection, trace_id, stage: events.append("processing"),
        raising=False,
    )

    run_core_once(
        redis_client=object(),
        connection=FakeConnection(),
        stage_processor=StageProcessor(),
        worker_id="worker-1",
    )

    assert events[:3] == ["processing", "commit", "process"]


def test_run_core_once_commits_stage_success_before_ack(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    events = []
    consumer = None

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            nonlocal consumer
            consumer = self
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-success-order",
                "envelope": StreamEnvelope(trace_id="trace_success_order", stage=AnalysisStage.CORE),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)
            events.append("ack")

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.connection = connection

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 1, "max_attempts": 5})()

        def mark_succeeded(self, **kwargs):
            events.append("mark_succeeded")

    class FakeConnection:
        def commit(self):
            events.append("commit")

    class StageProcessor:
        def process(self, trace_id):
            events.append("process")
            return {"accepted_trace_id": trace_id, "worker_status": "processed"}

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: FakeConsumer())
    monkeypatch.setattr("main.AnalysisTaskStore", FakeTaskStore)
    monkeypatch.setattr(
        "main.mark_trace_stage_processing",
        lambda connection, trace_id, stage: events.append("processing"),
        raising=False,
    )

    run_core_once(
        redis_client=object(),
        connection=FakeConnection(),
        stage_processor=StageProcessor(),
        worker_id="worker-1",
    )

    assert events == ["processing", "commit", "process", "mark_succeeded", "commit", "ack"]
    assert consumer.acked == ["1748944471000-success-order"]


def test_run_core_once_does_not_ack_when_lease_is_lost_before_success_commit(monkeypatch):
    from main import LeaseLostError, run_core_once
    from models import AnalysisStage, StreamEnvelope

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-lease-lost",
                "envelope": StreamEnvelope(
                    trace_id="trace_lease_lost",
                    stage=AnalysisStage.CORE,
                    enqueued_at="2026-06-03T10:00:00+00:00",
                ),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.failed_retryable = []
            self.failed_terminal = []

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 1, "max_attempts": 5})()

        def mark_succeeded(self, **kwargs):
            return False

        def mark_failed_retryable(self, **kwargs):
            self.failed_retryable.append(kwargs)

        def mark_failed_terminal(self, **kwargs):
            self.failed_terminal.append(kwargs)

    class FakeConnection:
        def __init__(self):
            self.rollbacks = 0

        def commit(self):
            return None

        def rollback(self):
            self.rollbacks += 1

    class StageProcessor:
        def process(self, trace_id):
            return {
                "accepted_trace_id": trace_id,
                "worker_status": "processed",
                "enrichment_required": False,
                "enrichment_reasons": [],
            }

    consumer = FakeConsumer()
    task_store = FakeTaskStore(connection=None, worker_id="worker-1")

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", lambda *args, **kwargs: task_store)
    monkeypatch.setattr("main.mark_trace_stage_processing", lambda connection, trace_id, stage: None)

    with pytest.raises(LeaseLostError, match="trace_lease_lost"):
        run_core_once(
            redis_client=object(),
            connection=FakeConnection(),
            stage_processor=StageProcessor(),
            worker_id="worker-1",
        )

    assert consumer.acked == []
    assert task_store.failed_retryable == []
    assert task_store.failed_terminal == []


def test_run_core_once_enqueues_enrichment_after_core_commit_before_ack(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    events = []

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-enqueue-order",
                "envelope": StreamEnvelope(
                    trace_id="trace_enqueue_order",
                    stage=AnalysisStage.CORE,
                    enqueued_at="2026-06-03T10:00:00+00:00",
                ),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)
            events.append("ack")

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.connection = connection

        def insert_task(self, **kwargs):
            events.append(f"insert:{kwargs['queued_at']}")

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 1, "max_attempts": 5})()

        def mark_succeeded(self, **kwargs):
            events.append("mark_succeeded")

    class FakeConnection:
        def commit(self):
            events.append("commit")

    class StageProcessor:
        def process(self, trace_id):
            events.append("process")
            return {
                "accepted_trace_id": trace_id,
                "worker_status": "processed",
                "enrichment_required": True,
                "enrichment_reasons": ["llm_judge"],
            }

    def fake_enqueue(**kwargs):
        events.append("enqueue")
        return kwargs["result"] | {"enrichment_enqueue_status": "queued"}

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: FakeConsumer())
    monkeypatch.setattr("main.AnalysisTaskStore", FakeTaskStore)
    monkeypatch.setattr(
        "main.mark_trace_stage_processing",
        lambda connection, trace_id, stage: events.append("processing"),
        raising=False,
    )
    monkeypatch.setattr("main.maybe_enqueue_enrichment_after_core_commit", fake_enqueue)

    result = run_core_once(
        redis_client=object(),
        connection=FakeConnection(),
        stage_processor=StageProcessor(),
        worker_id="worker-1",
    )

    assert result["enrichment_enqueue_status"] == "queued"
    assert events == [
        "insert:2026-06-03T10:00:00+00:00",
        "processing",
        "commit",
        "process",
        "mark_succeeded",
        "commit",
        "enqueue",
        "ack",
    ]


def test_run_core_once_uses_message_id_timestamp_when_enqueued_at_missing(monkeypatch):
    from datetime import datetime, timezone

    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    observed = {}

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-0",
                "envelope": StreamEnvelope(
                    trace_id="trace_missing_enqueued_at",
                    stage=AnalysisStage.CORE,
                    enqueued_at="",
                ),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            pass

        def insert_task(self, **kwargs):
            observed["queued_at"] = kwargs["queued_at"]

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 1, "max_attempts": 5})()

        def mark_succeeded(self, **kwargs):
            return None

    class FakeConnection:
        def commit(self):
            return None

    class StageProcessor:
        def process(self, trace_id):
            return {
                "accepted_trace_id": trace_id,
                "worker_status": "processed",
                "enrichment_required": False,
                "enrichment_reasons": [],
            }

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: FakeConsumer())
    monkeypatch.setattr("main.AnalysisTaskStore", FakeTaskStore)
    monkeypatch.setattr("main.mark_trace_stage_processing", lambda connection, trace_id, stage: None)

    run_core_once(
        redis_client=object(),
        connection=FakeConnection(),
        stage_processor=StageProcessor(),
        worker_id="worker-1",
    )

    assert observed["queued_at"] == datetime.fromtimestamp(1748944471, tz=timezone.utc).isoformat()


def test_run_core_once_rolls_back_before_marking_retryable_failure(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    events = []

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-rollback",
                "envelope": StreamEnvelope(trace_id="trace_rollback", stage=AnalysisStage.CORE),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)
            events.append("ack")

    class FakeConnection:
        def __init__(self):
            self.uncommitted_stage_rows = []

        def rollback(self):
            events.append("rollback")
            self.uncommitted_stage_rows.clear()

        def commit(self):
            events.append("commit")

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.connection = connection

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 1, "max_attempts": 5})()

        def mark_succeeded(self, **kwargs):
            raise AssertionError("retryable failure must not mark succeeded")

        def mark_failed_retryable(self, **kwargs):
            assert self.connection.uncommitted_stage_rows == []
            events.append("mark_failed_retryable")

        def mark_failed_terminal(self, **kwargs):
            raise AssertionError("retryable failure must not mark terminal")

    class FailingProcessor:
        def __init__(self, connection):
            self.connection = connection

        def process(self, trace_id):
            self.connection.uncommitted_stage_rows.append(trace_id)
            raise RuntimeError("temporary stage failure")

    connection = FakeConnection()

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: FakeConsumer())
    monkeypatch.setattr("main.AnalysisTaskStore", FakeTaskStore)
    monkeypatch.setattr(
        "main.mark_trace_stage_processing",
        lambda connection, trace_id, stage: events.append("processing"),
        raising=False,
    )
    monkeypatch.setattr(
        "main.mark_trace_stage_failed",
        lambda connection, trace_id, stage, error_code, terminal: events.append("mark_trace_stage_failed"),
    )
    monkeypatch.setattr(
        "main.publish_stream_message",
        lambda *args, **kwargs: events.append("requeue_retry"),
        raising=False,
    )

    with pytest.raises(RuntimeError, match="temporary stage failure"):
        run_core_once(
            redis_client=object(),
            connection=connection,
            stage_processor=FailingProcessor(connection),
            worker_id="worker-1",
        )

    assert events == [
        "processing",
        "commit",
        "rollback",
        "mark_failed_retryable",
        "mark_trace_stage_failed",
        "commit",
        "requeue_retry",
        "ack",
    ]
    assert connection.uncommitted_stage_rows == []


def test_run_core_once_leaves_core_message_pending_when_enrichment_enqueue_fails(monkeypatch):
    from main import EnrichmentEnqueueRetryable, run_core_once
    from models import AnalysisStage, StreamEnvelope

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-enqueue-fail",
                "envelope": StreamEnvelope(
                    trace_id="trace_enqueue_fail",
                    stage=AnalysisStage.CORE,
                    enqueued_at="2026-06-03T10:00:00+00:00",
                ),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.failed_retryable = []
            self.failed_terminal = []

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 1, "max_attempts": 5})()

        def mark_succeeded(self, **kwargs):
            return None

        def mark_failed_retryable(self, **kwargs):
            self.failed_retryable.append(kwargs)

        def mark_failed_terminal(self, **kwargs):
            self.failed_terminal.append(kwargs)

    class FakeConnection:
        def __init__(self):
            self.rollbacks = 0
            self.commits = 0

        def commit(self):
            self.commits += 1

        def rollback(self):
            self.rollbacks += 1

    class StageProcessor:
        def process(self, trace_id):
            return {
                "accepted_trace_id": trace_id,
                "worker_status": "processed",
                "enrichment_required": True,
                "enrichment_reasons": ["llm_judge"],
            }

    consumer = FakeConsumer()
    task_store = FakeTaskStore(connection=None, worker_id="worker-1")
    failed_traces = []

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", lambda *args, **kwargs: task_store)
    monkeypatch.setattr("main.mark_trace_stage_processing", lambda connection, trace_id, stage: None)
    monkeypatch.setattr(
        "main.maybe_enqueue_enrichment_after_core_commit",
        lambda **kwargs: (_ for _ in ()).throw(EnrichmentEnqueueRetryable("redis unavailable")),
    )
    monkeypatch.setattr(
        "main.mark_trace_stage_failed",
        lambda connection, trace_id, stage, error_code, terminal: failed_traces.append((trace_id, error_code, terminal)),
    )

    with pytest.raises(EnrichmentEnqueueRetryable, match="redis unavailable"):
        run_core_once(
            redis_client=object(),
            connection=FakeConnection(),
            stage_processor=StageProcessor(),
            worker_id="worker-1",
        )

    assert consumer.acked == []
    assert task_store.failed_retryable == []
    assert task_store.failed_terminal == []
    assert failed_traces == []


def test_maybe_enqueue_enrichment_after_core_commit_raises_retryable_on_publish_failure(monkeypatch):
    from main import EnrichmentEnqueueRetryable, maybe_enqueue_enrichment_after_core_commit
    from models import AnalysisStage, StreamEnvelope

    class FakeConnection:
        def __init__(self):
            self.traces = {
                "trace_publish_failure": {
                    "core_status": "completed",
                    "enrichment_required": True,
                    "enrichment_status": "pending",
                    "enrichment_queued_at": None,
                    "last_analysis_error_code": "",
                }
            }
            self.commit_calls = 0

        def cursor(self):
            raise AssertionError("publish failure must not mutate trace state before retry")

        def commit(self):
            self.commit_calls += 1

    connection = FakeConnection()

    def fail_publish(*args, **kwargs):
        raise RuntimeError("redis unavailable")

    monkeypatch.setattr("main.publish_stream_message", fail_publish)

    with pytest.raises(EnrichmentEnqueueRetryable, match="redis unavailable"):
        maybe_enqueue_enrichment_after_core_commit(
            redis_client=object(),
            connection=connection,
            message=type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-0",
                "envelope": StreamEnvelope(trace_id="trace_publish_failure", stage=AnalysisStage.CORE),
            })(),
            result={
                "accepted_trace_id": "trace_publish_failure",
                "worker_status": "processed",
                "enrichment_required": True,
                "enrichment_reasons": ["llm_judge"],
            },
        )

    assert connection.traces["trace_publish_failure"]["core_status"] == "completed"
    assert connection.traces["trace_publish_failure"]["enrichment_required"] is True
    assert connection.traces["trace_publish_failure"]["enrichment_status"] == "pending"
    assert connection.traces["trace_publish_failure"]["enrichment_queued_at"] is None
    assert connection.commit_calls == 0


def test_core_stage_hot_path_avoids_heavy_analysis_context_queries():
    from core_stage import CoreStageProcessor
    from models import parse_job

    class FakeCursor:
        def __init__(self, connection):
            self.connection = connection
            self.executed = []
            self._next_row = None

        def execute(self, query, params):
            self.executed.append(" ".join(query.split()))
            normalized = self.executed[-1]
            if "FROM traces" in normalized and "WHERE trace_id = %s" in normalized:
                trace = self.connection.traces[params[0]]
                self._next_row = (
                    trace["trace_id"],
                    trace["route_pattern"],
                    trace["protocol_family"],
                    trace["capture_mode"],
                    trace["username_snapshot"],
                    trace["request_raw_ref"],
                    trace["request_headers_ref"],
                    trace["response_raw_ref"],
                    trace["response_headers_ref"],
                    trace["model_requested"],
                    trace["usage_prompt_tokens"],
                    trace["usage_completion_tokens"],
                    trace["usage_total_tokens"],
                    trace["usage_reasoning_tokens"],
                    trace["usage_cached_tokens"],
                    trace["token_fingerprint"],
                    trace["fingerprint_display"],
                    trace["new_api_token_id_snapshot"],
                    trace["token_name_snapshot"],
                    trace["identity_resolution_status"],
                    trace["client_ip_hash"],
                    trace["user_agent_hash"],
                    trace["status_code"],
                    trace["upstream_status_code"],
                    trace["stream"],
                    trace["request_started_at"],
                    trace["request_body_size"],
                    trace["response_body_size"],
                )
                return
            if "FROM media_snapshot_jobs" in normalized:
                self._next_row = None
                return
            if "usage_aggregates" in normalized or "baseline_cache" in normalized:
                raise AssertionError(f"heavy query should not run in core hot path: {normalized}")
            if "SUM(usage_total_tokens)" in normalized or "COUNT(DISTINCT client_hash)" in normalized:
                raise AssertionError(f"history query should not run in core hot path: {normalized}")
            if normalized.startswith("UPDATE traces SET core_status = 'completed'"):
                self._next_row = None
                return
            self._next_row = None

        def fetchone(self):
            return self._next_row

    class FakeConnection:
        def __init__(self):
            self.traces = {
                "trace_hot_path": {
                    "trace_id": "trace_hot_path",
                    "route_pattern": "/v1/chat/completions",
                    "protocol_family": "openai_chat",
                    "capture_mode": "raw_and_normalized",
                    "username_snapshot": "alice",
                    "request_raw_ref": "file:///tmp/request.bin",
                    "request_headers_ref": "",
                    "response_raw_ref": "file:///tmp/response.bin",
                    "response_headers_ref": "",
                    "model_requested": "gpt-4.1",
                    "usage_prompt_tokens": 11,
                    "usage_completion_tokens": 7,
                    "usage_total_tokens": 18,
                    "usage_reasoning_tokens": 0,
                    "usage_cached_tokens": 0,
                    "token_fingerprint": "tkfp",
                    "fingerprint_display": "tkfp_display",
                    "new_api_token_id_snapshot": 42,
                    "token_name_snapshot": "alice",
                    "identity_resolution_status": "resolved",
                    "client_ip_hash": "",
                    "user_agent_hash": "",
                    "status_code": 200,
                    "upstream_status_code": 200,
                    "stream": False,
                    "request_started_at": "2026-06-03T10:00:00+00:00",
                    "request_body_size": 128,
                    "response_body_size": 256,
                }
            }
            self.cursor_obj = FakeCursor(self)

        def cursor(self):
            return self.cursor_obj

    def fake_process_job_line(payload, evidence_store, repository, context_repository, **kwargs):
        context = repository.analysis_context_for(parse_job(payload))
        assert kwargs["enable_media_derivation"] is False
        assert context.daily_tokens_before == 0
        assert context.short_window_tokens_before == 0
        assert context.distinct_client_hashes_1h == 0
        assert context.trace_effective_tokens_p95 is None
        return {
            "accepted_trace_id": "trace_hot_path",
            "worker_status": "processed",
            "enrichment_required": False,
            "enrichment_reasons": [],
        }

    processor = CoreStageProcessor(
        connection=FakeConnection(),
        evidence_store=FilesystemEvidenceStore("/tmp/evidence-unused"),
        redis_client=None,
        process_job_line_fn=fake_process_job_line,
    )

    result = processor.process("trace_hot_path")

    assert result["accepted_trace_id"] == "trace_hot_path"


def test_process_job_line_can_skip_media_derivation_without_mutating_request_body(tmp_path: Path):
    request_ref = "file:///raw/2026/04/28/trace_media/request_body.bin"
    response_ref = "file:///raw/2026/04/28/trace_media/response_body.bin"
    original_request = json.dumps({
        "model": "gpt-4.1",
        "messages": [{
            "role": "user",
            "content": [
                {"type": "text", "text": "review this image"},
                {
                    "type": "image_url",
                    "image_url": {
                        "url": "data:image/png;base64,aW1n",
                    },
                },
            ],
        }],
    })
    response_body = json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "done"}}],
        "usage": {"total_tokens": 18},
    })

    class RepoWithoutDerivedWrites:
        def __init__(self):
            self.messages = []
            self.results = []
            self.aggregates = []

        def analysis_context_for(self, job):
            return AnalysisContext()

        def save_trace_analysis(self, messages, results, aggregates, anomalies=(), coverage_alerts=()):
            self.messages.extend(messages)
            self.results.extend(results)
            self.aggregates.extend(aggregates)

        def save_media_assets(self, *args, **kwargs):
            raise AssertionError("media derivation must be disabled on the core path")

        def save_derived_evidence_object(self, *args, **kwargs):
            raise AssertionError("sanitized evidence must not be written on the core path")

        def update_request_body_sha256(self, *args, **kwargs):
            raise AssertionError("original request body must remain unchanged")

    store = FilesystemEvidenceStore(tmp_path)
    store.write_text(request_ref, original_request)
    store.write_text(response_ref, response_body)
    repo = RepoWithoutDerivedWrites()
    line = json.dumps({
        "type": "trace_captured",
        "trace_id": "trace_media",
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

    result = process_job_line(
        line,
        store,
        repo,
        enable_media_derivation=False,
    )

    assert result["worker_status"] == "processed"
    assert result["media_assets_extracted"] == 0
    assert result["enrichment_required"] is True
    assert result["enrichment_reasons"] == ["media_derivation"]
    assert store.read_text(request_ref) == original_request


def test_default_process_enrichment_derives_inline_media_without_llm_judge(tmp_path: Path, monkeypatch):
    from enrichment_stage import default_process_enrichment

    request_ref = "file:///raw/2026/04/28/trace_media_enrichment/request_body.bin"
    response_ref = "file:///raw/2026/04/28/trace_media_enrichment/response_body.bin"
    request_body = json.dumps({
        "model": "gpt-4.1",
        "messages": [{
            "role": "user",
            "content": [
                {"type": "text", "text": "review this image"},
                {
                    "type": "image_url",
                    "image_url": {
                        "url": "data:image/png;base64,aW1n",
                    },
                },
            ],
        }],
    })
    response_body = json.dumps({
        "choices": [{"message": {"role": "assistant", "content": "done"}}],
        "usage": {"total_tokens": 18},
    })

    class RecordingRepository:
        instances = []

        def __init__(self, connection):
            self.connection = connection
            self.saved_assets = []
            self.saved_derived = []
            self.saved_results = []
            self.__class__.instances.append(self)

        def load_trace_job_json(self, trace_id: str) -> str:
            return json.dumps({
                "type": "trace_captured",
                "trace_id": trace_id,
                "route_pattern": "/v1/chat/completions",
                "protocol_family": "openai_chat",
                "capture_mode": "raw_and_normalized",
                "username": "alice",
                "request_raw_ref": request_ref,
                "response_raw_ref": response_ref,
                "request_content_type": "application/json",
                "model_requested": "gpt-4.1",
                "usage_total_tokens": 18,
                "status_code": 200,
                "upstream_status_code": 200,
                "request_started_at": "2026-04-28T13:45:22Z",
            }, sort_keys=True)

        def save_media_assets(self, trace_id, assets, derived_from="", storage_backend="filesystem"):
            self.saved_assets.append((trace_id, assets, derived_from, storage_backend))

        def save_derived_evidence_object(self, trace_id, object_ref, content_type, variant, derived_from, storage_backend="filesystem"):
            self.saved_derived.append((trace_id, object_ref, content_type, variant, derived_from, storage_backend))

        def save_trace_analysis(self, messages, results, aggregates):
            self.saved_results.append((messages, results, aggregates))

    class FakeConnection:
        pass

    store = FilesystemEvidenceStore(tmp_path)
    store.write_text(request_ref, request_body)
    store.write_text(response_ref, response_body)
    monkeypatch.setattr("enrichment_stage.PostgresAnalysisRepository", RecordingRepository)

    result = default_process_enrichment(
        "trace_media_enrichment",
        connection=FakeConnection(),
        evidence_store=store,
        storage_backend="filesystem",
        llm_judge=None,
        redis_client=None,
    )

    repo = RecordingRepository.instances[-1]
    assert result["worker_status"] == "processed"
    assert result["analysis_result_count"] == 0
    assert result["media_assets_extracted"] == 1
    assert len(repo.saved_assets) == 1
    assert repo.saved_assets[0][2] == request_ref
    assert len(repo.saved_derived) == 1
    assert store.read_text(request_ref) == request_body
    assert store.read_text(repo.saved_derived[0][1]) != request_body


def test_run_core_once_acks_duplicate_message_when_task_cannot_be_claimed(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope, TaskStatus

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-dup",
                "envelope": StreamEnvelope(trace_id="trace_dup", stage=AnalysisStage.CORE),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.inserted = []
            self.claimed = []

        def insert_task(self, **kwargs):
            self.inserted.append(kwargs)

        def claim_task(self, **kwargs):
            self.claimed.append(kwargs)
            return None

        def get_task(self, **kwargs):
            return type("Task", (), {"status": TaskStatus.SUCCEEDED})()

    class FailingProcessor:
        def process(self, trace_id):
            raise AssertionError("should not process when lease is not acquired")

    consumer = FakeConsumer()

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", FakeTaskStore)
    monkeypatch.setattr("main.trace_needs_enrichment_enqueue_recovery", lambda connection, message: False)

    result = run_core_once(
        redis_client=object(),
        connection=object(),
        stage_processor=FailingProcessor(),
        worker_id="worker-1",
    )

    assert result == {
        "worker_status": "deferred",
        "poll_result": "duplicate_acked",
        "trace_id": "trace_dup",
    }
    assert consumer.acked == ["1748944471000-dup"]


def test_run_core_once_recovers_missing_enrichment_enqueue_before_ack(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope, TaskStatus

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-recover-enqueue",
                "envelope": StreamEnvelope(
                    trace_id="trace_recover_enrichment",
                    stage=AnalysisStage.CORE,
                    enqueued_at="2026-06-03T10:00:00+00:00",
                ),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            pass

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return None

        def get_task(self, **kwargs):
            return type("Task", (), {"status": TaskStatus.SUCCEEDED})()

    consumer = FakeConsumer()

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", FakeTaskStore)
    monkeypatch.setattr("main.trace_needs_enrichment_enqueue_recovery", lambda connection, message: True)
    monkeypatch.setattr(
        "main.maybe_enqueue_enrichment_after_core_commit",
        lambda **kwargs: {
            **kwargs["result"],
            "enrichment_enqueue_status": "queued",
        },
    )

    result = run_core_once(
        redis_client=object(),
        connection=object(),
        stage_processor=object(),
        worker_id="worker-1",
    )

    assert result == {
        "accepted_trace_id": "trace_recover_enrichment",
        "worker_status": "processed",
        "enrichment_required": True,
        "enrichment_reasons": [],
        "enrichment_enqueue_status": "queued",
        "poll_result": "recovered_enrichment_enqueue",
    }
    assert consumer.acked == ["1748944471000-recover-enqueue"]


def test_run_core_once_keeps_reclaimed_message_pending_when_db_lease_still_active(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope, TaskStatus

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-reclaimed",
                "envelope": StreamEnvelope(trace_id="trace_reclaimed", stage=AnalysisStage.CORE),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.inserted = []
            self.claimed = []

        def insert_task(self, **kwargs):
            self.inserted.append(kwargs)

        def claim_task(self, **kwargs):
            self.claimed.append(kwargs)
            return None

        def get_task(self, **kwargs):
            return type("Task", (), {"status": TaskStatus.LEASED})()

    class FailingProcessor:
        def process(self, trace_id):
            raise AssertionError("should not process when another valid lease still owns the task")

    consumer = FakeConsumer()

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", FakeTaskStore)

    result = run_core_once(
        redis_client=object(),
        connection=object(),
        stage_processor=FailingProcessor(),
        worker_id="worker-1",
    )

    assert result == {
        "worker_status": "deferred",
        "poll_result": "active_lease",
        "trace_id": "trace_reclaimed",
    }
    assert consumer.acked == []


def test_run_core_once_acks_invalid_message_without_trace_id(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-invalid",
                "envelope": StreamEnvelope(trace_id="", stage=AnalysisStage.CORE),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FailingTaskStore:
        def __init__(self, *args, **kwargs):
            raise AssertionError("invalid messages must be filtered before task store access")

    class FailingProcessor:
        def process(self, trace_id):
            raise AssertionError("invalid messages must not reach stage processor")

    consumer = FakeConsumer()

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", FailingTaskStore)

    result = run_core_once(
        redis_client=object(),
        connection=object(),
        stage_processor=FailingProcessor(),
        worker_id="worker-1",
    )

    assert result == {
        "worker_status": "deferred",
        "poll_result": "invalid_message_acked",
        "trace_id": "",
    }
    assert consumer.acked == ["1748944471000-invalid"]


def test_run_core_once_requeues_retryable_core_failure_with_backoff_and_acks_old_message(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    class FakeCursor:
        def __init__(self, connection):
            self.connection = connection

        def execute(self, query, params):
            normalized = " ".join(query.split())
            if normalized.startswith("UPDATE traces SET core_status = 'failed'"):
                error_code, trace_id = params
                trace = self.connection.traces[trace_id]
                trace["core_status"] = "failed"
                trace["last_analysis_error_code"] = error_code
                return
            if normalized.startswith("UPDATE traces SET core_status = 'pending'"):
                error_code, trace_id = params
                trace = self.connection.traces[trace_id]
                trace["core_status"] = "pending"
                trace["last_analysis_error_code"] = error_code
                return

        def fetchone(self):
            return None

    class FakeConnection:
        def __init__(self):
            self.traces = {
                "trace_retry": {
                    "core_status": "processing",
                    "last_analysis_error_code": "",
                }
            }
            self.cursor_obj = FakeCursor(self)

        def cursor(self):
            return self.cursor_obj

        def commit(self):
            return None

        def rollback(self):
            return None

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-retry",
                "envelope": StreamEnvelope(trace_id="trace_retry", stage=AnalysisStage.CORE),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.failed_retryable = []
            self.failed_terminal = []

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 1, "max_attempts": 3})()

        def mark_succeeded(self, **kwargs):
            raise AssertionError("retryable failure must not mark succeeded")

        def mark_failed_retryable(self, **kwargs):
            self.failed_retryable.append(kwargs)

        def mark_failed_terminal(self, **kwargs):
            self.failed_terminal.append(kwargs)

    class FailingProcessor:
        def process(self, trace_id):
            raise RuntimeError("temporary redis error")

    consumer = FakeConsumer()
    task_store = FakeTaskStore(connection=None, worker_id="worker-1")
    connection = FakeConnection()
    retry_messages = []

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", lambda *args, **kwargs: task_store)
    monkeypatch.setattr("main.mark_trace_stage_processing", lambda connection, trace_id, stage: None)
    monkeypatch.setattr("main.publish_stream_message", lambda *args, **kwargs: retry_messages.append(kwargs), raising=False)

    with pytest.raises(RuntimeError, match="temporary redis error"):
        run_core_once(
            redis_client=object(),
            connection=connection,
            stage_processor=FailingProcessor(),
            worker_id="worker-1",
        )

    assert consumer.acked == ["1748944471000-retry"]
    assert task_store.failed_terminal == []
    assert task_store.failed_retryable == [{
        "trace_id": "trace_retry",
        "stage": "core",
        "error_code": "RuntimeError",
        "error_message": "temporary redis error",
    }]
    assert len(retry_messages) == 1
    assert retry_messages[0]["stream_name"] == "analysis.core"
    assert retry_messages[0]["trace_id"] == "trace_retry"
    assert retry_messages[0]["stage"] == AnalysisStage.CORE
    assert retry_messages[0]["attempt"] == 2
    assert retry_messages[0]["hints"]["retry_after_seconds"] == "2"
    assert retry_messages[0]["hints"]["retry_not_before"]
    assert retry_messages[0]["hints"]["source_message_id"] == "1748944471000-retry"
    assert retry_messages[0]["hints"]["source_stream"] == "analysis.core"
    assert "enqueued_at" not in retry_messages[0]
    assert connection.traces["trace_retry"]["core_status"] == "pending"
    assert connection.traces["trace_retry"]["last_analysis_error_code"] == "RuntimeError"


def test_run_core_once_marks_terminal_failure_and_acks_after_retry_exhausted(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-terminal",
                "envelope": StreamEnvelope(trace_id="trace_terminal", stage=AnalysisStage.CORE),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.failed_retryable = []
            self.failed_terminal = []

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 3, "max_attempts": 3})()

        def mark_succeeded(self, **kwargs):
            raise AssertionError("terminal failure must not mark succeeded")

        def mark_failed_retryable(self, **kwargs):
            self.failed_retryable.append(kwargs)

        def mark_failed_terminal(self, **kwargs):
            self.failed_terminal.append(kwargs)

    class FailingProcessor:
        def process(self, trace_id):
            raise RuntimeError("temporary redis error")

    class FakeConnection:
        def commit(self):
            return None

        def rollback(self):
            return None

    consumer = FakeConsumer()
    task_store = FakeTaskStore(connection=None, worker_id="worker-1")
    dlq_calls = []
    failed_traces = []

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", lambda *args, **kwargs: task_store)
    monkeypatch.setattr("main.mark_trace_stage_processing", lambda connection, trace_id, stage: None)
    monkeypatch.setattr("main.publish_stream_message", lambda *args, **kwargs: dlq_calls.append(kwargs), raising=False)
    monkeypatch.setattr("main.mark_trace_core_failed", lambda connection, trace_id, error_code: failed_traces.append((trace_id, error_code)))

    with pytest.raises(RuntimeError, match="temporary redis error"):
        run_core_once(
            redis_client=object(),
            connection=FakeConnection(),
            stage_processor=FailingProcessor(),
            worker_id="worker-1",
        )

    assert consumer.acked == ["1748944471000-terminal"]
    assert task_store.failed_retryable == []
    assert task_store.failed_terminal == [{
        "trace_id": "trace_terminal",
        "stage": "core",
        "error_code": "RuntimeError",
        "error_message": "temporary redis error",
    }]
    assert len(dlq_calls) == 1
    assert dlq_calls[0]["stream_name"] == "analysis.dlq"
    assert dlq_calls[0]["trace_id"] == "trace_terminal"
    assert dlq_calls[0]["stage"] == AnalysisStage.CORE
    assert dlq_calls[0]["attempt"] == 3
    assert dlq_calls[0]["enqueued_at"]
    assert dlq_calls[0]["hints"] == {
        "error_code": "RuntimeError",
        "error_message": "temporary redis error",
        "source_message_id": "1748944471000-terminal",
        "source_stream": "analysis.core",
    }
    assert failed_traces == [("trace_terminal", "RuntimeError")]


def test_run_core_once_marks_enrichment_terminal_failure_without_regressing_core_status(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    class FakeCursor:
        def __init__(self, connection):
            self.connection = connection

        def execute(self, query, params):
            normalized = " ".join(query.split())
            if normalized.startswith("UPDATE traces SET core_status = 'failed'"):
                error_code, trace_id = params
                trace = self.connection.traces[trace_id]
                trace["core_status"] = "failed"
                trace["last_analysis_error_code"] = error_code
                return
            if normalized.startswith("UPDATE traces SET enrichment_status = 'failed'"):
                error_code, trace_id = params
                trace = self.connection.traces[trace_id]
                trace["enrichment_status"] = "failed"
                trace["last_analysis_error_code"] = error_code
                return

        def fetchone(self):
            return None

    class FakeConnection:
        def __init__(self):
            self.traces = {
                "trace_enrichment_terminal": {
                    "core_status": "completed",
                    "enrichment_status": "pending",
                    "last_analysis_error_code": "",
                }
            }
            self.cursor_obj = FakeCursor(self)

        def cursor(self):
            return self.cursor_obj

        def commit(self):
            return None

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.enrichment",
                "message_id": "1748944471000-enrichment-terminal",
                "envelope": StreamEnvelope(
                    trace_id="trace_enrichment_terminal",
                    stage=AnalysisStage.ENRICHMENT,
                ),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.failed_retryable = []
            self.failed_terminal = []

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 3, "max_attempts": 3})()

        def mark_succeeded(self, **kwargs):
            raise AssertionError("terminal failure must not mark succeeded")

        def mark_failed_retryable(self, **kwargs):
            self.failed_retryable.append(kwargs)

        def mark_failed_terminal(self, **kwargs):
            self.failed_terminal.append(kwargs)

    class FailingProcessor:
        def process(self, trace_id):
            raise RuntimeError("slow llm timeout")

    consumer = FakeConsumer()
    task_store = FakeTaskStore(connection=None, worker_id="worker-1")
    connection = FakeConnection()
    dlq_calls = []

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", lambda *args, **kwargs: task_store)
    monkeypatch.setattr("main.publish_stream_message", lambda *args, **kwargs: dlq_calls.append(kwargs), raising=False)

    with pytest.raises(RuntimeError, match="slow llm timeout"):
        run_core_once(
            redis_client=object(),
            connection=connection,
            stage_processor=FailingProcessor(),
            worker_id="worker-1",
            stream_name="analysis.enrichment",
            group_name="analysis-enrichment-workers",
        )

    assert consumer.acked == ["1748944471000-enrichment-terminal"]
    assert task_store.failed_retryable == []
    assert task_store.failed_terminal == [{
        "trace_id": "trace_enrichment_terminal",
        "stage": "enrichment",
        "error_code": "RuntimeError",
        "error_message": "slow llm timeout",
    }]
    assert connection.traces["trace_enrichment_terminal"]["core_status"] == "completed"
    assert connection.traces["trace_enrichment_terminal"]["enrichment_status"] == "failed"
    assert connection.traces["trace_enrichment_terminal"]["last_analysis_error_code"] == "RuntimeError"
    assert len(dlq_calls) == 1
    assert dlq_calls[0]["stage"] == AnalysisStage.ENRICHMENT


def test_run_core_once_requeues_retryable_enrichment_failure_with_backoff_and_acks_old_message(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    class FakeCursor:
        def __init__(self, connection):
            self.connection = connection

        def execute(self, query, params):
            normalized = " ".join(query.split())
            if normalized.startswith("UPDATE traces SET enrichment_status = 'failed'"):
                error_code, trace_id = params
                trace = self.connection.traces[trace_id]
                trace["enrichment_status"] = "failed"
                trace["last_analysis_error_code"] = error_code
                return
            if normalized.startswith("UPDATE traces SET enrichment_status = 'pending'"):
                error_code, trace_id = params
                trace = self.connection.traces[trace_id]
                trace["enrichment_status"] = "pending"
                trace["last_analysis_error_code"] = error_code
                return

        def fetchone(self):
            return None

    class FakeConnection:
        def __init__(self):
            self.traces = {
                "trace_enrichment_retryable": {
                    "core_status": "completed",
                    "enrichment_status": "processing",
                    "last_analysis_error_code": "",
                }
            }
            self.cursor_obj = FakeCursor(self)

        def cursor(self):
            return self.cursor_obj

        def commit(self):
            return None

        def rollback(self):
            return None

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.enrichment",
                "message_id": "1748944471000-enrichment-retryable",
                "envelope": StreamEnvelope(
                    trace_id="trace_enrichment_retryable",
                    stage=AnalysisStage.ENRICHMENT,
                    enqueued_at="2026-06-03T10:00:00+00:00",
                ),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.failed_retryable = []
            self.failed_terminal = []

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 1, "max_attempts": 3})()

        def mark_succeeded(self, **kwargs):
            raise AssertionError("retryable failure must not mark succeeded")

        def mark_failed_retryable(self, **kwargs):
            self.failed_retryable.append(kwargs)

        def mark_failed_terminal(self, **kwargs):
            self.failed_terminal.append(kwargs)

    class FailingProcessor:
        def process(self, trace_id):
            raise RuntimeError("slow llm timeout")

    consumer = FakeConsumer()
    task_store = FakeTaskStore(connection=None, worker_id="worker-1")
    connection = FakeConnection()
    retry_messages = []

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", lambda *args, **kwargs: task_store)
    monkeypatch.setattr("main.publish_stream_message", lambda *args, **kwargs: retry_messages.append(kwargs), raising=False)

    with pytest.raises(RuntimeError, match="slow llm timeout"):
        run_core_once(
            redis_client=object(),
            connection=connection,
            stage_processor=FailingProcessor(),
            worker_id="worker-1",
            stream_name="analysis.enrichment",
            group_name="analysis-enrichment-workers",
        )

    assert consumer.acked == ["1748944471000-enrichment-retryable"]
    assert task_store.failed_terminal == []
    assert task_store.failed_retryable == [{
        "trace_id": "trace_enrichment_retryable",
        "stage": "enrichment",
        "error_code": "RuntimeError",
        "error_message": "slow llm timeout",
    }]
    assert len(retry_messages) == 1
    assert retry_messages[0]["stream_name"] == "analysis.enrichment"
    assert retry_messages[0]["trace_id"] == "trace_enrichment_retryable"
    assert retry_messages[0]["stage"] == AnalysisStage.ENRICHMENT
    assert retry_messages[0]["attempt"] == 2
    assert retry_messages[0]["hints"]["retry_after_seconds"] == "2"
    assert retry_messages[0]["hints"]["retry_not_before"]
    assert retry_messages[0]["hints"]["source_message_id"] == "1748944471000-enrichment-retryable"
    assert retry_messages[0]["hints"]["source_stream"] == "analysis.enrichment"
    assert "enqueued_at" not in retry_messages[0]
    assert connection.traces["trace_enrichment_retryable"]["core_status"] == "completed"
    assert connection.traces["trace_enrichment_retryable"]["enrichment_status"] == "pending"
    assert connection.traces["trace_enrichment_retryable"]["last_analysis_error_code"] == "RuntimeError"


def test_run_core_once_does_not_mark_terminal_before_dlq_publish_succeeds(monkeypatch):
    from main import run_core_once
    from models import AnalysisStage, StreamEnvelope

    class FakeConsumer:
        def __init__(self, *args, **kwargs):
            self.acked = []

        def read_one(self, count=1, block_ms=5000):
            return type("Msg", (), {
                "stream_name": "analysis.core",
                "message_id": "1748944471000-terminal-dlq",
                "envelope": StreamEnvelope(trace_id="trace_terminal_dlq", stage=AnalysisStage.CORE),
            })()

        def ack(self, message_id):
            self.acked.append(message_id)

    class FakeTaskStore:
        def __init__(self, connection, worker_id):
            self.failed_terminal = []

        def insert_task(self, **kwargs):
            return None

        def claim_task(self, **kwargs):
            return type("Task", (), {"attempt_count": 3, "max_attempts": 3})()

        def mark_succeeded(self, **kwargs):
            raise AssertionError("terminal failure must not mark succeeded")

        def mark_failed_retryable(self, **kwargs):
            raise AssertionError("terminal failure path should not downgrade to retryable")

        def mark_failed_terminal(self, **kwargs):
            self.failed_terminal.append(kwargs)

    class FailingProcessor:
        def process(self, trace_id):
            raise RuntimeError("temporary redis error")

    consumer = FakeConsumer()
    task_store = FakeTaskStore(connection=None, worker_id="worker-1")

    monkeypatch.setattr("main.StreamConsumer", lambda *args, **kwargs: consumer)
    monkeypatch.setattr("main.AnalysisTaskStore", lambda *args, **kwargs: task_store)

    def fail_dlq_publish(*args, **kwargs):
        raise RuntimeError("dlq unavailable")

    monkeypatch.setattr("main.publish_stream_message", fail_dlq_publish, raising=False)

    with pytest.raises(RuntimeError, match="dlq unavailable"):
        run_core_once(
            redis_client=object(),
            connection=object(),
            stage_processor=FailingProcessor(),
            worker_id="worker-1",
        )

    assert consumer.acked == []
    assert task_store.failed_terminal == []
