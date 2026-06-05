import json
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from enum import StrEnum
from hashlib import sha256
from typing import Any


class AnalysisStage(StrEnum):
    CORE = "core"
    ENRICHMENT = "enrichment"


class TaskStatus(StrEnum):
    QUEUED = "queued"
    LEASED = "leased"
    SUCCEEDED = "succeeded"
    FAILED_RETRYABLE = "failed_retryable"
    FAILED_TERMINAL = "failed_terminal"


class TraceStageStatus(StrEnum):
    PENDING = "pending"
    PROCESSING = "processing"
    COMPLETED = "completed"
    FAILED = "failed"
    NOT_REQUIRED = "not_required"


@dataclass(frozen=True)
class StreamEnvelope:
    trace_id: str
    stage: AnalysisStage = AnalysisStage.CORE
    enqueued_at: str = ""
    attempt: int = 1
    hints: dict[str, str] = field(default_factory=dict)


@dataclass(frozen=True)
class AnalysisTask:
    trace_id: str
    stage: AnalysisStage
    status: TaskStatus
    attempt_count: int
    max_attempts: int
    lease_owner: str = ""
    lease_expires_at: str = ""
    stream_name: str = ""
    stream_message_id: str = ""
    queued_at: str = ""
    started_at: str = ""
    completed_at: str = ""
    last_error_code: str = ""
    last_error_message: str = ""
    updated_at: str = ""


@dataclass(frozen=True)
class TraceCapturedJob:
    type: str
    trace_id: str
    route_pattern: str
    protocol_family: str
    capture_mode: str
    username: str
    request_raw_ref: str = ""
    request_headers_ref: str = ""
    response_raw_ref: str = ""
    response_headers_ref: str = ""
    request_content_type: str = ""
    response_content_type: str = ""
    model_requested: str = ""
    usage_prompt_tokens: int = 0
    usage_completion_tokens: int = 0
    usage_total_tokens: int = 0
    usage_reasoning_tokens: int = 0
    usage_cached_tokens: int = 0
    token_fingerprint: str = ""
    fingerprint_display: str = ""
    new_api_token_id: int = 0
    token_name_snapshot: str = ""
    identity_resolution_status: str = ""
    client_ip_hash: str = ""
    user_agent_hash: str = ""
    status_code: int = 0
    upstream_status_code: int = 0
    stream: bool = False
    request_started_at: str = ""
    request_body_size: int = 0
    response_body_size: int = 0


@dataclass(frozen=True)
class AnalysisContext:
    daily_tokens_before: int = 0
    daily_token_limit: int = 100_000
    short_window_tokens_before: int = 0
    short_window_token_threshold: int = 10_000
    expensive_models: set[str] | None = None
    expensive_model_token_threshold: int = 500
    long_output_token_threshold: int = 8_000
    repeated_prompt_threshold: int = 3
    local_timezone_offset_hours: int = 8
    off_hours_token_threshold: int = 2_000
    distinct_client_hashes_1h: int = 0
    token_leak_distinct_client_threshold: int = 3
    hourly_tokens_baseline: float | None = None
    hourly_tokens_mad: float | None = None
    short_window_baseline: float | None = None
    short_window_mad: float | None = None
    trace_effective_tokens_p95: float | None = None
    trace_tokens_p95: float | None = None
    completion_tokens_p95: float | None = None
    off_hours_baseline: float | None = None
    off_hours_mad: float | None = None
    model_baselines: dict[str, float] | None = None
    baseline_computed_at: str | None = None

    def expensive_model_set(self) -> set[str]:
        models = self.expensive_models or {"gpt-4.5-preview", "o1-pro"}
        return {model.strip().lower() for model in models if model.strip()}


@dataclass(frozen=True)
class NormalizedMessage:
    trace_id: str
    direction: str
    sequence_index: int
    role: str
    modality: str
    content_text: str
    content_text_hash: str
    media_url: str
    source_path: str
    protocol_item_type: str
    token_count_estimate: int
    metadata: dict[str, Any]


@dataclass(frozen=True)
class AnalysisResult:
    trace_id: str
    analyzer_name: str
    analyzer_version: str
    policy_version: str
    category: str
    label: str
    score: float
    confidence: float
    severity: str
    result: dict[str, Any]
    stage: str = ""
    producer: str = ""
    result_key: str = ""


@dataclass(frozen=True)
class UsageAggregateDelta:
    bucket_start: str
    bucket_size: str
    token_fingerprint: str
    new_api_token_id: int
    username: str
    token_name_snapshot: str
    model: str
    route_pattern: str
    protocol_family: str
    request_count: int
    success_count: int
    error_count: int
    stream_count: int
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int
    reasoning_tokens: int
    cached_tokens: int
    request_body_bytes: int
    response_body_bytes: int
    trace_id: str = ""
    request_started_at: str = ""


@dataclass(frozen=True)
class AnomalyAlert:
    anomaly_id: str
    anomaly_type: str
    severity: str
    token_fingerprint: str
    fingerprint_display: str
    new_api_token_id: int
    username: str
    token_name_snapshot: str
    window_start: str
    window_end: str
    observed_value: float
    threshold_value: float
    baseline_value: float | None
    model: str
    route_pattern: str
    sample_trace_ids: list[str]
    reason: str
    detector_version: str


@dataclass(frozen=True)
class CoverageAlert:
    alert_id: str
    alert_code: str
    severity: str
    method: str
    route_pattern: str
    raw_path: str
    content_type: str
    protocol_family: str
    payload_shape_hash: str
    normalizer: str
    normalizer_version: str
    sample_trace_ids: list[str]
    message: str
    affected_trace_count: int = 1
    affected_token_count: int = 0
    affected_user_count: int = 0


@dataclass(frozen=True)
class ContextCatalogEntry:
    id: int
    context_type: str
    name: str
    description: str
    keywords: list[str]
    aliases: list[str]
    owner: str
    expected_task_categories: list[str]
    expected_models: list[str]
    expected_usage_level: str
    active: bool

    def search_terms(self) -> list[str]:
        seen: set[str] = set()
        terms: list[str] = []
        for value in [*self.keywords, *self.aliases, self.name]:
            normalized = value.strip().lower()
            if normalized and normalized not in seen:
                seen.add(normalized)
                terms.append(normalized)
        return terms


@dataclass(frozen=True)
class WorkRelevanceAssessment:
    trace_id: str
    task_category: str
    work_related_score: float
    personal_use_score: float
    confidence: float
    matched_context: list[dict[str, Any]]
    evidence: list[Any]
    needs_review: bool
    analyzer_version: str
    decision: str = "unknown"
    recommended_action: str = "record_only"
    score_breakdown: dict[str, float] | None = None
    llm_judge_requested: bool = False
    llm_judge_reason: str = ""

    def to_analysis_result(
        self,
        *,
        stage: AnalysisStage | str = AnalysisStage.CORE,
        producer: str = "heuristic_work_relevance",
        result_key: str = "work_relevance_primary",
    ) -> AnalysisResult:
        score_breakdown = self.score_breakdown or {
            "work": self.work_related_score,
            "non_work": self.personal_use_score,
            "risk": 0.0,
            "conflict": 0.0,
            "uncertainty": max(0.0, 1.0 - self.confidence),
        }
        normalized_stage = stage.value if isinstance(stage, AnalysisStage) else str(stage or AnalysisStage.CORE.value)
        return AnalysisResult(
            trace_id=self.trace_id,
            analyzer_name="work_relevance",
            analyzer_version=self.analyzer_version,
            policy_version="",
            category="work_relevance",
            label=self.task_category,
            score=self.work_related_score,
            confidence=self.confidence,
            severity="review" if self.needs_review else "",
            result={
                "task_category": self.task_category,
                "work_related_score": self.work_related_score,
                "personal_use_score": self.personal_use_score,
                "confidence": self.confidence,
                "matched_context": self.matched_context,
                "evidence": self.evidence,
                "needs_review": self.needs_review,
                "decision": self.decision,
                "recommended_action": self.recommended_action,
                "score_breakdown": score_breakdown,
                "llm_judge_requested": self.llm_judge_requested,
                "llm_judge_reason": self.llm_judge_reason,
            },
            stage=normalized_stage,
            producer=producer,
            result_key=result_key,
        )


def stable_suffix(*parts: str) -> str:
    joined = "\x00".join(parts)
    return sha256(joined.encode("utf-8")).hexdigest()[:16]


def anomaly_id(rule_key: str, trace_id: str, username: str) -> str:
    return f"anom_{rule_key}_{stable_suffix(rule_key, trace_id, username)}"


def coverage_alert_id(alert_code: str, route_pattern: str, payload_shape_hash: str) -> str:
    return f"cov_{alert_code}_{stable_suffix(alert_code, route_pattern, payload_shape_hash)}"


def window_end_from_start(value: str, seconds: int = 60) -> str:
    if not value:
        return (datetime.now(timezone.utc) + timedelta(seconds=seconds)).isoformat()
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return (parsed.astimezone(timezone.utc) + timedelta(seconds=seconds)).isoformat()


def parse_job(line: str) -> TraceCapturedJob:
    data = json.loads(line)
    known = {field: data[field] for field in TraceCapturedJob.__dataclass_fields__ if field in data}
    return TraceCapturedJob(**known)


def text_hash(value: str) -> str:
    return sha256(value.encode("utf-8")).hexdigest()


def bucket_start_hour(value: str) -> str:
    if not value:
        return datetime.now(timezone.utc).replace(minute=0, second=0, microsecond=0).isoformat()
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return datetime(1970, 1, 1, tzinfo=timezone.utc).isoformat()
    return parsed.astimezone(timezone.utc).replace(minute=0, second=0, microsecond=0).isoformat()


def bucket_start_day(value: str) -> str:
    if not value:
        now = datetime.now(timezone.utc)
        return now.replace(hour=0, minute=0, second=0, microsecond=0).isoformat()
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return datetime(1970, 1, 1, tzinfo=timezone.utc).isoformat()
    return parsed.astimezone(timezone.utc).replace(hour=0, minute=0, second=0, microsecond=0).isoformat()
