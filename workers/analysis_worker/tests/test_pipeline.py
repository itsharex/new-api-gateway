import json
import subprocess
import sys
from pathlib import Path

from evidence import FileEvidenceStore
from main import process_job_line


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
    assert response["analysis_result_count"] == 1
    assert len(repo.messages) == 2
    assert len(repo.results) == 1
    assert [aggregate.bucket_size for aggregate in repo.aggregates] == ["hour", "day"]
    assert repo.aggregates[0].total_tokens == 18
    assert repo.aggregates[0].request_body_bytes == 128
    assert repo.aggregates[0].response_body_bytes == 256


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
    assert response["anomaly_count"] == 2
    assert response["coverage_alert_count"] == 1
    assert [alert.anomaly_type for alert in repo.anomalies] == [
        "identity_unresolved_success",
        "high_trace_tokens",
    ]
    assert [alert.alert_code for alert in repo.coverage_alerts] == ["normalization_gap"]


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
    assert response["anomaly_count"] == 0
    assert response["coverage_alert_count"] == 0
