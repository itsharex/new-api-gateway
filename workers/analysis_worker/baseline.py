import json
from dataclasses import dataclass
from typing import Any


@dataclass(frozen=True)
class BaselineRow:
    fingerprint_key: str
    metric_type: str
    metric_value: float
    metadata_json: dict[str, Any]


QUERY_TRACE_LEVEL = """
SELECT
    token_fingerprint AS fingerprint_key,
    PERCENTILE_CONT(0.95) WITHIN GROUP (
        ORDER BY GREATEST(usage_prompt_tokens - usage_cached_tokens, 0) + usage_completion_tokens
    ) AS p95_effective,
    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY usage_completion_tokens) AS p95_completion
FROM traces
WHERE request_started_at >= (now() - (%s || ' days')::interval)
GROUP BY token_fingerprint
HAVING COUNT(*) >= 5
"""


def compute_trace_level_baselines(rows: list[dict]) -> list[BaselineRow]:
    result: list[BaselineRow] = []
    for row in rows:
        result.append(
            BaselineRow(
                fingerprint_key=row["fingerprint_key"],
                metric_type="trace_effective_tokens_p95",
                metric_value=float(row["p95_effective"]),
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
