from models import AnalysisResult, NormalizedMessage, UsageAggregateDelta
from repository import PostgresAnalysisRepository


class FakeCursor:
    def __init__(self):
        self.executed = []

    def execute(self, query, params):
        self.executed.append((query, params))


class FakeConnection:
    def __init__(self):
        self.cursor_obj = FakeCursor()
        self.committed = False

    def cursor(self):
        return self.cursor_obj

    def commit(self):
        self.committed = True


def test_repository_inserts_messages_results_and_aggregate():
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

    repo.save_trace_analysis([message], [result], [aggregate])

    queries = "\n".join(query for query, _ in conn.cursor_obj.executed)
    assert "INSERT INTO normalized_messages" in queries
    assert "INSERT INTO analysis_results" in queries
    assert "INSERT INTO usage_aggregates" in queries
    assert "ON CONFLICT" in queries
    assert conn.committed is True
