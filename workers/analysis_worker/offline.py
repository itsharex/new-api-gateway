from baseline import (
    QUERY_HOURLY,
    QUERY_TRACE_LEVEL,
    QUERY_MODEL_HOURLY,
    compute_hourly_baselines,
    compute_trace_level_baselines,
    compute_model_baselines,
    upsert_baselines,
)


def run_offline_batch(connection, lookback_days: int = 7) -> dict:
    cursor = connection.cursor()

    # 1. Compute hourly baselines
    cursor.execute(QUERY_HOURLY, (str(lookback_days),))
    columns = ["fingerprint_key", "hourly_total", "hour_count"]
    hourly_rows = [dict(zip(columns, row)) for row in cursor.fetchall()]
    hourly_baselines = compute_hourly_baselines(hourly_rows)

    # 2. Compute trace-level baselines
    cursor.execute(QUERY_TRACE_LEVEL, (str(lookback_days),))
    columns = ["fingerprint_key", "p95_total", "p95_completion"]
    trace_rows = [dict(zip(columns, row)) for row in cursor.fetchall()]
    trace_baselines = compute_trace_level_baselines(trace_rows)

    # 3. Compute model baselines
    cursor.execute(QUERY_MODEL_HOURLY, (str(lookback_days),))
    columns = ["fingerprint_key", "model", "median_hourly"]
    model_rows = [dict(zip(columns, row)) for row in cursor.fetchall()]
    model_baseline_rows = compute_model_baselines(model_rows)

    # 4. Upsert all baselines
    all_baselines = hourly_baselines + trace_baselines + model_baseline_rows
    upsert_baselines(connection, all_baselines, ttl_hours=25)

    fingerprints = set(b.fingerprint_key for b in all_baselines)

    return {
        "fingerprints_processed": len(fingerprints),
        "baselines_written": len(all_baselines),
    }
