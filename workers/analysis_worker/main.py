import argparse
import json
import os
import signal
import socket
import sys
from pathlib import Path

import psycopg
import redis

from context_repository import PostgresContextRepository
from evidence import FileEvidenceStore
from heartbeat import HeartbeatRepository
from models import (
    AnalysisContext,
    ContextCatalogEntry,
    TraceCapturedJob,
    UsageAggregateDelta,
    bucket_start_day,
    bucket_start_hour,
    parse_job,
)
from normalizers import normalize_json_trace
from repository import PostgresAnalysisRepository
from rules import detect_anomalies, detect_coverage_alerts, detect_work_relevance_anomalies
from work_relevance import classify_work_relevance


class NoopAnalysisRepository:
    def save_trace_analysis(self, messages, results, aggregates, anomalies=(), coverage_alerts=()):
        pass


class NoopContextRepository:
    def list_active_contexts(self) -> list[ContextCatalogEntry]:
        return []


def aggregate_deltas(job: TraceCapturedJob) -> list[UsageAggregateDelta]:
    success = 1 if 200 <= job.status_code < 400 or job.status_code == 0 else 0
    error = 0 if success else 1
    common = {
        "token_fingerprint": job.token_fingerprint,
        "new_api_token_id": job.new_api_token_id,
        "username": job.username,
        "token_name_snapshot": job.token_name_snapshot,
        "model": job.model_requested,
        "route_pattern": job.route_pattern,
        "protocol_family": job.protocol_family,
        "request_count": 1,
        "success_count": success,
        "error_count": error,
        "stream_count": 1 if job.stream else 0,
        "prompt_tokens": job.usage_prompt_tokens,
        "completion_tokens": job.usage_completion_tokens,
        "total_tokens": job.usage_total_tokens,
        "reasoning_tokens": job.usage_reasoning_tokens,
        "cached_tokens": job.usage_cached_tokens,
        "request_body_bytes": job.request_body_size,
        "response_body_bytes": job.response_body_size,
    }
    return [
        UsageAggregateDelta(bucket_start=bucket_start_hour(job.request_started_at), bucket_size="hour", **common),
        UsageAggregateDelta(bucket_start=bucket_start_day(job.request_started_at), bucket_size="day", **common),
    ]


def process_job_line(line: str, evidence_store: FileEvidenceStore, repository, context_repository=None) -> dict:
    job = parse_job(line)
    request_body = evidence_store.read_text(job.request_raw_ref) if job.request_raw_ref else ""
    response_body = evidence_store.read_text(job.response_raw_ref) if job.response_raw_ref else ""
    contexts = context_repository.list_active_contexts() if context_repository else []
    return process_trace(job, request_body, response_body, repository, contexts)


def process_contract_validation_line(line: str) -> dict:
    job = parse_job(line)
    return process_trace(job, "", "", NoopAnalysisRepository(), [])


def process_trace(
    job: TraceCapturedJob,
    request_body: str,
    response_body: str,
    repository,
    contexts: list[ContextCatalogEntry] | None = None,
) -> dict:
    messages, results = normalize_json_trace(job, request_body, response_body)
    work_relevance = classify_work_relevance(job, messages, list(contexts or []))
    results.append(work_relevance.to_analysis_result())
    aggregates = aggregate_deltas(job)
    load_analysis_context = getattr(repository, "analysis_context_for", None)
    analysis_context = load_analysis_context(job) if load_analysis_context else AnalysisContext()
    anomalies = [
        *detect_anomalies(job, messages, analysis_context),
        *detect_work_relevance_anomalies(job, work_relevance),
    ]
    coverage_alerts = detect_coverage_alerts(job, messages)
    repository.save_trace_analysis(messages, results, aggregates, anomalies, coverage_alerts)
    return {
        "accepted_trace_id": job.trace_id,
        "worker_status": "processed",
        "normalized_message_count": len(messages),
        "analysis_result_count": len(results),
        "work_relevance_count": 1,
        "aggregate_count": len(aggregates),
        "anomaly_count": len(anomalies),
        "coverage_alert_count": len(coverage_alerts),
        "usage_total_tokens": job.usage_total_tokens,
    }


def process_stdin(evidence_root: str, postgres_dsn: str) -> int:
    payload = sys.stdin.read().strip()
    if not payload:
        return 0
    with psycopg.connect(postgres_dsn) as connection:
        result = process_job_line(
            payload,
            FileEvidenceStore(evidence_root),
            PostgresAnalysisRepository(connection),
            PostgresContextRepository(connection),
        )
    print(json.dumps(result, sort_keys=True))
    return 0


def process_contract_stdin() -> int:
    payload = sys.stdin.read().strip()
    if not payload:
        return 0
    try:
        data = json.loads(payload)
        contract_data = json.loads((Path(__file__).with_name("contract_example.json")).read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise SystemExit("service config required for stdin jobs outside contract validation") from exc
    if data != contract_data:
        raise SystemExit("service config required for stdin jobs outside contract validation")
    result = process_contract_validation_line(payload)
    print(json.dumps(result, sort_keys=True))
    return 0


def worker_id() -> str:
    configured = os.environ.get("ANALYSIS_WORKER_ID", "").strip()
    if configured:
        return configured
    return f"{socket.gethostname()}:{os.getpid()}"


def record_heartbeat_safely(heartbeat, connection, **kwargs) -> None:
    rollback = getattr(connection, "rollback", None)
    if rollback:
        try:
            rollback()
        except Exception:
            pass
    try:
        heartbeat.record(**kwargs)
    except Exception:
        pass


def process_redis_once(
    redis_url: str,
    list_name: str,
    evidence_root: str,
    postgres_dsn: str,
    timeout_seconds: int,
    connection_factory=psycopg.connect,
) -> int:
    client = redis.Redis.from_url(redis_url, decode_responses=True)
    item = client.blpop(list_name, timeout=timeout_seconds)
    with connection_factory(postgres_dsn) as connection:
        heartbeat = HeartbeatRepository(connection)
        if item is None:
            heartbeat.record(
                worker_id=worker_id(),
                worker_kind="analysis",
                status="idle",
                queue_name=list_name,
                processed_count=0,
                error_count=0,
                metadata={"poll_result": "idle"},
            )
            print(json.dumps({"worker_status": "idle", "list": list_name}, sort_keys=True))
            return 0
        _, payload = item
        try:
            result = process_job_line(
                payload,
                FileEvidenceStore(evidence_root),
                PostgresAnalysisRepository(connection),
                PostgresContextRepository(connection),
            )
        except Exception as exc:
            record_heartbeat_safely(
                heartbeat,
                connection,
                worker_id=worker_id(),
                worker_kind="analysis",
                status="error",
                queue_name=list_name,
                processed_count=0,
                error_count=1,
                metadata={"error_type": exc.__class__.__name__},
            )
            raise
        heartbeat.record(
            worker_id=worker_id(),
            worker_kind="analysis",
            status="processed",
            queue_name=list_name,
            processed_count=1,
            error_count=0,
            metadata={"trace_id": result.get("accepted_trace_id", "")},
        )
    print(json.dumps(result, sort_keys=True))
    return 0


def process_redis_loop(
    redis_url: str,
    list_name: str,
    evidence_root: str,
    postgres_dsn: str,
    timeout_seconds: int,
) -> int:
    client = redis.Redis.from_url(redis_url, decode_responses=True)
    evidence_store = FileEvidenceStore(evidence_root)
    wid = worker_id()
    running = True

    def _stop(signum, _frame):
        nonlocal running
        running = False

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)
    print(json.dumps({"worker_status": "starting", "worker_id": wid, "list": list_name}), flush=True)

    while running:
        item = client.blpop(list_name, timeout=timeout_seconds)
        if not running:
            break
        with psycopg.connect(postgres_dsn) as connection:
            heartbeat = HeartbeatRepository(connection)
            if item is None:
                heartbeat.record(
                    worker_id=wid,
                    worker_kind="analysis",
                    status="idle",
                    queue_name=list_name,
                    processed_count=0,
                    error_count=0,
                    metadata={"poll_result": "idle"},
                )
                continue
            _, payload = item
            try:
                result = process_job_line(
                    payload,
                    evidence_store,
                    PostgresAnalysisRepository(connection),
                    PostgresContextRepository(connection),
                )
            except Exception as exc:
                record_heartbeat_safely(
                    heartbeat,
                    connection,
                    worker_id=wid,
                    worker_kind="analysis",
                    status="error",
                    queue_name=list_name,
                    processed_count=0,
                    error_count=1,
                    metadata={"error_type": exc.__class__.__name__},
                )
                print(json.dumps({"worker_status": "error", "error": str(exc)}), flush=True)
                continue
            heartbeat.record(
                worker_id=wid,
                worker_kind="analysis",
                status="processed",
                queue_name=list_name,
                processed_count=1,
                error_count=0,
                metadata={"trace_id": result.get("accepted_trace_id", "")},
            )
            print(json.dumps(result, sort_keys=True), flush=True)

    print(json.dumps({"worker_status": "stopped", "worker_id": wid}), flush=True)
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--redis-once", action="store_true")
    parser.add_argument("--redis-url", default=os.environ.get("REDIS_URL", "redis://localhost:6379/0"))
    parser.add_argument("--redis-list", default=os.environ.get("ANALYSIS_REDIS_LIST", "analysis_jobs"))
    parser.add_argument("--redis-timeout-seconds", type=int, default=5)
    parser.add_argument("--evidence-root", default=os.environ.get("EVIDENCE_STORAGE_DIR", ""))
    parser.add_argument("--postgres-dsn", default=os.environ.get("POSTGRES_DSN", ""))
    args = parser.parse_args()
    # Resolve relative paths against the project root (parent of workers/analysis_worker)
    if args.evidence_root and not Path(args.evidence_root).is_absolute():
        args.evidence_root = str((Path(__file__).resolve().parent.parent.parent / args.evidence_root).resolve())
    if not args.redis_once and not args.evidence_root and not args.postgres_dsn:
        return process_contract_stdin()
    if not args.evidence_root:
        raise SystemExit("EVIDENCE_STORAGE_DIR or --evidence-root is required")
    if not args.postgres_dsn:
        raise SystemExit("POSTGRES_DSN or --postgres-dsn is required")
    if args.redis_once:
        return process_redis_once(
            args.redis_url,
            args.redis_list,
            args.evidence_root,
            args.postgres_dsn,
            args.redis_timeout_seconds,
        )
    return process_redis_loop(
        args.redis_url,
        args.redis_list,
        args.evidence_root,
        args.postgres_dsn,
        args.redis_timeout_seconds,
    )


if __name__ == "__main__":
    raise SystemExit(main())
