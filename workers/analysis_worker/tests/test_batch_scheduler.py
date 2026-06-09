from datetime import datetime, timezone

from batch_scheduler import run_hourly_batch_scheduler, seconds_until_next_hour


def test_seconds_until_next_hour_uses_next_hour_boundary():
    now = datetime(2026, 6, 9, 10, 15, 30, tzinfo=timezone.utc)

    assert seconds_until_next_hour(now) == 2670.0


def test_seconds_until_next_hour_waits_full_hour_on_boundary():
    now = datetime(2026, 6, 9, 10, 0, 0, tzinfo=timezone.utc)

    assert seconds_until_next_hour(now) == 3600.0


def test_run_hourly_batch_scheduler_sleeps_then_runs_once():
    sleep_calls = []
    ran = []

    class FakeConnection:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

    def fake_connect(dsn: str):
        assert dsn == "postgres://example"
        return FakeConnection()

    def fake_run_batch(connection):
        ran.append(connection)
        return {"usage_aggregate_rows": 3}

    now = datetime(2026, 6, 9, 10, 15, 30, tzinfo=timezone.utc)
    run_hourly_batch_scheduler(
        dsn="postgres://example",
        connect=fake_connect,
        run_batch=fake_run_batch,
        now_fn=lambda: now,
        sleep_fn=sleep_calls.append,
        log_fn=lambda message: None,
        max_runs=1,
    )

    assert sleep_calls == [2670.0]
    assert len(ran) == 1
