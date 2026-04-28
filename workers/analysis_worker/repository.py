import json
from typing import Iterable

from models import AnalysisResult, NormalizedMessage, UsageAggregateDelta


class PostgresAnalysisRepository:
    def __init__(self, connection):
        self.connection = connection

    def save_trace_analysis(
        self,
        messages: Iterable[NormalizedMessage],
        results: Iterable[AnalysisResult],
        aggregates: Iterable[UsageAggregateDelta],
    ) -> None:
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
                    employee_no, token_name_snapshot, model, route_pattern, protocol_family,
                    request_count, success_count, error_count, stream_count,
                    prompt_tokens, completion_tokens, total_tokens, reasoning_tokens, cached_tokens,
                    request_body_bytes, response_body_bytes
                ) VALUES (
                    %s,%s,%s,%s,%s,%s,%s,%s,%s,
                    %s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s
                )
                ON CONFLICT (
                    bucket_start, bucket_size, token_fingerprint, employee_no,
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
                    aggregate.employee_no,
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
        self.connection.commit()
