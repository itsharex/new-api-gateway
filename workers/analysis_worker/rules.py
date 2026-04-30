from datetime import datetime, timedelta, timezone

from models import (
    AnalysisContext,
    AnomalyAlert,
    CoverageAlert,
    NormalizedMessage,
    TraceCapturedJob,
    WorkRelevanceAssessment,
    anomaly_id,
    bucket_start_hour,
    coverage_alert_id,
    stable_suffix,
    window_end_from_start,
)


DETECTOR_VERSION = "rules_mvp_2026_04_28"
NORMALIZER_VERSION = "normalizer_mvp_2026_04_28"

HIGH_TRACE_TOKEN_THRESHOLD = 20_000
LOW_WORK_RELEVANCE_TOKEN_THRESHOLD = 20_000
LOW_WORK_RELEVANCE_PERSONAL_SCORE_THRESHOLD = 0.6
RAW_ONLY_RESPONSE_BYTES_THRESHOLD = 1_048_576
MISSING_TIMESTAMP_WINDOW_START = "1970-01-01T00:00:00+00:00"
MISSING_TIMESTAMP_WINDOW_END = "1970-01-01T00:01:00+00:00"


def detect_anomalies(
    job: TraceCapturedJob,
    messages: list[NormalizedMessage] | None = None,
    context: AnalysisContext | None = None,
) -> list[AnomalyAlert]:
    messages = messages or []
    context = context or AnalysisContext()
    alerts: list[AnomalyAlert] = []
    if job.identity_resolution_status == "missing_employee_no":
        alerts.append(_anomaly(
            job,
            "missing_employee_no",
            "high",
            observed_value=1,
            threshold_value=0,
            reason="identity resolver marked the trace as missing an employee number",
        ))
    elif _upstream_success(job) and not job.employee_no:
        alerts.append(_anomaly(
            job,
            "identity_unresolved_success",
            "high",
            observed_value=1,
            threshold_value=0,
            reason="identity was unresolved while upstream returned a successful response",
        ))
    if job.identity_resolution_status == "invalid_employee_no":
        alerts.append(_anomaly(
            job,
            "invalid_employee_no",
            "high",
            observed_value=1,
            threshold_value=0,
            reason="identity resolver marked the token name snapshot as an invalid employee number",
        ))
    if job.usage_total_tokens > HIGH_TRACE_TOKEN_THRESHOLD:
        alerts.append(_anomaly(
            job,
            "high_trace_tokens",
            "medium",
            observed_value=job.usage_total_tokens,
            threshold_value=HIGH_TRACE_TOKEN_THRESHOLD,
            reason=f"single trace used {job.usage_total_tokens} tokens, exceeding {HIGH_TRACE_TOKEN_THRESHOLD}",
        ))
    has_token_context = bool(job.token_fingerprint)
    if has_token_context:
        daily_total = context.daily_tokens_before + job.usage_total_tokens
        if daily_total >= context.daily_token_limit:
            alerts.append(_anomaly(
                job,
                "daily_token_limit_exceeded",
                "high",
                observed_value=daily_total,
                threshold_value=context.daily_token_limit,
                reason=(
                    f"daily token total reached {daily_total}, meeting or exceeding "
                    f"{context.daily_token_limit}"
                ),
            ))
        short_window_total = context.short_window_tokens_before + job.usage_total_tokens
        if short_window_total >= context.short_window_token_threshold:
            alerts.append(_anomaly(
                job,
                "short_window_token_spike",
                "medium",
                observed_value=short_window_total,
                threshold_value=context.short_window_token_threshold,
                reason=(
                    f"short-window token total reached {short_window_total}, meeting or exceeding "
                    f"{context.short_window_token_threshold}"
                ),
            ))
    if (
        job.model_requested.strip().lower() in context.expensive_model_set()
        and job.usage_total_tokens >= context.expensive_model_token_threshold
    ):
        alerts.append(_anomaly(
            job,
            "expensive_model_overuse",
            "high",
            observed_value=job.usage_total_tokens,
            threshold_value=context.expensive_model_token_threshold,
            reason=(
                f"expensive model {job.model_requested} used {job.usage_total_tokens} tokens, "
                "meeting or exceeding "
                f"{context.expensive_model_token_threshold}"
            ),
        ))
    if job.usage_completion_tokens >= context.long_output_token_threshold:
        alerts.append(_anomaly(
            job,
            "long_output_anomaly",
            "medium",
            observed_value=job.usage_completion_tokens,
            threshold_value=context.long_output_token_threshold,
            reason=(
                f"completion used {job.usage_completion_tokens} tokens, meeting or exceeding "
                f"{context.long_output_token_threshold}"
            ),
        ))
    repeated_prompt_count = _max_repeated_prompt_count(messages)
    if repeated_prompt_count >= context.repeated_prompt_threshold:
        alerts.append(_anomaly(
            job,
            "repeated_prompt",
            "medium",
            observed_value=repeated_prompt_count,
            threshold_value=context.repeated_prompt_threshold,
            reason=f"same request prompt appeared {repeated_prompt_count} times within one trace",
        ))
    if (
        _is_off_hours(job.request_started_at, context.local_timezone_offset_hours)
        and job.usage_total_tokens >= context.off_hours_token_threshold
    ):
        alerts.append(_anomaly(
            job,
            "off_hours_high_usage",
            "medium",
            observed_value=job.usage_total_tokens,
            threshold_value=context.off_hours_token_threshold,
            reason=(
                f"off-hours trace used {job.usage_total_tokens} tokens, meeting or exceeding "
                f"{context.off_hours_token_threshold}"
            ),
        ))
    if (
        has_token_context
        and context.distinct_client_hashes_1h >= context.token_leak_distinct_client_threshold
    ):
        alerts.append(_anomaly(
            job,
            "possible_token_leak",
            "high",
            observed_value=context.distinct_client_hashes_1h,
            threshold_value=context.token_leak_distinct_client_threshold,
            reason=(
                f"token appeared from {context.distinct_client_hashes_1h} distinct client hashes "
                "within the last hour"
            ),
        ))
    if job.capture_mode in {"raw_only", "raw_and_minimal"} and job.response_body_size > RAW_ONLY_RESPONSE_BYTES_THRESHOLD:
        alerts.append(_anomaly(
            job,
            "raw_only_large_response",
            "medium",
            observed_value=job.response_body_size,
            threshold_value=RAW_ONLY_RESPONSE_BYTES_THRESHOLD,
            reason="raw-only route returned a large response body without deep normalization",
        ))
    if job.status_code >= 500 or job.upstream_status_code >= 500:
        alerts.append(_anomaly(
            job,
            "retry_storm_trace",
            "medium",
            observed_value=max(job.status_code, job.upstream_status_code),
            threshold_value=500,
            reason="trace returned a server error and may contribute to retry storms",
        ))
    return alerts


def detect_work_relevance_anomalies(
    job: TraceCapturedJob,
    assessment: WorkRelevanceAssessment,
) -> list[AnomalyAlert]:
    if job.usage_total_tokens < LOW_WORK_RELEVANCE_TOKEN_THRESHOLD:
        return []
    if assessment.personal_use_score < LOW_WORK_RELEVANCE_PERSONAL_SCORE_THRESHOLD:
        return []
    return [_anomaly(
        job,
        "low_work_relevance_high_cost",
        "high",
        observed_value=job.usage_total_tokens,
        threshold_value=LOW_WORK_RELEVANCE_TOKEN_THRESHOLD,
        reason=(
            f"trace used {job.usage_total_tokens} tokens with personal use score "
            f"{assessment.personal_use_score:.2f}"
        ),
    )]


def detect_coverage_alerts(job: TraceCapturedJob, messages: list[NormalizedMessage]) -> list[CoverageAlert]:
    if job.capture_mode != "raw_and_normalized":
        return []
    if messages:
        return []
    shape = stable_suffix(
        job.route_pattern,
        job.protocol_family,
        job.request_content_type,
        job.response_content_type,
        str(job.request_body_size),
        str(job.response_body_size),
    )
    return [CoverageAlert(
        alert_id=coverage_alert_id("normalization_gap", job.route_pattern, shape),
        alert_code="normalization_gap",
        severity="high",
        method="POST",
        route_pattern=job.route_pattern,
        raw_path=job.route_pattern,
        content_type=job.request_content_type or job.response_content_type,
        protocol_family=job.protocol_family,
        payload_shape_hash=shape,
        normalizer=job.protocol_family,
        normalizer_version=NORMALIZER_VERSION,
        sample_trace_ids=[job.trace_id],
        message="route was marked raw_and_normalized but the worker extracted no normalized messages",
        affected_trace_count=1,
        affected_token_count=1 if job.token_fingerprint else 0,
        affected_employee_count=1 if job.employee_no else 0,
    )]


def _upstream_success(job: TraceCapturedJob) -> bool:
    status = job.upstream_status_code or job.status_code
    return 200 <= status < 400


def _max_repeated_prompt_count(messages: list[NormalizedMessage]) -> int:
    counts: dict[str, int] = {}
    for message in messages:
        if message.direction != "request":
            continue
        if message.role and message.role != "user":
            continue
        key = message.content_text_hash or message.content_text.strip().lower()
        if not key:
            continue
        counts[key] = counts.get(key, 0) + 1
    return max(counts.values(), default=0)


def _is_off_hours(value: str, offset_hours: int) -> bool:
    if not value:
        return False
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return False
    local_time = parsed.astimezone(timezone.utc) + timedelta(hours=offset_hours)
    return local_time.hour < 8 or local_time.hour >= 20


def _anomaly(
    job: TraceCapturedJob,
    anomaly_type: str,
    severity: str,
    observed_value: float,
    threshold_value: float,
    reason: str,
) -> AnomalyAlert:
    window_start = (
        bucket_start_hour(job.request_started_at)
        if job.request_started_at
        else MISSING_TIMESTAMP_WINDOW_START
    )
    window_end = (
        window_end_from_start(job.request_started_at)
        if job.request_started_at
        else MISSING_TIMESTAMP_WINDOW_END
    )
    return AnomalyAlert(
        anomaly_id=anomaly_id(anomaly_type, job.trace_id, job.employee_no),
        anomaly_type=anomaly_type,
        severity=severity,
        token_fingerprint=job.token_fingerprint,
        fingerprint_display=job.fingerprint_display,
        new_api_token_id=job.new_api_token_id,
        employee_no=job.employee_no,
        token_name_snapshot=job.token_name_snapshot,
        window_start=window_start,
        window_end=window_end,
        observed_value=observed_value,
        threshold_value=threshold_value,
        baseline_value=None,
        model=job.model_requested,
        route_pattern=job.route_pattern,
        sample_trace_ids=[job.trace_id],
        reason=reason,
        detector_version=DETECTOR_VERSION,
    )
