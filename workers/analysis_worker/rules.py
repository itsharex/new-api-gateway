from datetime import datetime, timedelta, timezone

from models import (
    AnalysisContext,
    AnomalyAlert,
    CoverageAlert,
    NormalizedMessage,
    TraceCapturedJob,
    WorkRelevanceAssessment,
    anomaly_id,
    coverage_alert_id,
    stable_suffix,
)


DETECTOR_VERSION = "rules_mvp_2026_04_28"
NORMALIZER_VERSION = "normalizer_mvp_2026_04_28"

HIGH_TRACE_TOKEN_THRESHOLD = 40_000
LONG_OUTPUT_TOKEN_THRESHOLD = 16_000
OFF_HOURS_TOKEN_THRESHOLD = 20_000
MISSING_TIMESTAMP_WINDOW_START = "1970-01-01T00:00:00+00:00"
MISSING_TIMESTAMP_WINDOW_END = "1970-01-01T00:01:00+00:00"


def _effective_tokens(job: TraceCapturedJob) -> int:
    return max(job.usage_prompt_tokens - job.usage_cached_tokens, 0) + job.usage_completion_tokens


def detect_anomalies(
    job: TraceCapturedJob,
    messages: list[NormalizedMessage] | None = None,
    context: AnalysisContext | None = None,
) -> list[AnomalyAlert]:
    context = context or AnalysisContext()
    alerts: list[AnomalyAlert] = []
    effective_tokens = _effective_tokens(job)

    trace_threshold = HIGH_TRACE_TOKEN_THRESHOLD
    trace_baseline = context.trace_effective_tokens_p95
    if trace_baseline is not None:
        trace_threshold = max(trace_baseline * 1.5, HIGH_TRACE_TOKEN_THRESHOLD)
    if effective_tokens >= trace_threshold:
        alerts.append(_anomaly(
            job,
            "high_trace_tokens",
            "medium",
            observed_value=effective_tokens,
            threshold_value=trace_threshold,
            reason=(
                f"effective token usage reached {effective_tokens}, "
                f"meeting or exceeding {trace_threshold:.0f}"
            ),
            baseline_value=trace_baseline,
        ))

    output_threshold = LONG_OUTPUT_TOKEN_THRESHOLD
    output_baseline = context.completion_tokens_p95
    if output_baseline is not None:
        output_threshold = max(output_baseline * 1.5, LONG_OUTPUT_TOKEN_THRESHOLD)
    if job.usage_completion_tokens >= output_threshold:
        alerts.append(_anomaly(
            job,
            "long_output_anomaly",
            "medium",
            observed_value=job.usage_completion_tokens,
            threshold_value=output_threshold,
            reason=(
                f"completion tokens reached {job.usage_completion_tokens}, "
                f"meeting or exceeding {output_threshold:.0f}"
            ),
            baseline_value=output_baseline,
        ))

    if (
        _is_off_hours(job.request_started_at, context.local_timezone_offset_hours)
        and effective_tokens >= OFF_HOURS_TOKEN_THRESHOLD
    ):
        alerts.append(_anomaly(
            job,
            "off_hours_high_usage",
            "medium",
            observed_value=effective_tokens,
            threshold_value=OFF_HOURS_TOKEN_THRESHOLD,
            reason=(
                f"off-hours effective token usage reached {effective_tokens}, "
                f"meeting or exceeding {OFF_HOURS_TOKEN_THRESHOLD}"
            ),
        ))
    return alerts


def detect_work_relevance_anomalies(
    job: TraceCapturedJob,
    assessment: WorkRelevanceAssessment,
) -> list[AnomalyAlert]:
    action = getattr(assessment, "recommended_action", "")

    if action == "alert_non_work":
        return [_anomaly(
            job,
            "non_work_use",
            "high",
            observed_value=job.usage_total_tokens,
            threshold_value=0,
            reason=_work_relevance_reason(
                assessment,
                "trace was classified as explicit non-work use",
            ),
        )]
    return []


def _work_relevance_reason(assessment: WorkRelevanceAssessment, fallback: str) -> str:
    evidence = getattr(assessment, "evidence", []) or []
    reasons: list[str] = []
    for item in evidence:
        if isinstance(item, dict) and item.get("reason"):
            reasons.append(str(item["reason"]))
        elif isinstance(item, str):
            reasons.append(item)
    detail = "; ".join(reasons[:3])
    if detail:
        return f"{fallback}: {detail}"
    return fallback


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
        affected_user_count=1 if job.username else 0,
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
    parsed = _parse_utc(value)
    if parsed is None:
        return False
    local_time = parsed.astimezone(timezone.utc) + timedelta(hours=offset_hours)
    return local_time.hour >= 23 or local_time.hour < 7


def _parse_utc(value: str) -> datetime | None:
    if not value:
        return None
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)
    except ValueError:
        return None


def _day_window(value: str) -> tuple[str | None, str | None]:
    parsed = _parse_utc(value)
    if parsed is None:
        return None, None
    start = parsed.replace(hour=0, minute=0, second=0, microsecond=0)
    end = start + timedelta(days=1)
    return start.isoformat(), end.isoformat()


def _relative_window(value: str, seconds: int) -> tuple[str | None, str | None]:
    parsed = _parse_utc(value)
    if parsed is None:
        return None, None
    return (parsed - timedelta(seconds=seconds)).isoformat(), parsed.isoformat()


def _default_anomaly_window(value: str) -> tuple[str, str]:
    parsed = _parse_utc(value)
    if parsed is None:
        return MISSING_TIMESTAMP_WINDOW_START, MISSING_TIMESTAMP_WINDOW_END
    return (
        parsed.replace(minute=0, second=0, microsecond=0).isoformat(),
        (parsed + timedelta(seconds=60)).isoformat(),
    )


def _anomaly(
    job: TraceCapturedJob,
    anomaly_type: str,
    severity: str,
    observed_value: float,
    threshold_value: float,
    reason: str,
    window_start: str | None = None,
    window_end: str | None = None,
    baseline_value: float | None = None,
) -> AnomalyAlert:
    default_window_start, default_window_end = _default_anomaly_window(job.request_started_at)
    resolved_window_start = window_start or default_window_start
    resolved_window_end = window_end or default_window_end
    return AnomalyAlert(
        anomaly_id=anomaly_id(anomaly_type, job.trace_id, job.username),
        anomaly_type=anomaly_type,
        severity=severity,
        token_fingerprint=job.token_fingerprint,
        fingerprint_display=job.fingerprint_display,
        new_api_token_id=job.new_api_token_id,
        username=job.username,
        token_name_snapshot=job.token_name_snapshot,
        window_start=resolved_window_start,
        window_end=resolved_window_end,
        observed_value=observed_value,
        threshold_value=threshold_value,
        baseline_value=baseline_value,
        model=job.model_requested,
        route_pattern=job.route_pattern,
        sample_trace_ids=[job.trace_id],
        reason=reason,
        detector_version=DETECTOR_VERSION,
    )
