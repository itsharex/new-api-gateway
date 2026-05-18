"""Isolation Forest multivariate anomaly detection."""

from __future__ import annotations

import numpy as np
from sklearn.ensemble import IsolationForest

from models import AnomalyAlert, anomaly_id

FEATURE_COLUMNS: list[str] = [
    "usage_total_tokens",
    "completion_ratio",
    "hour_of_day",
    "is_weekend",
    "model_price_tier",
    "prompt_repetition",
    "distinct_models_24h",
]


class IsolationForestModel:
    """Thin wrapper around sklearn IsolationForest with serialization support."""

    def __init__(self, forest: IsolationForest) -> None:
        self.forest = forest

    @classmethod
    def train(
        cls, X: list[list[float]], contamination: float = 0.02
    ) -> IsolationForestModel:
        forest = IsolationForest(
            n_estimators=100,
            contamination=contamination,
            random_state=42,
        )
        forest.fit(X)
        return cls(forest)

    def predict(self, X: list[list[float]]) -> list[int]:
        return self.forest.predict(X).tolist()

    def serialize(self) -> bytes:
        import pickle

        return pickle.dumps(self.forest)

    @classmethod
    def deserialize(cls, data: bytes) -> IsolationForestModel:
        import pickle

        forest = pickle.loads(data)
        return cls(forest)


def build_feature_matrix(
    traces: list[dict],
) -> tuple[np.ndarray, list[dict]]:
    """Convert trace dicts into a feature matrix and metadata list.

    Returns:
        (feature_array of shape (n, 7), list of metadata dicts)
    """
    features: list[list[float]] = []
    metadata: list[dict] = []

    for t in traces:
        total_tokens = t["usage_total_tokens"]
        completion_tokens = t["usage_completion_tokens"]
        completion_ratio = completion_tokens / max(total_tokens, 1)

        row = [
            float(total_tokens),
            float(completion_ratio),
            float(t["hour_of_day"]),
            float(t["is_weekend"]),
            float(t["model_price_tier"]),
            float(t.get("prompt_repetition", 0.0)),
            float(t.get("distinct_models_24h", 1)),
        ]
        features.append(row)

        metadata.append(
            {
                "trace_id": t["trace_id"],
                "token_fingerprint": t["token_fingerprint"],
                "username": t["username"],
                "model_requested": t["model_requested"],
                "route_pattern": t["route_pattern"],
                "request_started_at": t["request_started_at"],
            }
        )

    return np.array(features, dtype=np.float64), metadata


def score_traces(
    traces: list[dict], model: IsolationForestModel
) -> list[AnomalyAlert]:
    """Score traces and return AnomalyAlerts for detected anomalies."""
    X, meta = build_feature_matrix(traces)
    predictions = model.predict(X.tolist())

    alerts: list[AnomalyAlert] = []
    for i, pred in enumerate(predictions):
        if pred != -1:
            continue
        m = meta[i]
        alerts.append(
            AnomalyAlert(
                anomaly_id=anomaly_id(
                    "multivariate_anomaly", m["trace_id"], m["username"]
                ),
                anomaly_type="multivariate_anomaly",
                severity="medium",
                token_fingerprint=m["token_fingerprint"],
                fingerprint_display="",
                new_api_token_id=0,
                username=m["username"],
                token_name_snapshot="",
                window_start=m["request_started_at"],
                window_end=m["request_started_at"],
                observed_value=1.0,
                threshold_value=0.0,
                baseline_value=None,
                model=m["model_requested"],
                route_pattern=m["route_pattern"],
                sample_trace_ids=[m["trace_id"]],
                reason="Isolation Forest flagged this trace as a multivariate anomaly",
                detector_version="isolation_forest_v1_2026_05_18",
            )
        )
    return alerts
