import json

from baseline import (
    BaselineRow,
    QUERY_HOURLY,
    QUERY_MODEL_HOURLY,
    QUERY_TRACE_LEVEL,
    compute_hourly_baselines,
    compute_model_baselines,
    compute_trace_level_baselines,
    upsert_baselines,
)


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


# -- compute_hourly_baselines --


def test_compute_hourly_baselines_returns_one_row_per_fingerprint():
    rows = [
        {"fingerprint_key": "fp_a", "hourly_total": 5000.0, "hour_count": 10},
        {"fingerprint_key": "fp_b", "hourly_total": 12000.5, "hour_count": 6},
    ]

    result = compute_hourly_baselines(rows)

    assert len(result) == 2
    assert result[0] == BaselineRow(
        fingerprint_key="fp_a",
        metric_type="hourly_tokens_median",
        metric_value=5000.0,
        metadata_json={"hour_count": 10},
    )
    assert result[1] == BaselineRow(
        fingerprint_key="fp_b",
        metric_type="hourly_tokens_median",
        metric_value=12000.5,
        metadata_json={"hour_count": 6},
    )


def test_compute_hourly_baselines_returns_empty_for_no_rows():
    result = compute_hourly_baselines([])
    assert result == []


# -- compute_trace_level_baselines --


def test_compute_trace_level_baselines_returns_trace_effective_and_completion_rows():
    rows = [
        {"fingerprint_key": "fp_a", "p95_effective": 18000.0, "p95_completion": 3000.0},
    ]

    result = compute_trace_level_baselines(rows)

    assert result[0] == BaselineRow(
        fingerprint_key="fp_a",
        metric_type="trace_effective_tokens_p95",
        metric_value=18000.0,
        metadata_json={},
    )
    assert result[1] == BaselineRow(
        fingerprint_key="fp_a",
        metric_type="completion_tokens_p95",
        metric_value=3000.0,
        metadata_json={},
    )


def test_compute_trace_level_baselines_returns_multiple_fingerprints():
    rows = [
        {"fingerprint_key": "fp_a", "p95_effective": 100.0, "p95_completion": 50.0},
        {"fingerprint_key": "fp_b", "p95_effective": 200.0, "p95_completion": 80.0},
    ]

    result = compute_trace_level_baselines(rows)

    assert len(result) == 4
    assert result[0].metric_type == "trace_effective_tokens_p95"
    assert result[0].fingerprint_key == "fp_a"
    assert result[1].metric_type == "completion_tokens_p95"
    assert result[1].fingerprint_key == "fp_a"
    assert result[2].metric_type == "trace_effective_tokens_p95"
    assert result[2].fingerprint_key == "fp_b"
    assert result[3].metric_type == "completion_tokens_p95"
    assert result[3].fingerprint_key == "fp_b"


def test_compute_trace_level_baselines_returns_empty_for_no_rows():
    result = compute_trace_level_baselines([])
    assert result == []


# -- compute_model_baselines --


def test_compute_model_baselines_returns_one_row_per_input():
    rows = [
        {"fingerprint_key": "fp_a", "model": "gpt-4.1", "median_hourly": 5000.0},
        {"fingerprint_key": "fp_a", "model": "o3", "median_hourly": 2000.0},
        {"fingerprint_key": "fp_b", "model": "gpt-4.1", "median_hourly": 8000.0},
    ]

    result = compute_model_baselines(rows)

    assert len(result) == 3
    assert result[0] == BaselineRow(
        fingerprint_key="fp_a",
        metric_type="model_hourly_median_gpt-4.1",
        metric_value=5000.0,
        metadata_json={"model": "gpt-4.1"},
    )
    assert result[1] == BaselineRow(
        fingerprint_key="fp_a",
        metric_type="model_hourly_median_o3",
        metric_value=2000.0,
        metadata_json={"model": "o3"},
    )
    assert result[2] == BaselineRow(
        fingerprint_key="fp_b",
        metric_type="model_hourly_median_gpt-4.1",
        metric_value=8000.0,
        metadata_json={"model": "gpt-4.1"},
    )


def test_compute_model_baselines_returns_empty_for_no_rows():
    result = compute_model_baselines([])
    assert result == []


# -- upsert_baselines --


def test_upsert_baselines_inserts_all_rows_and_commits():
    conn = FakeConnection()
    baselines = [
        BaselineRow("fp_a", "hourly_tokens_median", 5000.0, {"hour_count": 8}),
        BaselineRow("fp_a", "trace_effective_tokens_p95", 12000.0, {}),
    ]

    upsert_baselines(conn, baselines, ttl_hours=25)

    assert conn.committed is True
    assert len(conn.cursor_obj.executed) == 2

    query0, params0 = conn.cursor_obj.executed[0]
    assert "INSERT INTO baseline_cache" in query0
    assert "ON CONFLICT (fingerprint_key, metric_type) DO UPDATE" in query0
    assert params0[0] == "fp_a"
    assert params0[1] == "hourly_tokens_median"
    assert params0[2] == 5000.0
    assert json.loads(params0[3]) == {"hour_count": 8}
    assert params0[4] == "25"

    query1, params1 = conn.cursor_obj.executed[1]
    assert params1[0] == "fp_a"
    assert params1[1] == "trace_effective_tokens_p95"
    assert params1[2] == 12000.0


def test_upsert_baselines_uses_custom_ttl():
    conn = FakeConnection()
    baselines = [
        BaselineRow("fp_a", "hourly_tokens_median", 100.0, {}),
    ]

    upsert_baselines(conn, baselines, ttl_hours=48)

    _, params = conn.cursor_obj.executed[0]
    assert params[4] == "48"


def test_upsert_baselines_skips_empty_list():
    conn = FakeConnection()

    upsert_baselines(conn, [])

    assert conn.cursor_obj.executed == []
    assert conn.committed is False


def test_upsert_baselines_json_serializes_metadata():
    conn = FakeConnection()
    baselines = [
        BaselineRow("fp_a", "test_metric", 42.0, {"model": "gpt-4.1", "count": 5}),
    ]

    upsert_baselines(conn, baselines)

    _, params = conn.cursor_obj.executed[0]
    parsed = json.loads(params[3])
    assert parsed == {"count": 5, "model": "gpt-4.1"}


# -- SQL query constants --


def test_query_constants_use_parameterized_lookback():
    assert "%s" in QUERY_HOURLY
    assert "%s" in QUERY_TRACE_LEVEL
    assert "%s" in QUERY_MODEL_HOURLY


def test_query_hourly_groups_by_fingerprint():
    assert "PERCENTILE_CONT(0.5)" in QUERY_HOURLY
    assert "HAVING COUNT(*) >= 3" in QUERY_HOURLY
    assert "bucket_size = 'hour'" in QUERY_HOURLY


def test_query_trace_level_uses_trace_effective_tokens_formula():
    assert "PERCENTILE_CONT(0.95)" in QUERY_TRACE_LEVEL
    assert "HAVING COUNT(*) >= 5" in QUERY_TRACE_LEVEL
    assert "GREATEST(usage_prompt_tokens - usage_cached_tokens, 0) + usage_completion_tokens" in QUERY_TRACE_LEVEL
    assert "usage_completion_tokens" in QUERY_TRACE_LEVEL


def test_query_model_hourly_groups_by_model():
    assert "PERCENTILE_CONT(0.5)" in QUERY_MODEL_HOURLY
    assert "GROUP BY token_fingerprint, model" in QUERY_MODEL_HOURLY
    assert "HAVING COUNT(*) >= 3" in QUERY_MODEL_HOURLY
