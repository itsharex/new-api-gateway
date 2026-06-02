import argparse
import json
import math
import os
import signal
import socket
import sys
from hashlib import sha256
from pathlib import Path

import psycopg
import redis

from context_repository import PostgresContextRepository
from evidence import EvidenceStore, FilesystemEvidenceStore
from media_extraction import MediaExtractionContext
from heartbeat import HeartbeatRepository
from llm_judge import LLMJudgeClient
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


def create_evidence_store() -> EvidenceStore:
    backend = os.environ.get("EVIDENCE_STORAGE_BACKEND", "").strip()
    if backend == "oss":
        from oss_evidence import OSSEvidenceStore
        endpoint = os.environ.get("OSS_ENDPOINT", "").strip()
        bucket = os.environ.get("OSS_BUCKET", "").strip()
        access_key_id = os.environ.get("OSS_ACCESS_KEY_ID", "").strip()
        access_key_secret = os.environ.get("OSS_ACCESS_KEY_SECRET", "").strip()
        missing = [k for k, v in [
            ("OSS_ENDPOINT", endpoint),
            ("OSS_BUCKET", bucket),
            ("OSS_ACCESS_KEY_ID", access_key_id),
            ("OSS_ACCESS_KEY_SECRET", access_key_secret),
        ] if not v]
        if missing:
            raise SystemExit(f"EVIDENCE_STORAGE_BACKEND=oss requires {', '.join(missing)}")
        return OSSEvidenceStore.from_env(endpoint, bucket, access_key_id, access_key_secret)
    if backend == "filesystem":
        evidence_dir = os.environ.get("EVIDENCE_STORAGE_DIR", "").strip()
        if not evidence_dir:
            raise SystemExit("EVIDENCE_STORAGE_DIR is required when EVIDENCE_STORAGE_BACKEND=filesystem")
        if not Path(evidence_dir).is_absolute():
            evidence_dir = str((Path(__file__).resolve().parent.parent.parent / evidence_dir).resolve())
        return FilesystemEvidenceStore(evidence_dir)
    raise SystemExit(f"EVIDENCE_STORAGE_BACKEND must be 'filesystem' or 'oss', got {backend!r}")


def create_llm_judge_from_env() -> LLMJudgeClient | None:
    base_url = os.environ.get("LLM_JUDGE_BASE_URL", "").strip()
    model = os.environ.get("LLM_JUDGE_MODEL", "").strip()
    api_key_raw = os.environ.get("LLM_JUDGE_API_KEY")
    timeout_raw_env = os.environ.get("LLM_JUDGE_TIMEOUT_SECONDS")
    if not any((base_url, model, api_key_raw is not None, timeout_raw_env is not None)):
        return None
    if not base_url or not model:
        raise SystemExit(
            "LLM_JUDGE_BASE_URL and LLM_JUDGE_MODEL must be set when any LLM_JUDGE_* variable is configured"
        )
    if api_key_raw is None:
        api_key = None
    else:
        api_key = api_key_raw.strip() or None
    timeout_raw = (timeout_raw_env if timeout_raw_env is not None else "20").strip()
    try:
        timeout_seconds = float(timeout_raw)
    except ValueError as exc:
        raise SystemExit("LLM_JUDGE_TIMEOUT_SECONDS must be a valid number") from exc
    if not math.isfinite(timeout_seconds) or timeout_seconds <= 0:
        raise SystemExit("LLM_JUDGE_TIMEOUT_SECONDS must be a finite positive number")
    return LLMJudgeClient(
        base_url=base_url,
        model=model,
        api_key=api_key,
        timeout_seconds=timeout_seconds,
    )


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


def process_job_line(
    line: str,
    evidence_store: EvidenceStore,
    repository,
    context_repository=None,
    storage_backend: str = "filesystem",
    llm_judge=None,
) -> dict:
    job = parse_job(line)
    request_body = evidence_store.read_text(job.request_raw_ref) if job.request_raw_ref else ""
    response_body = evidence_store.read_text(job.response_raw_ref) if job.response_raw_ref else ""
    contexts = context_repository.list_active_contexts() if context_repository else []
    return process_trace(
        job,
        request_body,
        response_body,
        repository,
        contexts,
        evidence_store,
        storage_backend=storage_backend,
        llm_judge=llm_judge,
    )


def process_contract_validation_line(line: str) -> dict:
    job = parse_job(line)
    return process_trace(job, "", "", NoopAnalysisRepository(), [])


def llm_judge_metadata(work_relevance) -> dict:
    fallback_items = [
        item for item in work_relevance.evidence
        if isinstance(item, dict) and item.get("kind") == "llm_unavailable"
    ]
    if not fallback_items:
        return {}
    return {
        "llm_judge_status": "degraded",
        "llm_judge_error_type": str(fallback_items[0].get("category") or "unknown"),
        "llm_judge_fallback_count": len(fallback_items),
    }


def processed_heartbeat_metadata(result: dict) -> dict:
    metadata = {"trace_id": result.get("accepted_trace_id", "")}
    for key in ("llm_judge_status", "llm_judge_error_type", "llm_judge_fallback_count"):
        if key in result:
            metadata[key] = result[key]
    return metadata


def process_trace(
    job: TraceCapturedJob,
    request_body: str,
    response_body: str,
    repository,
    contexts: list[ContextCatalogEntry] | None = None,
    evidence_store: EvidenceStore | None = None,
    storage_backend: str = "filesystem",
    llm_judge=None,
) -> dict:
    extraction_context: MediaExtractionContext | None = None
    if evidence_store and job.request_raw_ref:
        evidence_dir = job.request_raw_ref.rsplit("/", 1)[0]
        extraction_context = MediaExtractionContext(evidence_store, evidence_dir, job.trace_id)
    messages, results = normalize_json_trace(job, request_body, response_body, extraction_context)
    work_relevance = classify_work_relevance(job, messages, list(contexts or []), llm_judge=llm_judge)
    results.append(work_relevance.to_analysis_result())
    llm_metadata = llm_judge_metadata(work_relevance)
    aggregates = aggregate_deltas(job)
    load_analysis_context = getattr(repository, "analysis_context_for", None)
    analysis_context = load_analysis_context(job) if load_analysis_context else AnalysisContext()
    anomalies = [
        *detect_anomalies(job, messages, analysis_context),
        *detect_work_relevance_anomalies(job, work_relevance),
    ]
    coverage_alerts = detect_coverage_alerts(job, messages)
    repository.save_trace_analysis(messages, results, aggregates, anomalies, coverage_alerts)
    if extraction_context and extraction_context.replacements:
        extraction_context.apply_replacements(job.request_raw_ref)
        if hasattr(repository, "save_media_assets"):
            repository.save_media_assets(job.trace_id, extraction_context.assets, storage_backend=storage_backend)
        if hasattr(repository, "update_request_body_sha256"):
            modified = evidence_store.read_text(job.request_raw_ref)
            new_sha = sha256(modified.encode("utf-8")).hexdigest()
            repository.update_request_body_sha256(job.trace_id, new_sha)
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
        "media_assets_extracted": len(extraction_context.assets) if extraction_context else 0,
        **llm_metadata,
    }


def process_stdin(evidence_store: EvidenceStore, postgres_dsn: str) -> int:
    payload = sys.stdin.read().strip()
    if not payload:
        return 0
    with psycopg.connect(postgres_dsn) as connection:
        result = process_job_line(
            payload,
            evidence_store,
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
    evidence_store: EvidenceStore,
    postgres_dsn: str,
    timeout_seconds: int,
    connection_factory=psycopg.connect,
    storage_backend: str = "filesystem",
    llm_judge=None,
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
                evidence_store,
                PostgresAnalysisRepository(connection),
                PostgresContextRepository(connection),
                storage_backend=storage_backend,
                llm_judge=llm_judge,
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
            metadata=processed_heartbeat_metadata(result),
        )
    print(json.dumps(result, sort_keys=True))
    return 0


def process_redis_loop(
    redis_url: str,
    list_name: str,
    evidence_store: EvidenceStore,
    postgres_dsn: str,
    timeout_seconds: int,
    storage_backend: str = "filesystem",
    llm_judge=None,
) -> int:
    client = redis.Redis.from_url(redis_url, decode_responses=True)
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
                    storage_backend=storage_backend,
                    llm_judge=llm_judge,
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
                metadata=processed_heartbeat_metadata(result),
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
    parser.add_argument("--postgres-dsn", default=os.environ.get("POSTGRES_DSN", ""))
    parser.add_argument("--offline-batch", action="store_true",
                        help="Run offline baseline computation and exit")
    args = parser.parse_args()

    if args.offline_batch:
        from offline import run_offline_batch
        dsn = os.environ.get("POSTGRES_DSN", "")
        if not dsn:
            print("--offline-batch requires POSTGRES_DSN environment variable", file=sys.stderr)
            return 1
        with psycopg.connect(dsn) as conn:
            result = run_offline_batch(conn)
        print(f"offline batch complete: {result}")
        return 0

    if not args.redis_once and "EVIDENCE_STORAGE_BACKEND" not in os.environ and not args.postgres_dsn:
        return process_contract_stdin()

    evidence_store = create_evidence_store()
    storage_backend = os.environ.get("EVIDENCE_STORAGE_BACKEND", "")
    llm_judge = create_llm_judge_from_env()

    if not args.postgres_dsn:
        raise SystemExit("POSTGRES_DSN is required")
    if args.redis_once:
        return process_redis_once(
            args.redis_url,
            args.redis_list,
            evidence_store,
            args.postgres_dsn,
            args.redis_timeout_seconds,
            storage_backend=storage_backend,
            llm_judge=llm_judge,
        )
    return process_redis_loop(
        args.redis_url,
        args.redis_list,
        evidence_store,
        args.postgres_dsn,
        args.redis_timeout_seconds,
        storage_backend=storage_backend,
        llm_judge=llm_judge,
    )


if __name__ == "__main__":
    raise SystemExit(main())
