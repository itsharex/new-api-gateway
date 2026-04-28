import argparse
import json
import os
import sys

import psycopg
import redis

from evidence import FileEvidenceStore
from models import TraceCapturedJob, UsageAggregateDelta, bucket_start_day, bucket_start_hour, parse_job
from normalizers import normalize_json_trace
from repository import PostgresAnalysisRepository


def aggregate_deltas(job: TraceCapturedJob) -> list[UsageAggregateDelta]:
    success = 1 if 200 <= job.status_code < 400 or job.status_code == 0 else 0
    error = 0 if success else 1
    common = {
        "token_fingerprint": job.token_fingerprint,
        "new_api_token_id": job.new_api_token_id,
        "employee_no": job.employee_no,
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


def process_job_line(line: str, evidence_store: FileEvidenceStore, repository) -> dict:
    job = parse_job(line)
    request_body = evidence_store.read_text(job.request_raw_ref) if job.request_raw_ref else ""
    response_body = evidence_store.read_text(job.response_raw_ref) if job.response_raw_ref else ""
    messages, results = normalize_json_trace(job, request_body, response_body)
    aggregates = aggregate_deltas(job)
    repository.save_trace_analysis(messages, results, aggregates)
    return {
        "accepted_trace_id": job.trace_id,
        "worker_status": "processed",
        "normalized_message_count": len(messages),
        "analysis_result_count": len(results),
        "aggregate_count": len(aggregates),
        "usage_total_tokens": job.usage_total_tokens,
    }


def process_stdin(evidence_root: str, postgres_dsn: str) -> int:
    payload = sys.stdin.read().strip()
    if not payload:
        return 0
    with psycopg.connect(postgres_dsn) as connection:
        result = process_job_line(payload, FileEvidenceStore(evidence_root), PostgresAnalysisRepository(connection))
    print(json.dumps(result, sort_keys=True))
    return 0


def process_redis_once(redis_url: str, list_name: str, evidence_root: str, postgres_dsn: str, timeout_seconds: int) -> int:
    client = redis.Redis.from_url(redis_url, decode_responses=True)
    item = client.blpop(list_name, timeout=timeout_seconds)
    if item is None:
        print(json.dumps({"worker_status": "idle", "list": list_name}, sort_keys=True))
        return 0
    _, payload = item
    with psycopg.connect(postgres_dsn) as connection:
        result = process_job_line(payload, FileEvidenceStore(evidence_root), PostgresAnalysisRepository(connection))
    print(json.dumps(result, sort_keys=True))
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
    return process_stdin(args.evidence_root, args.postgres_dsn)


if __name__ == "__main__":
    raise SystemExit(main())
