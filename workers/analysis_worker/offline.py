import json
from datetime import datetime, timezone

from baseline import (
    compute_trace_level_baselines,
    upsert_baselines,
)

ROLLUP_USAGE_FACTS = """
INSERT INTO usage_aggregates (
    bucket_start, bucket_size, token_fingerprint, new_api_token_id,
    username, token_name_snapshot, model, route_pattern, protocol_family,
    request_count, success_count, error_count, stream_count,
    prompt_tokens, completion_tokens, total_tokens, reasoning_tokens, cached_tokens,
    request_body_bytes, response_body_bytes
)
SELECT
    date_trunc(%s, request_started_at) AS bucket_start,
    %s AS bucket_size,
    token_fingerprint,
    0 AS new_api_token_id,
    username,
    '' AS token_name_snapshot,
    model,
    route_pattern,
    protocol_family,
    SUM(request_count) AS request_count,
    SUM(success_count) AS success_count,
    SUM(error_count) AS error_count,
    SUM(stream_count) AS stream_count,
    SUM(prompt_tokens) AS prompt_tokens,
    SUM(completion_tokens) AS completion_tokens,
    SUM(total_tokens) AS total_tokens,
    SUM(reasoning_tokens) AS reasoning_tokens,
    SUM(cached_tokens) AS cached_tokens,
    SUM(request_body_bytes) AS request_body_bytes,
    SUM(response_body_bytes) AS response_body_bytes
FROM trace_usage_facts
GROUP BY 1, 2, 3, 5, 7, 8, 9
ON CONFLICT (
    bucket_start, bucket_size, token_fingerprint, username, model, route_pattern, protocol_family
) DO UPDATE SET
    request_count = EXCLUDED.request_count,
    success_count = EXCLUDED.success_count,
    error_count = EXCLUDED.error_count,
    stream_count = EXCLUDED.stream_count,
    prompt_tokens = EXCLUDED.prompt_tokens,
    completion_tokens = EXCLUDED.completion_tokens,
    total_tokens = EXCLUDED.total_tokens,
    reasoning_tokens = EXCLUDED.reasoning_tokens,
    cached_tokens = EXCLUDED.cached_tokens,
    request_body_bytes = EXCLUDED.request_body_bytes,
    response_body_bytes = EXCLUDED.response_body_bytes,
    updated_at = now()
"""

TRACE_LEVEL_BASELINES_FROM_FACTS = """
SELECT
    token_fingerprint AS fingerprint_key,
    PERCENTILE_CONT(0.95) WITHIN GROUP (
        ORDER BY GREATEST(prompt_tokens - cached_tokens, 0) + completion_tokens
    ) AS p95_effective,
    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY completion_tokens) AS p95_completion
FROM trace_usage_facts
WHERE request_started_at >= (now() - (%s || ' days')::interval)
GROUP BY token_fingerprint
HAVING COUNT(*) >= 5
"""


def _rebuild_usage_aggregates(connection) -> int:
    cursor = connection.cursor()
    cursor.execute(
        """
        DELETE FROM usage_aggregates
        WHERE bucket_size IN ('hour', 'day')
        """
    )
    inserted_rows = 0
    for bucket in ("hour", "day"):
        cursor.execute(ROLLUP_USAGE_FACTS, (bucket, bucket))
        rowcount = getattr(cursor, "rowcount", 0)
        if isinstance(rowcount, int) and rowcount > 0:
            inserted_rows += rowcount
    connection.commit()
    return inserted_rows


def load_trace_level_rows(connection, lookback_days: int) -> list[dict]:
    cursor = connection.cursor()
    cursor.execute(TRACE_LEVEL_BASELINES_FROM_FACTS, (str(lookback_days),))
    columns = ["fingerprint_key", "p95_effective", "p95_completion"]
    return [dict(zip(columns, row)) for row in cursor.fetchall()]


def run_offline_batch(connection, lookback_days: int = 7) -> dict:
    cursor = connection.cursor()
    usage_aggregate_rows = _rebuild_usage_aggregates(connection)

    # 1. Upsert trace-level baselines
    fact_trace_rows = load_trace_level_rows(connection, lookback_days)
    trace_baselines = compute_trace_level_baselines(fact_trace_rows)
    all_baselines = trace_baselines
    upsert_baselines(connection, all_baselines, ttl_hours=25)

    fingerprints = set(b.fingerprint_key for b in all_baselines)

    # 2. Train Isolation Forest if enough trace data
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
            username_snapshot,
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
                "username": row[9] or "",
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
        "usage_aggregate_rows": usage_aggregate_rows,
    }
