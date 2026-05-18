import json
from dataclasses import dataclass
from typing import Any


@dataclass(frozen=True)
class BaselineRow:
    fingerprint_key: str
    metric_type: str
    metric_value: float
    metadata_json: dict[str, Any]


QUERY_HOURLY = """
SELECT
    token_fingerprint AS fingerprint_key,
    PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY total_tokens) AS hourly_total,
    COUNT(*) AS hour_count
FROM usage_aggregates
WHERE bucket_size = 'hour'
  AND bucket_start >= (now() - (%s || ' days')::interval)
GROUP BY token_fingerprint
HAVING COUNT(*) >= 3
"""

QUERY_TRACE_LEVEL = """
SELECT
    token_fingerprint AS fingerprint_key,
    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY usage_total_tokens) AS p95_total,
    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY usage_completion_tokens) AS p95_completion
FROM traces
WHERE request_started_at >= (now() - (%s || ' days')::interval)
GROUP BY token_fingerprint
HAVING COUNT(*) >= 5
"""

QUERY_MODEL_HOURLY = """
SELECT
    token_fingerprint AS fingerprint_key,
    model,
    PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY total_tokens) AS median_hourly
FROM usage_aggregates
WHERE bucket_size = 'hour'
  AND bucket_start >= (now() - (%s || ' days')::interval)
GROUP BY token_fingerprint, model
HAVING COUNT(*) >= 3
"""


def compute_hourly_baselines(rows: list[dict]) -> list[BaselineRow]:
    result: list[BaselineRow] = []
    for row in rows:
        result.append(
            BaselineRow(
                fingerprint_key=row["fingerprint_key"],
                metric_type="hourly_tokens_median",
                metric_value=float(row["hourly_total"]),
                metadata_json={"hour_count": int(row["hour_count"])},
            )
        )
    return result


def compute_trace_level_baselines(rows: list[dict]) -> list[BaselineRow]:
    result: list[BaselineRow] = []
    for row in rows:
        result.append(
            BaselineRow(
                fingerprint_key=row["fingerprint_key"],
                metric_type="trace_tokens_p95",
                metric_value=float(row["p95_total"]),
                metadata_json={},
            )
        )
        result.append(
            BaselineRow(
                fingerprint_key=row["fingerprint_key"],
                metric_type="completion_tokens_p95",
                metric_value=float(row["p95_completion"]),
                metadata_json={},
            )
        )
    return result


def compute_model_baselines(rows: list[dict]) -> list[BaselineRow]:
    result: list[BaselineRow] = []
    for row in rows:
        model = row["model"]
        result.append(
            BaselineRow(
                fingerprint_key=row["fingerprint_key"],
                metric_type=f"model_hourly_median_{model}",
                metric_value=float(row["median_hourly"]),
                metadata_json={"model": model},
            )
        )
    return result


def upsert_baselines(
    connection,
    baselines: list[BaselineRow],
    ttl_hours: int = 25,
) -> None:
    if not baselines:
        return
    cursor = connection.cursor()
    for baseline in baselines:
        cursor.execute(
            """
            INSERT INTO baseline_cache (
                fingerprint_key, metric_type, metric_value,
                metadata_json, computed_at, expires_at
            ) VALUES (%s, %s, %s, %s::jsonb, now(), now() + (%s || ' hours')::interval)
            ON CONFLICT (fingerprint_key, metric_type) DO UPDATE SET
                metric_value = EXCLUDED.metric_value,
                metadata_json = EXCLUDED.metadata_json,
                computed_at = EXCLUDED.computed_at,
                expires_at = EXCLUDED.expires_at
            """,
            (
                baseline.fingerprint_key,
                baseline.metric_type,
                baseline.metric_value,
                json.dumps(baseline.metadata_json, sort_keys=True),
                str(ttl_hours),
            ),
        )
    connection.commit()
