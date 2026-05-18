from datetime import datetime, timedelta, timezone
from pathlib import Path

from media_extraction import MediaAsset
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
    def __init__(self, rows_by_trace=None, aggregate_rows=None, client_rows=None, distinct_client_hashes=0, baseline_rows=None):
        self.executed = []
        self.rows_by_trace = list(rows_by_trace or [])
        self.aggregate_rows = list(aggregate_rows or [])
        self.client_rows = list(client_rows or [])
        self.distinct_client_hashes = distinct_client_hashes
        self.baseline_rows = list(baseline_rows or [])
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
            if self.client_rows:
                token_fingerprint, window_end, window_end_again, current_client_ip, current_user_agent = params
                assert window_end == window_end_again
                parsed_window_end = datetime.fromisoformat(window_end.replace("Z", "+00:00"))
                window_start = parsed_window_end - timedelta(hours=1)
                distinct_clients = {
                    (row["client_ip_hash"], row["user_agent_hash"])
                    for row in self.client_rows
                    if row["token_fingerprint"] == token_fingerprint
                    and window_start <= _parse_time(row["request_started_at"]) <= parsed_window_end
                    and (row["client_ip_hash"] or row["user_agent_hash"])
                }
                if current_client_ip or current_user_agent:
                    distinct_clients.add((current_client_ip, current_user_agent))
                self.next_row = (len(distinct_clients),)
            else:
                self.next_row = (self.distinct_client_hashes,)
        elif self.aggregate_rows:
            self.next_row = self.aggregate_rows.pop(0)
        else:
            self.next_row = (0,)

    def fetchone(self):
        return self.next_row

    def fetchall(self):
        if "baseline_cache" in (self.executed[-1][0] if self.executed else ""):
            return self.baseline_rows
        return []


class SemanticConnection:
    def __init__(self, cursor):
        self.cursor_obj = cursor

    def cursor(self):
        return self.cursor_obj


def _parse_time(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


class FakeCursor:
    def __init__(self, fetch_values=None, fetchall_rows=None):
        self.executed = []
        self.fetch_values = list(fetch_values or [])
        self.fetchall_rows = list(fetchall_rows or [])

    def execute(self, query, params):
        self.executed.append((query, params))

    def fetchone(self):
        if not self.fetch_values:
            return None
        return self.fetch_values.pop(0)

    def fetchall(self):
        if not self.fetchall_rows:
            return []
        return self.fetchall_rows.pop(0)


class FakeConnection:
    def __init__(self, fetch_values=None, fetchall_rows=None):
        self.cursor_obj = FakeCursor(fetch_values, fetchall_rows)
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
        username="alice",
        token_name_snapshot="alice",
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
        username="alice",
        token_name_snapshot="alice",
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
        affected_user_count=1,
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
        username="alice",
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
                "username": "alice",
                "request_started_at": "2026-04-28T13:40:21Z",
                "usage_total_tokens": 9000,
            },
            {
                "token_fingerprint": "tkfp_raw",
                "username": "alice",
                "request_started_at": "2026-04-28T13:40:22Z",
                "usage_total_tokens": 300,
            },
            {
                "token_fingerprint": "tkfp_raw",
                "username": "alice",
                "request_started_at": "2026-04-28T13:44:22Z",
                "usage_total_tokens": 450,
            },
            {
                "token_fingerprint": "tkfp_raw",
                "username": "alice",
                "request_started_at": "2026-04-28T13:45:22Z",
                "usage_total_tokens": 2000,
            },
            {
                "token_fingerprint": "other_token",
                "username": "alice",
                "request_started_at": "2026-04-28T13:44:22Z",
                "usage_total_tokens": 7000,
            },
            {
                "token_fingerprint": "tkfp_raw",
                "username": "bob",
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
        username="alice",
        token_fingerprint="tkfp_raw",
        usage_total_tokens=2000,
        request_started_at="2026-04-28T13:45:22Z",
    )

    context = repo.analysis_context_for(job)

    assert context.daily_tokens_before == 97000
    assert context.short_window_tokens_before == 8750
    assert context.distinct_client_hashes_1h == 2


def test_repository_counts_current_job_client_tuple_for_token_leak_context():
    cursor = SemanticCursor(
        aggregate_rows=[(0,)],
        rows_by_trace=[],
        client_rows=[
            {
                "token_fingerprint": "tkfp_raw",
                "request_started_at": "2026-04-28T13:20:00Z",
                "client_ip_hash": "client_a",
                "user_agent_hash": "ua_a",
            },
            {
                "token_fingerprint": "tkfp_raw",
                "request_started_at": "2026-04-28T13:45:22.500Z",
                "client_ip_hash": "client_late",
                "user_agent_hash": "ua_late",
            },
            {
                "token_fingerprint": "tkfp_raw",
                "request_started_at": "2026-04-28T13:44:00Z",
                "client_ip_hash": "client_b",
                "user_agent_hash": "ua_b",
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
        username="alice",
        token_fingerprint="tkfp_raw",
        client_ip_hash="client_current",
        user_agent_hash="ua_current",
        request_started_at="2026-04-28T13:45:22Z",
    )

    context = repo.analysis_context_for(job)

    assert context.distinct_client_hashes_1h == 3


def test_repository_returns_default_context_without_querying_for_empty_token_fingerprint():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)
    job = TraceCapturedJob(
        type="trace_captured",
        trace_id="trace_empty_token",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        username="alice",
        token_fingerprint="",
        request_started_at="2026-04-28T13:45:22Z",
    )

    context = repo.analysis_context_for(job)

    assert context.daily_tokens_before == 0
    assert context.short_window_tokens_before == 0
    assert context.distinct_client_hashes_1h == 0
    assert conn.cursor_obj.executed == []


def test_repository_returns_default_context_without_querying_for_malformed_timestamp():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)
    job = TraceCapturedJob(
        type="trace_captured",
        trace_id="trace_bad_timestamp",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        username="alice",
        token_fingerprint="tkfp_raw",
        request_started_at="not-a-timestamp",
    )

    context = repo.analysis_context_for(job)

    assert context.daily_tokens_before == 0
    assert context.short_window_tokens_before == 0
    assert context.distinct_client_hashes_1h == 0
    assert conn.cursor_obj.executed == []


def test_repository_queues_media_snapshot_jobs_for_media_urls():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)
    media_message = NormalizedMessage(
        trace_id="trace_media",
        direction="request",
        sequence_index=0,
        role="user",
        modality="image",
        content_text="",
        content_text_hash="",
        media_url="https://example.test/image.png",
        source_path="request.messages[0].content[1]",
        protocol_item_type="media_url",
        token_count_estimate=0,
        metadata={"protocol_family": "openai_chat"},
    )

    repo.save_trace_analysis([media_message], [], [], [], [])

    media_queries = [
        (query, params)
        for query, params in conn.cursor_obj.executed
        if "INSERT INTO media_snapshot_jobs" in query
    ]
    assert len(media_queries) == 1
    assert media_queries[0][1] == (
        "trace_media",
        "https://example.test/image.png",
        "request.messages[0].content[1]",
        "generated_or_referenced_media",
    )
    assert "ON CONFLICT (trace_id, source_url, source_context, policy_reason) DO NOTHING" in media_queries[0][0]


def test_repository_skips_obvious_non_http_media_urls():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)
    media_message = NormalizedMessage(
        trace_id="trace_media",
        direction="request",
        sequence_index=0,
        role="user",
        modality="image",
        content_text="",
        content_text_hash="",
        media_url="data:image/png;base64,abc",
        source_path="request.messages[0].content[1]",
        protocol_item_type="media_url",
        token_count_estimate=0,
        metadata={"protocol_family": "openai_chat"},
    )

    repo.save_trace_analysis([media_message], [], [], [], [])

    media_queries = [
        query for query, _ in conn.cursor_obj.executed if "INSERT INTO media_snapshot_jobs" in query
    ]
    assert media_queries == []


def test_media_snapshot_upgrade_migration_defines_idempotent_job_key():
    migrations_dir = Path(__file__).parents[3] / "migrations"
    initial_migration = (migrations_dir / "0011_media_snapshot_jobs.sql").read_text(encoding="utf-8")
    upgrade_migration = (migrations_dir / "0012_media_snapshot_job_uniqueness.sql").read_text(encoding="utf-8")

    assert "idx_media_snapshot_jobs_unique_source" not in initial_migration
    assert "WITH ranked_duplicates AS" in upgrade_migration
    assert "DELETE FROM media_snapshot_jobs" in upgrade_migration
    assert "ROW_NUMBER() OVER" in upgrade_migration
    assert "CREATE UNIQUE INDEX IF NOT EXISTS idx_media_snapshot_jobs_unique_source" in upgrade_migration
    assert "trace_id, source_url, source_context, policy_reason" in upgrade_migration


def test_repository_inserts_media_asset_records():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)
    assets = [
        MediaAsset(
            object_type="media_asset_000001",
            object_ref="raw/2026/05/05/trace_1/media_asset_000001.bin",
            media_type="image/png",
            size_bytes=12345,
        ),
        MediaAsset(
            object_type="media_asset_000002",
            object_ref="raw/2026/05/05/trace_1/media_asset_000002.bin",
            media_type="audio/wav",
            size_bytes=2048,
        ),
    ]

    repo.save_media_assets("trace_1", assets)

    media_queries = [
        (query, params)
        for query, params in conn.cursor_obj.executed
        if "INSERT INTO raw_evidence_objects" in query
    ]
    assert len(media_queries) == 2
    assert media_queries[0][1][:3] == (
        "trace_1",
        "media_asset_000001",
        "raw/2026/05/05/trace_1/media_asset_000001.bin",
    )
    assert media_queries[1][1][:3] == (
        "trace_1",
        "media_asset_000002",
        "raw/2026/05/05/trace_1/media_asset_000002.bin",
    )


def test_repository_skips_media_assets_when_empty():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)

    repo.save_media_assets("trace_1", [])

    media_queries = [
        query for query, _ in conn.cursor_obj.executed
        if "INSERT INTO raw_evidence_objects" in query
    ]
    assert media_queries == []


def test_repository_updates_request_body_sha256():
    conn = FakeConnection()
    repo = PostgresAnalysisRepository(conn)

    repo.update_request_body_sha256("trace_1", "abc123sha256")

    sha_queries = [
        (query, params) for query, params in conn.cursor_obj.executed
        if "UPDATE traces" in query and "request_body_sha256" in query
    ]
    assert len(sha_queries) == 1
    assert sha_queries[0][1] == ("abc123sha256", "trace_1")


def test_analysis_context_for_loads_baselines_from_cache():
    from datetime import datetime, timezone

    computed = datetime(2026, 5, 18, 12, 0, 0, tzinfo=timezone.utc)
    baseline_rows = [
        ("trace_tokens_p95", 25000.0, {}, computed),
        ("hourly_tokens_median", 3000.0, {}, computed),
        ("model_hourly_median_o1-pro", 600.0, {}, computed),
    ]
    conn = FakeConnection(
        fetch_values=[(0,), (0,), (0,)],
        fetchall_rows=[baseline_rows],
    )
    repo = PostgresAnalysisRepository(conn)
    job = TraceCapturedJob(
        type="trace_captured",
        trace_id="t1",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        username="alice",
        token_fingerprint="tkfp_raw",
        request_started_at="2026-05-18T10:00:00Z",
    )

    context = repo.analysis_context_for(job)

    assert context.trace_tokens_p95 == 25000.0
    assert context.hourly_tokens_baseline == 3000.0
    assert context.model_baselines == {"o1-pro": 600.0}
    assert context.baseline_computed_at is not None

    queries = "\n".join(query for query, _ in conn.cursor_obj.executed)
    assert "baseline_cache" in queries
    assert "expires_at > now()" in queries


def test_analysis_context_for_ignores_expired_baselines():
    conn = FakeConnection(
        fetch_values=[(0,), (0,), (0,)],
        fetchall_rows=[[]],
    )
    repo = PostgresAnalysisRepository(conn)
    job = TraceCapturedJob(
        type="trace_captured",
        trace_id="t1",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        username="alice",
        token_fingerprint="tkfp_raw",
        request_started_at="2026-05-18T10:00:00Z",
    )

    context = repo.analysis_context_for(job)

    assert context.trace_tokens_p95 is None
    assert context.baseline_computed_at is None


def test_analysis_context_for_maps_all_baseline_metric_types():
    from datetime import datetime, timezone

    computed = datetime(2026, 5, 18, 12, 0, 0, tzinfo=timezone.utc)
    baseline_rows = [
        ("hourly_tokens_median", 1000.0, {}, computed),
        ("hourly_tokens_mad", 200.0, {}, computed),
        ("short_window_baseline", 500.0, {}, computed),
        ("short_window_mad", 50.0, {}, computed),
        ("trace_tokens_p95", 8000.0, {}, computed),
        ("completion_tokens_p95", 3000.0, {}, computed),
        ("off_hours_baseline", 400.0, {}, computed),
        ("off_hours_mad", 80.0, {}, computed),
        ("model_hourly_median_gpt-4.1", 700.0, {}, computed),
        ("model_hourly_median_o3", 300.0, {}, computed),
    ]
    conn = FakeConnection(
        fetch_values=[(0,), (0,), (0,)],
        fetchall_rows=[baseline_rows],
    )
    repo = PostgresAnalysisRepository(conn)
    job = TraceCapturedJob(
        type="trace_captured",
        trace_id="t1",
        route_pattern="/v1/chat/completions",
        protocol_family="openai_chat",
        capture_mode="raw_and_normalized",
        username="alice",
        token_fingerprint="tkfp_raw",
        request_started_at="2026-05-18T10:00:00Z",
    )

    context = repo.analysis_context_for(job)

    assert context.hourly_tokens_baseline == 1000.0
    assert context.hourly_tokens_mad == 200.0
    assert context.short_window_baseline == 500.0
    assert context.short_window_mad == 50.0
    assert context.trace_tokens_p95 == 8000.0
    assert context.completion_tokens_p95 == 3000.0
    assert context.off_hours_baseline == 400.0
    assert context.off_hours_mad == 80.0
    assert context.model_baselines == {"gpt-4.1": 700.0, "o3": 300.0}
    assert context.baseline_computed_at is not None
