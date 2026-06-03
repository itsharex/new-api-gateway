from unittest.mock import MagicMock, patch

from offline import run_offline_batch


def test_run_offline_batch_queries_and_upserts():
    mock_conn = MagicMock()
    mock_cursor = MagicMock()

    # Simulate cursor returning rows for each query
    hourly_rows = [("fp_a", 2000.0, 10)]
    trace_rows = [("fp_a", 15000.0, 5000.0)]
    model_rows = [("fp_a", "gpt-4.1", 300.0)]

    mock_cursor.fetchall.side_effect = [hourly_rows, trace_rows, model_rows, []]
    mock_conn.cursor.return_value = mock_cursor

    with patch("offline.upsert_baselines") as mock_upsert:
        result = run_offline_batch(mock_conn, lookback_days=7)

    assert result["fingerprints_processed"] == 1
    assert result["baselines_written"] == 4  # 1 hourly + 2 trace + 1 model
    assert mock_upsert.call_count == 1
    written = mock_upsert.call_args.args[1]
    assert written[1].metric_type == "trace_effective_tokens_p95"
    assert written[1].metric_value == 15000.0


def test_full_offline_pipeline_with_if_training():
    """Integration test: baselines + Isolation Forest training with >= 100 traces."""
    mock_conn = MagicMock()
    mock_cursor = MagicMock()

    hourly_rows = [
        ("fp_a", 3000.0, 8),
        ("fp_b", 500.0, 12),
    ]
    trace_rows = [
        ("fp_a", 12000.0, 4000.0),
        ("fp_b", 6000.0, 2000.0),
    ]
    model_rows = [
        ("fp_a", "gpt-4.1", 300.0),
    ]
    if_rows = [
        (5000, 2000, 14, False, 1, 0.0, 1, f"t_{i}", "fp_a", "alice", "gpt-4.1", "/v1/chat/completions", "2026-05-18T10:00:00Z")
        for i in range(150)
    ]

    mock_cursor.fetchall.side_effect = [hourly_rows, trace_rows, model_rows, if_rows]
    mock_conn.cursor.return_value = mock_cursor

    with patch("offline.upsert_baselines") as mock_upsert:
        result = run_offline_batch(mock_conn, lookback_days=7)

    assert result["fingerprints_processed"] >= 1
    assert result["baselines_written"] >= 3
    written = mock_upsert.call_args.args[1]
    assert any(row.metric_type == "trace_effective_tokens_p95" for row in written)
