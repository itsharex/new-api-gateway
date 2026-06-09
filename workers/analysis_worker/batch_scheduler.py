import os
import time
from datetime import datetime, timedelta, timezone

from offline import run_offline_batch


def _default_log(message: str) -> None:
    print(message, flush=True)


def seconds_until_next_hour(now: datetime) -> float:
    next_hour = now.replace(minute=0, second=0, microsecond=0) + timedelta(hours=1)
    return (next_hour - now).total_seconds()


def run_hourly_batch_scheduler(
    dsn: str,
    *,
    connect=None,
    run_batch=run_offline_batch,
    now_fn=lambda: datetime.now(timezone.utc),
    sleep_fn=time.sleep,
    log_fn=_default_log,
    max_runs: int | None = None,
) -> None:
    if not dsn:
        raise SystemExit("POSTGRES_DSN is required for analysis batch scheduler")
    if connect is None:
        import psycopg

        connect = psycopg.connect

    runs = 0
    while True:
        sleep_seconds = seconds_until_next_hour(now_fn())
        log_fn(f"analysis batch sleeping {sleep_seconds:.0f}s until next hourly run")
        sleep_fn(sleep_seconds)

        started_at = now_fn().isoformat()
        try:
            with connect(dsn) as conn:
                result = run_batch(conn)
            log_fn(f"offline batch complete at {started_at}: {result}")
        except Exception as exc:  # pragma: no cover - defensive runtime logging
            log_fn(f"offline batch failed at {started_at}: {exc!r}")

        runs += 1
        if max_runs is not None and runs >= max_runs:
            return


def main() -> int:
    dsn = os.environ.get("POSTGRES_DSN", "").strip()
    run_hourly_batch_scheduler(dsn)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
