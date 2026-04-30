import json
import subprocess
import sys
from pathlib import Path

from evidence import FileEvidenceStore
from main import process_job_line
from models import ContextCatalogEntry


class RecordingRepository:
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


class RecordingContextRepository:
    def __init__(self, contexts=None):
        self.contexts = list(contexts or [])

    def list_active_contexts(self):
        return self.contexts


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
        "employee_no": "E10001",
        "request_raw_ref": "raw/2026/04/28/trace_1/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_1/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_prompt_tokens": 11,
        "usage_completion_tokens": 7,
        "usage_total_tokens": 18,
        "usage_reasoning_tokens": 2,
        "usage_cached_tokens": 3,
        "token_fingerprint": "tkfp_raw",
        "fingerprint_display": "tkfp_display",
        "new_api_token_id": 42,
        "token_name_snapshot": "E10001",
        "status_code": 200,
        "upstream_status_code": 200,
        "stream": False,
        "request_started_at": "2026-04-28T13:45:22Z",
        "request_body_size": 128,
        "response_body_size": 256
    })

    response = process_job_line(line, FileEvidenceStore(tmp_path), repo)

    assert response["accepted_trace_id"] == "trace_1"
    assert response["normalized_message_count"] == 2
    assert response["analysis_result_count"] == 2
    assert len(repo.messages) == 2
    assert len(repo.results) == 2
    assert [result.category for result in repo.results] == ["usage_extraction", "work_relevance"]
    assert [aggregate.bucket_size for aggregate in repo.aggregates] == ["hour", "day"]
    assert repo.aggregates[0].total_tokens == 18
    assert repo.aggregates[0].request_body_bytes == 128
    assert repo.aggregates[0].response_body_bytes == 256


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
        "employee_no": "E10001",
        "request_raw_ref": "raw/2026/04/28/trace_stream/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_stream/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 10,
        "status_code": 200,
        "upstream_status_code": 200,
        "stream": True,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(line, FileEvidenceStore(tmp_path), repo)

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
        "employee_no": "E10001",
        "request_raw_ref": "raw/2026/04/28/trace_work/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_work/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 100,
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(line, FileEvidenceStore(tmp_path), repo, contexts)

    assert response["work_relevance_count"] == 1
    work_results = [result for result in repo.results if result.category == "work_relevance"]
    assert len(work_results) == 1
    assert work_results[0].label == "debugging"
    assert work_results[0].result["work_related_score"] == 0.9


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
        "employee_no": "",
        "request_raw_ref": "raw/2026/04/28/trace_gap/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_gap/response_body.bin",
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

    response = process_job_line(line, FileEvidenceStore(tmp_path), repo)

    assert response["worker_status"] == "processed"
    assert response["work_relevance_count"] == 1
    assert response["anomaly_count"] == 2
    assert response["coverage_alert_count"] == 1
    assert [alert.anomaly_type for alert in repo.anomalies] == [
        "identity_unresolved_success",
        "high_trace_tokens",
    ]
    assert [alert.alert_code for alert in repo.coverage_alerts] == ["normalization_gap"]


def test_process_job_line_detects_low_work_relevance_high_cost(tmp_path: Path):
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
        "employee_no": "E10001",
        "request_raw_ref": "raw/2026/04/28/trace_personal/request_body.bin",
        "response_raw_ref": "raw/2026/04/28/trace_personal/response_body.bin",
        "model_requested": "gpt-4.1",
        "usage_total_tokens": 25000,
        "status_code": 200,
        "upstream_status_code": 200,
        "request_started_at": "2026-04-28T13:45:22Z",
    })

    response = process_job_line(line, FileEvidenceStore(tmp_path), repo, RecordingContextRepository())

    assert response["anomaly_count"] == 2
    assert [alert.anomaly_type for alert in repo.anomalies] == [
        "high_trace_tokens",
        "low_work_relevance_high_cost",
    ]


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
    modified_contract["employee_no"] = "E99999"

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
            "employee_no": "E10001",
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
        "/tmp/evidence-unused",
        "postgres://unused",
        1,
        connection_factory=lambda dsn: FakeConnection(),
    )

    assert exit_code == 0
    assert FakeHeartbeatRepository.calls[0]["worker_id"] == "worker-test"
    assert FakeHeartbeatRepository.calls[0]["status"] == "idle"
    assert FakeHeartbeatRepository.calls[0]["queue_name"] == "analysis_jobs"
    assert FakeHeartbeatRepository.calls[0]["metadata"] == {"poll_result": "idle"}


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
            "/tmp/evidence-unused",
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
            "/tmp/evidence-unused",
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
