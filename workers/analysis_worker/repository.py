import json
from datetime import datetime, timezone
from typing import Iterable
from urllib.parse import urlparse

from models import (
    AnalysisStage,
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


def _analysis_result_identity(result: AnalysisResult) -> tuple[str, str, str]:
    if result.stage or result.producer or result.result_key:
        return (
            result.stage or AnalysisStage.CORE.value,
            result.producer or result.analyzer_name,
            result.result_key or f"{result.category}:{result.label}",
        )
    return (
        AnalysisStage.CORE.value,
        result.analyzer_name,
        f"{result.category}:{result.label}",
    )


class PostgresAnalysisRepository:
    def __init__(self, connection):
        self.connection = connection

    def load_trace_job_json(self, trace_id: str) -> str:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            SELECT
                trace_id,
                route_pattern,
                protocol_family,
                capture_mode,
                username_snapshot,
                request_raw_ref,
                request_headers_ref,
                response_raw_ref,
                response_headers_ref,
                model_requested,
                usage_prompt_tokens,
                usage_completion_tokens,
                usage_total_tokens,
                usage_reasoning_tokens,
                usage_cached_tokens,
                token_fingerprint,
                fingerprint_display,
                new_api_token_id_snapshot,
                token_name_snapshot,
                identity_resolution_status,
                client_ip_hash,
                user_agent_hash,
                status_code,
                upstream_status_code,
                stream,
                request_started_at,
                request_body_size,
                response_body_size
            FROM traces
            WHERE trace_id = %s
            """,
            (trace_id,),
        )
        row = cursor.fetchone()
        if row is None:
            raise ValueError(f"trace not found: {trace_id}")
        payload = {
            "type": "trace_captured",
            "trace_id": row[0],
            "route_pattern": row[1],
            "protocol_family": row[2],
            "capture_mode": row[3],
            "username": row[4],
            "request_raw_ref": row[5],
            "request_headers_ref": row[6],
            "response_raw_ref": row[7],
            "response_headers_ref": row[8],
            "model_requested": row[9],
            "usage_prompt_tokens": row[10],
            "usage_completion_tokens": row[11],
            "usage_total_tokens": row[12],
            "usage_reasoning_tokens": row[13],
            "usage_cached_tokens": row[14],
            "token_fingerprint": row[15],
            "fingerprint_display": row[16],
            "new_api_token_id": row[17],
            "token_name_snapshot": row[18],
            "identity_resolution_status": row[19],
            "client_ip_hash": row[20],
            "user_agent_hash": row[21],
            "status_code": row[22],
            "upstream_status_code": row[23],
            "stream": row[24],
            "request_started_at": row[25].isoformat() if hasattr(row[25], "isoformat") else (row[25] or ""),
            "request_body_size": row[26],
            "response_body_size": row[27],
        }
        return json.dumps(payload, sort_keys=True)

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
        cursor.execute(
            """
            SELECT metric_type, metric_value, metadata_json, computed_at
            FROM baseline_cache
            WHERE fingerprint_key = %s AND expires_at > now()
            """,
            (job.token_fingerprint,),
        )
        baseline_rows = cursor.fetchall()

        baseline_fields = {
            "hourly_tokens_median": "hourly_tokens_baseline",
            "hourly_tokens_mad": "hourly_tokens_mad",
            "short_window_baseline": "short_window_baseline",
            "short_window_mad": "short_window_mad",
            "trace_effective_tokens_p95": "trace_effective_tokens_p95",
            "trace_tokens_p95": "trace_tokens_p95",
            "completion_tokens_p95": "completion_tokens_p95",
            "off_hours_baseline": "off_hours_baseline",
            "off_hours_mad": "off_hours_mad",
        }
        model_prefix = "model_hourly_median_"

        baseline_kwargs: dict[str, object] = {}
        model_baselines: dict[str, float] = {}
        max_computed_at = None

        for metric_type, metric_value, _metadata_json, computed_at in baseline_rows:
            if metric_type in baseline_fields:
                baseline_kwargs[baseline_fields[metric_type]] = metric_value
            elif metric_type.startswith(model_prefix):
                model_name = metric_type[len(model_prefix):]
                model_baselines[model_name] = metric_value
            if computed_at is not None and (max_computed_at is None or computed_at > max_computed_at):
                max_computed_at = computed_at

        if "trace_effective_tokens_p95" in baseline_kwargs:
            baseline_kwargs["trace_tokens_p95"] = baseline_kwargs["trace_effective_tokens_p95"]
        if model_baselines:
            baseline_kwargs["model_baselines"] = model_baselines
        if max_computed_at is not None:
            baseline_kwargs["baseline_computed_at"] = max_computed_at.isoformat()
        return AnalysisContext(
            daily_tokens_before=int(daily_row[0] if daily_row else 0),
            short_window_tokens_before=int(short_window_row[0] if short_window_row else 0),
            distinct_client_hashes_1h=int(client_hash_row[0] if client_hash_row else 0),
            local_timezone_offset_hours=8,
            **baseline_kwargs,
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
        trace_ids = {m.trace_id for m in messages} | {r.trace_id for r in results}
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
            stage, producer, result_key = _analysis_result_identity(result)
            cursor.execute(
                """
                INSERT INTO analysis_results (
                    trace_id, analyzer_name, analyzer_version, policy_version,
                    category, label, score, confidence, severity,
                    stage, producer, result_key, result_json
                ) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s::jsonb)
                ON CONFLICT (trace_id, stage, producer, result_key) DO UPDATE SET
                    analyzer_version = EXCLUDED.analyzer_version,
                    policy_version = EXCLUDED.policy_version,
                    score = EXCLUDED.score,
                    confidence = EXCLUDED.confidence,
                    severity = EXCLUDED.severity,
                    result_json = EXCLUDED.result_json
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
                    stage,
                    producer,
                    result_key,
                    json.dumps(result.result, sort_keys=True),
                ),
            )
        seen_usage_fact_traces: set[str] = set()
        for aggregate in aggregates:
            trace_id = aggregate.trace_id
            if not trace_id and len(trace_ids) == 1:
                trace_id = next(iter(trace_ids))
            if not trace_id or trace_id in seen_usage_fact_traces:
                continue
            self._save_trace_usage_fact(
                cursor,
                trace_id=trace_id,
                token_fingerprint=aggregate.token_fingerprint,
                username=aggregate.username,
                model=aggregate.model,
                route_pattern=aggregate.route_pattern,
                protocol_family=aggregate.protocol_family,
                request_started_at=aggregate.request_started_at or aggregate.bucket_start,
                request_count=aggregate.request_count,
                success_count=aggregate.success_count,
                error_count=aggregate.error_count,
                stream_count=aggregate.stream_count,
                prompt_tokens=aggregate.prompt_tokens,
                completion_tokens=aggregate.completion_tokens,
                cached_tokens=aggregate.cached_tokens,
                total_tokens=aggregate.total_tokens,
                reasoning_tokens=aggregate.reasoning_tokens,
                request_body_bytes=aggregate.request_body_bytes,
                response_body_bytes=aggregate.response_body_bytes,
            )
            seen_usage_fact_traces.add(trace_id)
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
        for tid in trace_ids:
            cursor.execute(
                "UPDATE traces SET analysis_status = 'completed', updated_at = now() WHERE trace_id = %s",
                (tid,),
            )

    def save_trace_usage_fact(self, **fact) -> None:
        cursor = self.connection.cursor()
        self._save_trace_usage_fact(cursor, **fact)
        self.connection.commit()

    def _save_trace_usage_fact(self, cursor, **fact) -> None:
        cursor.execute(
            """
            INSERT INTO trace_usage_facts (
                trace_id, token_fingerprint, username, model, route_pattern, protocol_family,
                request_started_at, request_count, success_count, error_count, stream_count,
                prompt_tokens, completion_tokens, cached_tokens, total_tokens, reasoning_tokens,
                request_body_bytes, response_body_bytes, updated_at
            ) VALUES (
                %(trace_id)s, %(token_fingerprint)s, %(username)s, %(model)s, %(route_pattern)s, %(protocol_family)s,
                %(request_started_at)s, %(request_count)s, %(success_count)s, %(error_count)s, %(stream_count)s,
                %(prompt_tokens)s, %(completion_tokens)s, %(cached_tokens)s, %(total_tokens)s, %(reasoning_tokens)s,
                %(request_body_bytes)s, %(response_body_bytes)s, now()
            )
            ON CONFLICT (trace_id) DO UPDATE SET
                token_fingerprint = EXCLUDED.token_fingerprint,
                username = EXCLUDED.username,
                model = EXCLUDED.model,
                route_pattern = EXCLUDED.route_pattern,
                protocol_family = EXCLUDED.protocol_family,
                request_started_at = EXCLUDED.request_started_at,
                request_count = EXCLUDED.request_count,
                success_count = EXCLUDED.success_count,
                error_count = EXCLUDED.error_count,
                stream_count = EXCLUDED.stream_count,
                prompt_tokens = EXCLUDED.prompt_tokens,
                completion_tokens = EXCLUDED.completion_tokens,
                cached_tokens = EXCLUDED.cached_tokens,
                total_tokens = EXCLUDED.total_tokens,
                reasoning_tokens = EXCLUDED.reasoning_tokens,
                request_body_bytes = EXCLUDED.request_body_bytes,
                response_body_bytes = EXCLUDED.response_body_bytes,
                updated_at = now()
            """,
            fact,
        )

    def save_media_assets(
        self,
        trace_id: str,
        assets: list,
        derived_from: str = "",
        storage_backend: str = "filesystem",
    ) -> None:
        if not assets:
            return
        cursor = self.connection.cursor()
        for asset in assets:
            cursor.execute(
                """
                INSERT INTO raw_evidence_objects (
                    trace_id, object_type, object_ref, storage_backend,
                    content_type, size_bytes, variant, derived_from_object_ref
                ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s)
                """,
                (
                    trace_id,
                    asset.object_type,
                    asset.object_ref,
                    storage_backend,
                    asset.media_type,
                    asset.size_bytes,
                    "derived_media",
                    derived_from,
                ),
            )

    def save_derived_evidence_object(
        self,
        trace_id: str,
        object_ref: str,
        content_type: str,
        variant: str,
        derived_from: str,
        storage_backend: str = "filesystem",
    ) -> None:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            INSERT INTO raw_evidence_objects (
                trace_id, object_type, object_ref, storage_backend,
                content_type, size_bytes, variant, derived_from_object_ref
            ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s)
            """,
            (
                trace_id,
                "request_body",
                object_ref,
                storage_backend,
                content_type,
                0,
                variant,
                derived_from,
            ),
        )

    def update_request_body_sha256(self, trace_id: str, sha256: str) -> None:
        cursor = self.connection.cursor()
        cursor.execute(
            "UPDATE traces SET request_body_sha256 = %s, updated_at = now() WHERE trace_id = %s",
            (sha256, trace_id),
        )


class CoreStageAnalysisRepository(PostgresAnalysisRepository):
    def analysis_context_for(self, job: TraceCapturedJob) -> AnalysisContext:
        return AnalysisContext()


def _is_snapshot_queue_candidate(media_url: str) -> bool:
    if not media_url:
        return False
    return urlparse(media_url).scheme in {"http", "https"}
