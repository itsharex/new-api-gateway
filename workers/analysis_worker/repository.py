import json
from datetime import datetime, timezone
from typing import Iterable
from urllib.parse import urlparse

from models import (
    AnalysisContext,
    AnalysisResult,
    AnomalyAlert,
    CoverageAlert,
    NormalizedMessage,
    TraceCapturedJob,
    UsageAggregateDelta,
    bucket_start_day,
)


def _has_valid_timestamp(value: str) -> bool:
    if not value:
        return True
    try:
        datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return False
    return True


class PostgresAnalysisRepository:
    def __init__(self, connection):
        self.connection = connection

    def analysis_context_for(self, job: TraceCapturedJob) -> AnalysisContext:
        if not job.token_fingerprint:
            return AnalysisContext()
        if not _has_valid_timestamp(job.request_started_at):
            return AnalysisContext()
        cursor = self.connection.cursor()
        daily_bucket = bucket_start_day(job.request_started_at)
        window_end = job.request_started_at or datetime.now(timezone.utc).isoformat()
        cursor.execute(
            """
            SELECT COALESCE(SUM(total_tokens), 0)
            FROM usage_aggregates
            WHERE token_fingerprint = %s
              AND bucket_size = 'day'
              AND bucket_start = %s::timestamptz
            """,
            (job.token_fingerprint, daily_bucket),
        )
        daily_row = cursor.fetchone()
        cursor.execute(
            """
            SELECT COALESCE(SUM(usage_total_tokens), 0)
            FROM traces
            WHERE token_fingerprint = %s
              AND request_started_at >= (%s::timestamptz - interval '5 minutes')
              AND request_started_at < %s::timestamptz
            """,
            (job.token_fingerprint, window_end, window_end),
        )
        short_window_row = cursor.fetchone()
        cursor.execute(
            """
            SELECT COUNT(DISTINCT client_hash)
            FROM (
                SELECT concat_ws(':', NULLIF(client_ip_hash, ''), NULLIF(user_agent_hash, '')) AS client_hash
                FROM traces
                WHERE token_fingerprint = %s
                  AND request_started_at >= (%s::timestamptz - interval '1 hour')
                  AND request_started_at <= %s::timestamptz
                  AND (client_ip_hash <> '' OR user_agent_hash <> '')
                UNION ALL
                SELECT concat_ws(':', NULLIF(%s, ''), NULLIF(%s, '')) AS client_hash
            ) clients
            WHERE client_hash <> ''
            """,
            (
                job.token_fingerprint,
                window_end,
                window_end,
                job.client_ip_hash,
                job.user_agent_hash,
            ),
        )
        client_hash_row = cursor.fetchone()
        return AnalysisContext(
            daily_tokens_before=int(daily_row[0] if daily_row else 0),
            short_window_tokens_before=int(short_window_row[0] if short_window_row else 0),
            distinct_client_hashes_1h=int(client_hash_row[0] if client_hash_row else 0),
            local_timezone_offset_hours=8,
        )

    def save_trace_analysis(
        self,
        messages: Iterable[NormalizedMessage],
        results: Iterable[AnalysisResult],
        aggregates: Iterable[UsageAggregateDelta],
        anomalies: Iterable[AnomalyAlert] = (),
        coverage_alerts: Iterable[CoverageAlert] = (),
    ) -> None:
        messages = list(messages)
        results = list(results)
        aggregates = list(aggregates)
        anomalies = list(anomalies)
        coverage_alerts = list(coverage_alerts)
        cursor = self.connection.cursor()
        for message in messages:
            cursor.execute(
                """
                INSERT INTO normalized_messages (
                    trace_id, direction, sequence_index, role, modality,
                    content_text, content_text_hash, media_url, source_path,
                    protocol_item_type, token_count_estimate, metadata_json
                ) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s::jsonb)
                ON CONFLICT (trace_id, direction, sequence_index, source_path)
                DO UPDATE SET
                    role = EXCLUDED.role,
                    modality = EXCLUDED.modality,
                    content_text = EXCLUDED.content_text,
                    content_text_hash = EXCLUDED.content_text_hash,
                    media_url = EXCLUDED.media_url,
                    protocol_item_type = EXCLUDED.protocol_item_type,
                    token_count_estimate = EXCLUDED.token_count_estimate,
                    metadata_json = EXCLUDED.metadata_json
                """,
                (
                    message.trace_id,
                    message.direction,
                    message.sequence_index,
                    message.role,
                    message.modality,
                    message.content_text,
                    message.content_text_hash,
                    message.media_url,
                    message.source_path,
                    message.protocol_item_type,
                    message.token_count_estimate,
                    json.dumps(message.metadata, sort_keys=True),
                ),
            )
        for message in messages:
            if not _is_snapshot_queue_candidate(message.media_url):
                continue
            cursor.execute(
                """
                INSERT INTO media_snapshot_jobs (
                    trace_id, source_url, source_context, policy_reason, status
                ) VALUES (%s,%s,%s,%s,'queued')
                ON CONFLICT (trace_id, source_url, source_context, policy_reason) DO NOTHING
                """,
                (
                    message.trace_id,
                    message.media_url,
                    message.source_path,
                    "generated_or_referenced_media",
                ),
            )
        for result in results:
            cursor.execute(
                """
                INSERT INTO analysis_results (
                    trace_id, analyzer_name, analyzer_version, policy_version,
                    category, label, score, confidence, severity, result_json
                ) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s::jsonb)
                """,
                (
                    result.trace_id,
                    result.analyzer_name,
                    result.analyzer_version,
                    result.policy_version,
                    result.category,
                    result.label,
                    result.score,
                    result.confidence,
                    result.severity,
                    json.dumps(result.result, sort_keys=True),
                ),
            )
        for aggregate in aggregates:
            cursor.execute(
                """
                INSERT INTO usage_aggregates (
                    bucket_start, bucket_size, token_fingerprint, new_api_token_id,
                    username, token_name_snapshot, model, route_pattern, protocol_family,
                    request_count, success_count, error_count, stream_count,
                    prompt_tokens, completion_tokens, total_tokens, reasoning_tokens, cached_tokens,
                    request_body_bytes, response_body_bytes
                ) VALUES (
                    %s,%s,%s,%s,%s,%s,%s,%s,%s,
                    %s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s
                )
                ON CONFLICT (
                    bucket_start, bucket_size, token_fingerprint, username,
                    model, route_pattern, protocol_family
                ) DO UPDATE SET
                    request_count = usage_aggregates.request_count + EXCLUDED.request_count,
                    success_count = usage_aggregates.success_count + EXCLUDED.success_count,
                    error_count = usage_aggregates.error_count + EXCLUDED.error_count,
                    stream_count = usage_aggregates.stream_count + EXCLUDED.stream_count,
                    prompt_tokens = usage_aggregates.prompt_tokens + EXCLUDED.prompt_tokens,
                    completion_tokens = usage_aggregates.completion_tokens + EXCLUDED.completion_tokens,
                    total_tokens = usage_aggregates.total_tokens + EXCLUDED.total_tokens,
                    reasoning_tokens = usage_aggregates.reasoning_tokens + EXCLUDED.reasoning_tokens,
                    cached_tokens = usage_aggregates.cached_tokens + EXCLUDED.cached_tokens,
                    request_body_bytes = usage_aggregates.request_body_bytes + EXCLUDED.request_body_bytes,
                    response_body_bytes = usage_aggregates.response_body_bytes + EXCLUDED.response_body_bytes,
                    updated_at = now()
                """,
                (
                    aggregate.bucket_start,
                    aggregate.bucket_size,
                    aggregate.token_fingerprint,
                    aggregate.new_api_token_id,
                    aggregate.username,
                    aggregate.token_name_snapshot,
                    aggregate.model,
                    aggregate.route_pattern,
                    aggregate.protocol_family,
                    aggregate.request_count,
                    aggregate.success_count,
                    aggregate.error_count,
                    aggregate.stream_count,
                    aggregate.prompt_tokens,
                    aggregate.completion_tokens,
                    aggregate.total_tokens,
                    aggregate.reasoning_tokens,
                    aggregate.cached_tokens,
                    aggregate.request_body_bytes,
                    aggregate.response_body_bytes,
                ),
            )
        for anomaly in anomalies:
            cursor.execute(
                """
                INSERT INTO usage_anomalies (
                    anomaly_id, anomaly_type, severity, token_fingerprint, fingerprint_display,
                    new_api_token_id, username, token_name_snapshot, window_start, window_end,
                    observed_value, threshold_value, baseline_value, model, route_pattern,
                    sample_trace_ids, reason, detector_version
                ) VALUES (
                    %s,%s,%s,%s,%s,
                    %s,%s,%s,%s,%s,
                    %s,%s,%s,%s,%s,
                    %s,%s,%s
                )
                ON CONFLICT (anomaly_id) DO UPDATE SET
                    severity = EXCLUDED.severity,
                    observed_value = EXCLUDED.observed_value,
                    threshold_value = EXCLUDED.threshold_value,
                    baseline_value = EXCLUDED.baseline_value,
                    sample_trace_ids = EXCLUDED.sample_trace_ids,
                    reason = EXCLUDED.reason,
                    updated_at = now()
                """,
                (
                    anomaly.anomaly_id,
                    anomaly.anomaly_type,
                    anomaly.severity,
                    anomaly.token_fingerprint,
                    anomaly.fingerprint_display,
                    anomaly.new_api_token_id,
                    anomaly.username,
                    anomaly.token_name_snapshot,
                    anomaly.window_start,
                    anomaly.window_end,
                    anomaly.observed_value,
                    anomaly.threshold_value,
                    anomaly.baseline_value,
                    anomaly.model,
                    anomaly.route_pattern,
                    anomaly.sample_trace_ids,
                    anomaly.reason,
                    anomaly.detector_version,
                ),
            )
        for alert in coverage_alerts:
            cursor.execute(
                """
                INSERT INTO coverage_alerts (
                    alert_id, alert_code, severity, method, route_pattern, raw_path,
                    content_type, protocol_family, payload_shape_hash, normalizer,
                    normalizer_version, occurrence_count, sample_trace_ids, message,
                    affected_trace_count, affected_token_count, affected_user_count
                ) VALUES (
                    %s,%s,%s,%s,%s,%s,
                    %s,%s,%s,%s,
                    %s,1,%s,%s,
                    %s,%s,%s
                )
                ON CONFLICT (alert_id) DO UPDATE SET
                    last_seen_at = now(),
                    occurrence_count = coverage_alerts.occurrence_count + 1,
                    sample_trace_ids = (
                        SELECT ARRAY(
                            SELECT DISTINCT unnest(coverage_alerts.sample_trace_ids || EXCLUDED.sample_trace_ids)
                        )
                    ),
                    message = EXCLUDED.message,
                    affected_trace_count = cardinality(
                        ARRAY(
                            SELECT DISTINCT unnest(coverage_alerts.sample_trace_ids || EXCLUDED.sample_trace_ids)
                        )
                    ),
                    affected_token_count = GREATEST(coverage_alerts.affected_token_count, EXCLUDED.affected_token_count),
                    affected_user_count = GREATEST(coverage_alerts.affected_user_count, EXCLUDED.affected_user_count),
                    updated_at = now()
                """,
                (
                    alert.alert_id,
                    alert.alert_code,
                    alert.severity,
                    alert.method,
                    alert.route_pattern,
                    alert.raw_path,
                    alert.content_type,
                    alert.protocol_family,
                    alert.payload_shape_hash,
                    alert.normalizer,
                    alert.normalizer_version,
                    alert.sample_trace_ids,
                    alert.message,
                    alert.affected_trace_count,
                    alert.affected_token_count,
                    alert.affected_user_count,
                ),
            )
        trace_ids = {m.trace_id for m in messages} | {r.trace_id for r in results}
        for tid in trace_ids:
            cursor.execute(
                "UPDATE traces SET analysis_status = 'completed', updated_at = now() WHERE trace_id = %s",
                (tid,),
            )
        self.connection.commit()

    def save_media_assets(self, trace_id: str, assets: list) -> None:
        if not assets:
            return
        cursor = self.connection.cursor()
        for asset in assets:
            cursor.execute(
                """
                INSERT INTO raw_evidence_objects (
                    trace_id, object_type, object_ref, storage_backend,
                    content_type, size_bytes
                ) VALUES (%s, %s, %s, 'filesystem', %s, %s)
                """,
                (
                    trace_id,
                    asset.object_type,
                    asset.object_ref,
                    asset.media_type,
                    asset.size_bytes,
                ),
            )
        self.connection.commit()

    def update_request_body_sha256(self, trace_id: str, sha256: str) -> None:
        cursor = self.connection.cursor()
        cursor.execute(
            "UPDATE traces SET request_body_sha256 = %s, updated_at = now() WHERE trace_id = %s",
            (sha256, trace_id),
        )
        self.connection.commit()


def _is_snapshot_queue_candidate(media_url: str) -> bool:
    if not media_url:
        return False
    return urlparse(media_url).scheme in {"http", "https"}
