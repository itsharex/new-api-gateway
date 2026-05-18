from unittest.mock import MagicMock, patch

from offline import run_offline_batch


def test_run_offline_batch_queries_and_upserts():
    mock_conn = MagicMock()
    mock_cursor = MagicMock()

    # Simulate cursor returning rows for each query
    hourly_rows = [("fp_a", 2000.0, 10)]
    trace_rows = [("fp_a", 15000.0, 5000.0)]
    model_rows = [("fp_a", "gpt-4.1", 300.0)]

    mock_cursor.fetchall.side_effect = [hourly_rows, trace_rows, model_rows]
    mock_conn.cursor.return_value = mock_cursor

    with patch("offline.upsert_baselines") as mock_upsert:
        result = run_offline_batch(mock_conn, lookback_days=7)

    assert result["fingerprints_processed"] == 1
    assert result["baselines_written"] == 4  # 1 hourly + 2 trace + 1 model
    assert mock_upsert.call_count == 1
