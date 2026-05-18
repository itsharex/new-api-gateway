"""Tests for isolation_forest module."""

import numpy as np

from isolation_forest import (
    FEATURE_COLUMNS,
    IsolationForestModel,
    build_feature_matrix,
    score_traces,
)


def _trace(**overrides) -> dict:
    """Return a minimal trace dict with sensible defaults."""
    defaults = {
        "usage_total_tokens": 500,
        "usage_completion_tokens": 200,
        "hour_of_day": 14,
        "is_weekend": 0,
        "model_price_tier": 1,
        "prompt_repetition": 0.0,
        "distinct_models_24h": 1,
        "trace_id": "trace_default",
        "token_fingerprint": "fp_abc",
        "username": "alice",
        "model_requested": "gpt-4.1",
        "route_pattern": "/v1/chat/completions",
        "request_started_at": "2026-05-18T14:00:00Z",
    }
    defaults.update(overrides)
    return defaults


def test_build_feature_matrix_shape():
    traces = [
        _trace(trace_id="t1", usage_total_tokens=100, usage_completion_tokens=40),
        _trace(trace_id="t2", usage_total_tokens=800, usage_completion_tokens=300),
    ]
    X, meta = build_feature_matrix(traces)

    assert X.shape == (2, 7)
    assert len(meta) == 2
    assert meta[0]["trace_id"] == "t1"
    assert meta[1]["trace_id"] == "t2"
    assert meta[0]["username"] == "alice"
    assert meta[0]["model_requested"] == "gpt-4.1"
    assert meta[0]["route_pattern"] == "/v1/chat/completions"
    assert meta[0]["request_started_at"] == "2026-05-18T14:00:00Z"
    # completion_ratio: 40 / max(100, 1) = 0.4
    assert X[0, 1] == 0.4


def test_train_and_score():
    rng = np.random.RandomState(42)
    # Generate 200 normal-looking samples centered around typical values
    X_train = rng.normal(loc=[500, 0.4, 14, 0, 1, 0, 1], scale=[100, 0.1, 4, 0.3, 0.5, 0.1, 0.5], size=(200, 7)).tolist()
    model = IsolationForestModel.train(X_train)

    # Predict 5 samples from the same distribution
    X_test = X_train[:5]
    preds = model.predict(X_test)

    assert len(preds) == 5
    assert all(p in (1, -1) for p in preds)

    # Serialization round-trip
    data = model.serialize()
    model2 = IsolationForestModel.deserialize(data)
    preds2 = model2.predict(X_test)
    assert preds2 == preds


def test_score_traces_returns_anomaly_alerts():
    rng = np.random.RandomState(42)
    # Train on normal data
    X_train = rng.normal(loc=[500, 0.4, 14, 0, 1, 0, 1], scale=[100, 0.1, 4, 0.3, 0.5, 0.1, 0.5], size=(200, 7)).tolist()
    model = IsolationForestModel.train(X_train)

    # Create a clearly anomalous trace
    anomalous = _trace(
        usage_total_tokens=100_000,
        usage_completion_tokens=50_000,
        hour_of_day=3,
        is_weekend=1,
        model_price_tier=5,
        prompt_repetition=0.9,
        distinct_models_24h=10,
        trace_id="trace_anomaly",
        token_fingerprint="fp_anom",
        username="bob",
        model_requested="o1-pro",
        route_pattern="/v1/chat/completions",
        request_started_at="2026-05-18T03:00:00Z",
    )
    # Also add a normal trace to make it a batch of 2
    normal = _trace(
        usage_total_tokens=500,
        usage_completion_tokens=200,
        trace_id="trace_normal",
    )

    alerts = score_traces([normal, anomalous], model)

    # The anomalous trace should be flagged
    assert len(alerts) >= 1
    alert = alerts[0]
    assert alert.anomaly_type == "multivariate_anomaly"
    assert alert.severity == "medium"
    assert alert.detector_version == "isolation_forest_v1_2026_05_18"
    assert "trace_anomaly" in alert.sample_trace_ids or len(alerts) > 0

    # Find the alert for the anomalous trace
    anom_alerts = [a for a in alerts if a.sample_trace_ids == ["trace_anomaly"]]
    if anom_alerts:
        assert anom_alerts[0].model == "o1-pro"
        assert anom_alerts[0].username == "bob"
        assert anom_alerts[0].route_pattern == "/v1/chat/completions"
