from __future__ import annotations

from typing import Any


def _stage_for_stream(stream_name: str) -> str:
    if stream_name == "analysis.enrichment":
        return "enrichment"
    return "core"


def _group_stats(groups: list[Any], group_name: str) -> tuple[int, int, int]:
    for group in groups:
        if _value_from(group, "name") != group_name:
            continue
        pending = int(_value_from(group, "pending", 0) or 0)
        consumers = int(_value_from(group, "consumers", 0) or 0)
        lag = int(_value_from(group, "lag", 0) or 0)
        return pending, consumers, lag
    return 0, 0, 0


def _oldest_pending_age_seconds(entries: list[Any]) -> int:
    oldest_ms = 0
    for entry in entries:
        delivered_ms = int(_value_from(entry, "time_since_delivered", 0) or 0)
        if delivered_ms > oldest_ms:
            oldest_ms = delivered_ms
    return oldest_ms // 1000


def _pending_entry_id(entry: Any) -> str:
    return str(_value_from(entry, "message_id", _value_from(entry, "id", "")) or "")


def _value_from(item: Any, key: str, default: Any = None) -> Any:
    if isinstance(item, dict):
        return item.get(key, default)
    return getattr(item, key, default)


def _safe_rate(numerator: int, denominator: int) -> float:
    if denominator <= 0:
        return 0.0
    return float(numerator) / float(denominator)


def _sample_oldest_pending_age_seconds(redis_client, stream_name: str, group_name: str, page_size: int = 100) -> int:
    if redis_client is None:
        return 0
    if page_size <= 0:
        page_size = 100
    start = "-"
    oldest_seconds = 0
    while True:
        pending_entries = redis_client.xpending_range(stream_name, group_name, start, "+", page_size) or []
        oldest_seconds = max(oldest_seconds, _oldest_pending_age_seconds(pending_entries))
        if len(pending_entries) < page_size:
            return oldest_seconds
        last_id = _pending_entry_id(pending_entries[-1]).strip()
        if not last_id:
            return oldest_seconds
        start = f"({last_id}"


class RuntimeMetricsSampler:
    def __init__(self, connection, redis_client):
        self.connection = connection
        self.redis_client = redis_client

    def sample(self, stream_name: str, group_name: str) -> dict[str, int | float | str]:
        stage = _stage_for_stream(stream_name)
        active_consumers = 0
        oldest_pending_age_seconds = 0
        stream_pending_count = 0
        stream_lag_count = 0

        if self.redis_client is not None:
            groups = self.redis_client.xinfo_groups(stream_name) or []
            stream_pending_count, active_consumers, stream_lag_count = _group_stats(groups, group_name)
            oldest_pending_age_seconds = _sample_oldest_pending_age_seconds(
                self.redis_client,
                stream_name,
                group_name,
                100,
            )

        cursor = self.connection.cursor()
        cursor.execute(
            """
            SELECT
                COALESCE(COUNT(*) FILTER (WHERE status = 'queued'), 0),
                COALESCE(COUNT(*) FILTER (WHERE status = 'leased'), 0),
                COALESCE(COUNT(*) FILTER (WHERE status = 'failed_retryable'), 0),
                COALESCE(COUNT(*) FILTER (WHERE status = 'failed_terminal'), 0),
                COALESCE(COUNT(*) FILTER (
                    WHERE status = 'succeeded' AND completed_at >= now() - interval '1 minute'
                ), 0),
                COALESCE(PERCENTILE_CONT(0.50) WITHIN GROUP (
                    ORDER BY EXTRACT(EPOCH FROM (started_at - queued_at)) * 1000
                ) FILTER (WHERE started_at IS NOT NULL AND queued_at IS NOT NULL), 0),
                COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (
                    ORDER BY EXTRACT(EPOCH FROM (started_at - queued_at)) * 1000
                ) FILTER (WHERE started_at IS NOT NULL AND queued_at IS NOT NULL), 0),
                COALESCE(PERCENTILE_CONT(0.50) WITHIN GROUP (
                    ORDER BY EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000
                ) FILTER (WHERE completed_at IS NOT NULL AND started_at IS NOT NULL), 0),
                COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (
                    ORDER BY EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000
                ) FILTER (WHERE completed_at IS NOT NULL AND started_at IS NOT NULL), 0),
                COALESCE(COUNT(*) FILTER (
                    WHERE status = 'failed_retryable' AND updated_at >= now() - interval '1 minute'
                ), 0),
                COALESCE(COUNT(*) FILTER (
                    WHERE status = 'failed_terminal' AND completed_at >= now() - interval '1 minute'
                ), 0),
                COALESCE(COUNT(*) FILTER (
                    WHERE (
                        (status = 'failed_retryable' AND updated_at >= now() - interval '1 minute')
                        OR (status = 'failed_terminal' AND completed_at >= now() - interval '1 minute')
                    ) AND (
                        LOWER(last_error_code) LIKE '%%timeout%%'
                        OR (
                            last_error_code = 'LLMJudgeUnavailable'
                            AND LOWER(last_error_message) LIKE '%%timeout%%'
                        )
                    )
                ), 0)
            FROM analysis_tasks
            WHERE stage = %s
            """,
            (stage,),
        )
        row = cursor.fetchone() or (0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
        queued_count = int(row[0] or 0)
        leased_count = int(row[1] or 0)
        retryable_fail_count = int(row[2] or 0)
        terminal_fail_count = int(row[3] or 0)
        throughput_per_minute = int(row[4] or 0)
        queue_wait_p50_ms = int(row[5] or 0)
        queue_wait_p95_ms = int(row[6] or 0)
        processing_p50_ms = int(row[7] or 0)
        processing_p95_ms = int(row[8] or 0)
        recent_retryable_fail_count = int(row[9] or 0)
        recent_terminal_fail_count = int(row[10] or 0)
        recent_llm_judge_timeout_count = int(row[11] or 0)
        recent_attempt_count = throughput_per_minute + recent_retryable_fail_count + recent_terminal_fail_count
        success_rate = _safe_rate(throughput_per_minute, recent_attempt_count)
        retryable_fail_rate = _safe_rate(recent_retryable_fail_count, recent_attempt_count)
        terminal_fail_rate = _safe_rate(recent_terminal_fail_count, recent_attempt_count)
        llm_judge_timeout_rate = _safe_rate(recent_llm_judge_timeout_count, recent_attempt_count)
        pending_count = max(queued_count + retryable_fail_count, stream_lag_count)
        leased_count = max(leased_count, stream_pending_count)
        queue_depth = pending_count + leased_count

        cursor.execute(
            """
            INSERT INTO analysis_runtime_samples (
                stage,
                queue_depth,
                pending_count,
                leased_count,
                oldest_pending_age_seconds,
                throughput_per_minute,
                queue_wait_p50_ms,
                queue_wait_p95_ms,
                processing_p50_ms,
                processing_p95_ms,
                success_rate,
                retryable_fail_rate,
                terminal_fail_rate,
                llm_judge_timeout_rate,
                retryable_fail_count,
                terminal_fail_count,
                active_consumers
            ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
            """,
            (
                stage,
                queue_depth,
                pending_count,
                leased_count,
                oldest_pending_age_seconds,
                throughput_per_minute,
                queue_wait_p50_ms,
                queue_wait_p95_ms,
                processing_p50_ms,
                processing_p95_ms,
                success_rate,
                retryable_fail_rate,
                terminal_fail_rate,
                llm_judge_timeout_rate,
                retryable_fail_count,
                terminal_fail_count,
                active_consumers,
            ),
        )
        self.connection.commit()
        return {
            "stage": stage,
            "queue_depth": queue_depth,
            "pending_count": pending_count,
            "leased_count": leased_count,
            "oldest_pending_age_seconds": oldest_pending_age_seconds,
            "throughput_per_minute": throughput_per_minute,
            "success_rate": success_rate,
            "retryable_fail_rate": retryable_fail_rate,
            "terminal_fail_rate": terminal_fail_rate,
            "llm_judge_timeout_rate": llm_judge_timeout_rate,
            "queue_wait_p50_ms": queue_wait_p50_ms,
            "queue_wait_p95_ms": queue_wait_p95_ms,
            "processing_p50_ms": processing_p50_ms,
            "processing_p95_ms": processing_p95_ms,
            "retryable_fail_count": retryable_fail_count,
            "terminal_fail_count": terminal_fail_count,
            "active_consumers": active_consumers,
        }
