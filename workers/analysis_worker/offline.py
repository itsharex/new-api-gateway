import json
from datetime import datetime, timezone

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

    # 5. Train Isolation Forest if enough trace data
    cursor.execute(
        """
        SELECT
            usage_total_tokens,
            usage_completion_tokens,
            EXTRACT(HOUR FROM request_started_at)::int AS hour_of_day,
            EXTRACT(ISODOW FROM request_started_at) IN (6, 7) AS is_weekend,
            1 AS model_price_tier,
            0.0 AS prompt_repetition,
            1 AS distinct_models_24h,
            trace_id,
            token_fingerprint,
            username,
            model_requested,
            route_pattern,
            request_started_at
        FROM traces
        WHERE request_started_at >= now() - (%s || ' days')::interval
          AND usage_total_tokens > 0
        ORDER BY random()
        LIMIT 50000
        """,
        (str(lookback_days),),
    )
    all_trace_rows = cursor.fetchall()

    if len(all_trace_rows) >= 100:
        from isolation_forest import IsolationForestModel, score_traces

        feature_rows = []
        trace_dicts = []
        for row in all_trace_rows:
            feature_rows.append(list(row[:7]))
            trace_dicts.append({
                "usage_total_tokens": row[0],
                "usage_completion_tokens": row[1],
                "hour_of_day": row[2],
                "is_weekend": row[3],
                "model_price_tier": row[4],
                "prompt_repetition": row[5],
                "distinct_models_24h": row[6],
                "trace_id": row[7],
                "token_fingerprint": row[8],
                "username": row[9],
                "model_requested": row[10],
                "route_pattern": row[11],
                "request_started_at": row[12],
            })
        model = IsolationForestModel.train(feature_rows, contamination=0.02)

        version = f"if_v1_{datetime.now(timezone.utc).strftime('%Y_%m_%d_%H%M')}"
        cursor.execute(
            "UPDATE model_artifacts SET is_active = false WHERE model_name = 'isolation_forest'"
        )
        cursor.execute(
            """
            INSERT INTO model_artifacts (model_name, version, artifact, feature_columns, training_stats, is_active)
            VALUES ('isolation_forest', %s, %s, %s, %s::jsonb, true)
            """,
            (
                version,
                model.serialize(),
                [
                    "usage_total_tokens", "completion_ratio", "hour_of_day", "is_weekend",
                    "model_price_tier", "prompt_repetition", "distinct_models_24h",
                ],
                json.dumps({"sample_count": len(all_trace_rows)}),
            ),
        )
        connection.commit()

        recent_alerts = score_traces(trace_dicts, model)
        for alert in recent_alerts:
            cursor.execute(
                """
                INSERT INTO usage_anomalies (
                    anomaly_id, anomaly_type, severity, token_fingerprint, fingerprint_display,
                    new_api_token_id, username, token_name_snapshot, window_start, window_end,
                    observed_value, threshold_value, baseline_value, model, route_pattern,
                    sample_trace_ids, reason, detector_version
                ) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)
                ON CONFLICT (anomaly_id) DO NOTHING
                """,
                (
                    alert.anomaly_id, alert.anomaly_type, alert.severity,
                    alert.token_fingerprint, alert.fingerprint_display,
                    alert.new_api_token_id, alert.username, alert.token_name_snapshot,
                    alert.window_start, alert.window_end,
                    alert.observed_value, alert.threshold_value, alert.baseline_value,
                    alert.model, alert.route_pattern,
                    alert.sample_trace_ids, alert.reason, alert.detector_version,
                ),
            )
        connection.commit()

    return {
        "fingerprints_processed": len(fingerprints),
        "baselines_written": len(all_baselines),
    }
