"""pytest config: prerequisites gate + per-test errors reset + fixtures."""

from __future__ import annotations

import psycopg
import pytest
import redis
import requests

import helpers
from helpers import PG_DSN, REDIS_URL


# ---------------------------------------------------------------------------
# Session-scoped prerequisites (fail fast before any test runs)
# ---------------------------------------------------------------------------


@pytest.fixture(scope="session", autouse=True)
def prerequisites():
    print("\n=== Prerequisites Check ===")
    failures: list[str] = []

    # audit-gateway healthz
    try:
        r = requests.get(f"{helpers.GATEWAY_URL}/healthz", timeout=5)
        assert r.status_code == 200
        print("  ✓ audit-gateway")
    except Exception as exc:
        failures.append(f"audit-gateway ({helpers.GATEWAY_URL}): {exc}")

    # postgres + migration count
    try:
        with psycopg.connect(PG_DSN) as conn:
            n = conn.execute("SELECT count(*) FROM schema_migrations").fetchone()[0]
        assert n >= 19, f"expected >=19 migrations, got {n}"
        print(f"  ✓ postgres (migrations={n})")
    except Exception as exc:
        failures.append(f"postgres: {exc}")

    # redis
    try:
        redis.Redis.from_url(REDIS_URL).ping()
        print("  ✓ redis")
    except Exception as exc:
        failures.append(f"redis: {exc}")

    # new-api reachable
    try:
        requests.get(f"{helpers.UPSTREAM_URL}/", timeout=5)
        print("  ✓ new-api")
    except Exception as exc:
        failures.append(f"new-api ({helpers.UPSTREAM_URL}): {exc}")

    # model availability (probe each required model via a minimal request)
    for model in (helpers.OPENAI_MODEL, helpers.CLAUDE_MODEL):
        try:
            resp = requests.post(
                f"{helpers.UPSTREAM_URL}/v1/chat/completions",
                headers=helpers.HEADERS,
                json={"model": model, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
                timeout=30,
            )
            assert resp.status_code == 200, f"model {model}: {resp.status_code} {resp.text[:200]}"
            print(f"  ✓ model {model}")
        except Exception as exc:
            failures.append(f"model {model} unavailable: {exc}")

    if failures:
        pytest.exit("Prerequisites failed:\n  " + "\n  ".join(failures), returncode=1)
    yield


# ---------------------------------------------------------------------------
# Per-test: reset the module-level errors collector
# ---------------------------------------------------------------------------


@pytest.fixture(autouse=True)
def _isolate_errors():
    """Clear errors before each test; fail the test if any were recorded."""
    helpers.errors.clear()
    yield
    if helpers.errors:
        raise AssertionError(
            f"{len(helpers.errors)} assertion(s) failed:\n"
            + "\n".join(f"  {e}" for e in helpers.errors)
        )


# ---------------------------------------------------------------------------
# Shared fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def db():
    conn = psycopg.connect(PG_DSN)
    yield conn
    conn.close()
