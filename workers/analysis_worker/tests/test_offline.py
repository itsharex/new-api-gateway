from unittest.mock import MagicMock, patch

from offline import run_offline_batch


def test_run_offline_batch_queries_and_upserts():
    mock_conn = MagicMock()
    mock_cursor = MagicMock()
    mock_cursor.rowcount = 0

    # trace-level baseline query + isolation-forest query (not enough rows to train)
    trace_rows = [("fp_a", 15000.0, 5000.0)]

    mock_cursor.fetchall.side_effect = [trace_rows, []]
    mock_conn.cursor.return_value = mock_cursor

    with patch("offline.upsert_baselines") as mock_upsert:
        result = run_offline_batch(mock_conn, lookback_days=7)

    assert result["fingerprints_processed"] == 1
    assert result["baselines_written"] == 2  # trace_effective_tokens_p95 + completion_tokens_p95
    assert mock_upsert.call_count == 1
    written = mock_upsert.call_args.args[1]
    assert written[0].metric_type == "trace_effective_tokens_p95"
    assert written[0].metric_value == 15000.0


def test_full_offline_pipeline_with_if_training():
    """Integration test: baselines + Isolation Forest training with >= 100 traces."""
    mock_conn = MagicMock()
    mock_cursor = MagicMock()
    mock_cursor.rowcount = 0

    trace_rows = [
        ("fp_a", 12000.0, 4000.0),
        ("fp_b", 6000.0, 2000.0),
    ]
    if_rows = [
        (5000, 2000, 14, False, 1, 0.0, 1, f"t_{i}", "fp_a", "alice", "gpt-4.1", "/v1/chat/completions", "2026-05-18T10:00:00Z")
        for i in range(150)
    ]

    mock_cursor.fetchall.side_effect = [trace_rows, if_rows]
    mock_conn.cursor.return_value = mock_cursor

    with patch("offline.upsert_baselines") as mock_upsert:
        result = run_offline_batch(mock_conn, lookback_days=7)

    assert result["fingerprints_processed"] >= 1
    assert result["baselines_written"] >= 2
    written = mock_upsert.call_args.args[1]
    assert any(row.metric_type == "trace_effective_tokens_p95" for row in written)


def test_run_offline_batch_rebuilds_usage_aggregates_from_trace_usage_facts():
    mock_conn = MagicMock()
    mock_cursor = MagicMock()
    mock_cursor.rowcount = 1

    trace_rows = [("fp_a", 15000.0, 5000.0)]

    mock_cursor.fetchall.side_effect = [trace_rows, []]
    mock_conn.cursor.return_value = mock_cursor

    with patch("offline.upsert_baselines"):
        result = run_offline_batch(mock_conn, lookback_days=7)

    assert result["usage_aggregate_rows"] == 2
    executed_sql = "\n".join(call.args[0] for call in mock_cursor.execute.call_args_list)
    assert "DELETE FROM usage_aggregates" in executed_sql
    assert "FROM trace_usage_facts" in executed_sql
    assert "INSERT INTO usage_aggregates" in executed_sql
