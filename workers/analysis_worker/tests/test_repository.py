from datetime import datetime, timedelta, timezone

from models import (
    AnalysisResult,
    AnomalyAlert,
    CoverageAlert,
    NormalizedMessage,
    TraceCapturedJob,
    UsageAggregateDelta,
)
from repository import PostgresAnalysisRepository


class SemanticCursor:
    def __init__(self, rows_by_trace=None, aggregate_rows=None, distinct_client_hashes=0):
        self.executed = []
        self.rows_by_trace = list(rows_by_trace or [])
        self.aggregate_rows = list(aggregate_rows or [])
        self.distinct_client_hashes = distinct_client_hashes
        self.next_row = None

    def execute(self, query, params):
        self.executed.append((query, params))
        if "FROM traces" in query and "usage_total_tokens" in query:
            token_fingerprint, window_end, window_end_again = params
            assert window_end == window_end_again
            parsed_window_end = datetime.fromisoformat(window_end.replace("Z", "+00:00"))
            window_start = parsed_window_end - timedelta(minutes=5)
            self.next_row = (sum(
                row["usage_total_tokens"]
                for row in self.rows_by_trace
                if row["token_fingerprint"] == token_fingerprint
                and window_start <= _parse_time(row["request_started_at"]) < parsed_window_end
            ),)
        elif "COUNT(DISTINCT" in query:
            self.next_row = (self.distinct_client_hashes,)
        elif self.aggregate_rows:
            self.next_row = self.aggregate_rows.pop(0)
        else:
            self.next_row = (0,)

    def fetchone(self):
        return self.next_row


class SemanticConnection:
    def __init__(self, cursor):
        self.cursor_obj = cursor

    def cursor(self):
        return self.cursor_obj


def _parse_time(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


class FakeCursor:
    def __init__(self, fetch_values=None):
        self.executed = []
        self.fetch_values = list(fetch_values or [])

    def execute(self, query, params):
        self.executed.append((query, params))

    def fetchone(self):
        if not self.fetch_values:
            return None
        return self.fetch_values.pop(0)


class FakeConnection:
    def __init__(self, fetch_values=None):
        self.cursor_obj = FakeCursor(fetch_values)
        self.committed = False

    def cursor(self):
        return self.cursor_obj

    def commit(self):
        self.committed = True


def test_repository_inserts_messages_results_aggregates_anomalies_and_coverage():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)
    message = NormalizedMessage(
        trace_id="trace_1",
        direction="request",
        sequence_index=0,
        role="user",
        modality="text",
        content_text="Summarize incident",
        content_text_hash="abc",
        media_url="",
        source_path="request.messages[0]",
        protocol_item_type="openai_chat_message",
        token_count_estimate=2,
        metadata={"protocol_family": "openai_chat"},
    )
    result = AnalysisResult(
        trace_id="trace_1",
        analyzer_name="usage_extraction",
        analyzer_version="normalizer_mvp_2026_04_28",
        policy_version="",
        category="usage_extraction",
        label="usage_from_gateway_job",
        score=18,
        confidence=1.0,
        severity="",
        result={"total_tokens": 18},
    )
    work_relevance_result = AnalysisResult(
        trace_id="trace_1",
        analyzer_name="work_relevance",
        analyzer_version="work_relevance_mvp_2026_04_28",
        policy_version="",
        category="work_relevance",
        label="debugging",
        score=0.9,
        confidence=0.8,
        severity="",
        result={
            "task_category": "debugging",
            "work_related_score": 0.9,
            "personal_use_score": 0.02,
            "confidence": 0.8,
            "matched_context": [{"type": "repo", "name": "new-api-gateway", "matched_terms": ["gateway"]}],
            "evidence": ["Matched catalog context and work category debugging."],
            "needs_review": False,
        },
    )
    aggregate = UsageAggregateDelta(
        bucket_start="2026-04-28T13:00:00+00:00",
        bucket_size="hour",
        token_fingerprint="tkfp_raw",
        new_api_token_id=42,
        employee_no="E10001",
        token_name_snapshot="E10001",
        model="gpt-4.1",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        request_count=1,
        success_count=1,
        error_count=0,
        stream_count=0,
        prompt_tokens=11,
        completion_tokens=7,
        total_tokens=18,
        reasoning_tokens=2,
        cached_tokens=3,
        request_body_bytes=0,
        response_body_bytes=0,
    )
    anomaly = AnomalyAlert(
        anomaly_id="anom_high_trace_tokens_abc",
        anomaly_type="high_trace_tokens",
        severity="medium",
        token_fingerprint="tkfp_raw",
        fingerprint_display="tkfp_display",
        new_api_token_id=42,
        employee_no="E10001",
        token_name_snapshot="E10001",
        window_start="2026-04-28T13:00:00+00:00",
        window_end="2026-04-28T13:46:22+00:00",
        observed_value=25000,
        threshold_value=20000,
        baseline_value=None,
        model="gpt-4.1",
        route_pattern="/v1/chat/completions",
        sample_trace_ids=["trace_1"],
        reason="single trace exceeded threshold",
        detector_version="rules_mvp_2026_04_28",
    )
    coverage = CoverageAlert(
        alert_id="cov_normalization_gap_abc",
        alert_code="normalization_gap",
        severity="high",
        method="POST",
        route_pattern="/v1/chat/completions",
        raw_path="/v1/chat/completions",
        content_type="application/json",
        protocol_family="openai_chat",
        payload_shape_hash="shape123",
        normalizer="openai_chat",
        normalizer_version="normalizer_mvp_2026_04_28",
        sample_trace_ids=["trace_1"],
        message="no normalized messages",
        affected_trace_count=1,
        affected_token_count=1,
        affected_employee_count=1,
    )

    repo.save_trace_analysis([message], [result, work_relevance_result], [aggregate], [anomaly], [coverage])

    queries = "\n".join(query for query, _ in conn.cursor_obj.executed)
    assert "INSERT INTO normalized_messages" in queries
    assert "INSERT INTO analysis_results" in queries
    assert "INSERT INTO usage_aggregates" in queries
    assert "INSERT INTO usage_anomalies" in queries
    assert "INSERT INTO coverage_alerts" in queries
    assert "ON CONFLICT" in queries
    assert conn.committed is True

    coverage_queries = [
        query for query, _ in conn.cursor_obj.executed if "INSERT INTO coverage_alerts" in query
    ]
    assert len(coverage_queries) == 1
    coverage_query = coverage_queries[0]
    assert "coverage_alerts.occurrence_count + 1" in coverage_query
    assert "SELECT DISTINCT unnest(coverage_alerts.sample_trace_ids || EXCLUDED.sample_trace_ids)" in coverage_query
    assert (
        "affected_trace_count = coverage_alerts.affected_trace_count + EXCLUDED.affected_trace_count"
        not in coverage_query
    )
    analysis_queries = [
        query for query, _ in conn.cursor_obj.executed if "INSERT INTO analysis_results" in query
    ]
    assert len(analysis_queries) == 2


def test_repository_loads_analysis_context_from_aggregates_and_recent_trace_hashes():
    conn = FakeConnection(fetch_values=[(90000,), (7000,), (3,)])
    repo = PostgresAnalysisRepository(conn)
    job = TraceCapturedJob(
        type="trace_captured",
        trace_id="trace_1",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        employee_no="E10001",
        token_fingerprint="tkfp_raw",
        request_started_at="2026-04-28T13:45:22Z",
    )

    context = repo.analysis_context_for(job)

    assert context.daily_tokens_before == 90000
    assert context.short_window_tokens_before == 7000
    assert context.distinct_client_hashes_1h == 3
    queries = "\n".join(query for query, _ in conn.cursor_obj.executed)
    assert "bucket_size = 'day'" in queries
    assert "SUM(usage_total_tokens)" in queries
    assert "interval '5 minutes'" in queries
    assert "client_ip_hash" in queries
    assert "user_agent_hash" in queries


def test_repository_loads_short_window_context_from_previous_5_minutes_of_traces():
    cursor = SemanticCursor(
        aggregate_rows=[(97000,)],
        distinct_client_hashes=2,
        rows_by_trace=[
            {
                "token_fingerprint": "tkfp_raw",
                "employee_no": "E10001",
                "request_started_at": "2026-04-28T13:40:21Z",
                "usage_total_tokens": 9000,
            },
            {
                "token_fingerprint": "tkfp_raw",
                "employee_no": "E10001",
                "request_started_at": "2026-04-28T13:40:22Z",
                "usage_total_tokens": 300,
            },
            {
                "token_fingerprint": "tkfp_raw",
                "employee_no": "E10001",
                "request_started_at": "2026-04-28T13:44:22Z",
                "usage_total_tokens": 450,
            },
            {
                "token_fingerprint": "tkfp_raw",
                "employee_no": "E10001",
                "request_started_at": "2026-04-28T13:45:22Z",
                "usage_total_tokens": 2000,
            },
            {
                "token_fingerprint": "other_token",
                "employee_no": "E10001",
                "request_started_at": "2026-04-28T13:44:22Z",
                "usage_total_tokens": 7000,
            },
            {
                "token_fingerprint": "tkfp_raw",
                "employee_no": "E99999",
                "request_started_at": "2026-04-28T13:44:22Z",
                "usage_total_tokens": 8000,
            },
        ],
    )
    repo = PostgresAnalysisRepository(SemanticConnection(cursor))
    job = TraceCapturedJob(
        type="trace_captured",
        trace_id="trace_current",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        employee_no="E10001",
        token_fingerprint="tkfp_raw",
        usage_total_tokens=2000,
        request_started_at="2026-04-28T13:45:22Z",
    )

    context = repo.analysis_context_for(job)

    assert context.daily_tokens_before == 97000
    assert context.short_window_tokens_before == 8750
    assert context.distinct_client_hashes_1h == 2


def test_repository_returns_default_context_without_querying_for_empty_token_fingerprint():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)
    job = TraceCapturedJob(
        type="trace_captured",
        trace_id="trace_empty_token",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        employee_no="E10001",
        token_fingerprint="",
        request_started_at="2026-04-28T13:45:22Z",
    )

    context = repo.analysis_context_for(job)

    assert context.daily_tokens_before == 0
    assert context.short_window_tokens_before == 0
    assert context.distinct_client_hashes_1h == 0
    assert conn.cursor_obj.executed == []
