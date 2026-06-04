from task_store import AnalysisTaskStore


class FakeCursor:
    def __init__(self):
        self.executed = []
        self.fetchone_values = []

    def execute(self, query, params):
        self.executed.append((query, params))

    def fetchone(self):
        if not self.fetchone_values:
            return None
        return self.fetchone_values.pop(0)


class FakeConnection:
    def __init__(self):
        self.cursor_obj = FakeCursor()
        self.committed = False

    def cursor(self):
        return self.cursor_obj

    def commit(self):
        self.committed = True


def test_claim_task_transitions_queued_to_leased():
    connection = FakeConnection()
    connection.cursor_obj.fetchone_values = [
        (
            "trace_1",
            "core",
            "leased",
            1,
            5,
            "worker-1",
            "2026-06-03T10:05:00+00:00",
            "analysis.core",
            "1748944471000-0",
            "2026-06-03T10:00:00+00:00",
            "2026-06-03T10:00:01+00:00",
            "",
            "",
            "2026-06-03T10:00:01+00:00",
        ),
    ]
    store = AnalysisTaskStore(connection, worker_id="worker-1")

    task = store.claim_task(
        trace_id="trace_1",
        stage="core",
        lease_seconds=300,
    )

    assert task is not None
    assert task.trace_id == "trace_1"
    assert task.stage == "core"
    assert task.status == "leased"
    assert task.attempt_count == 1
    assert task.lease_owner == "worker-1"
    assert connection.committed is True

    query, params = connection.cursor_obj.executed[0]
    assert "UPDATE analysis_tasks" in query
    assert "status = 'leased'" in query
    assert "status = 'queued'" in query
    assert "status = 'failed_retryable'" in query
    assert params[0] == "worker-1"
    assert params[1] == 300
    assert params[2] == "trace_1"
    assert params[3] == "core"


def test_mark_failed_retryable_clears_lease_and_persists_error():
    connection = FakeConnection()
    store = AnalysisTaskStore(connection, worker_id="worker-1")

    store.mark_failed_retryable(
        trace_id="trace_1",
        stage="core",
        error_code="redis_timeout",
        error_message="redis unavailable",
    )

    query, params = connection.cursor_obj.executed[0]
    assert "UPDATE analysis_tasks" in query
    assert "status = 'failed_retryable'" in query
    assert "lease_owner = ''" in query
    assert "lease_expires_at = NULL" in query
    assert "last_error_code = %s" in query
    assert "last_error_message = %s" in query
    assert params == ("redis_timeout", "redis unavailable", "trace_1", "core")
    assert connection.committed is True


def test_mark_failed_terminal_sets_completed_at_and_persists_error():
    connection = FakeConnection()
    store = AnalysisTaskStore(connection, worker_id="worker-1")

    store.mark_failed_terminal(
        trace_id="trace_1",
        stage="core",
        error_code="max_attempts_exhausted",
        error_message="redis unavailable",
    )

    query, params = connection.cursor_obj.executed[0]
    assert "UPDATE analysis_tasks" in query
    assert "status = 'failed_terminal'" in query
    assert "completed_at = now()" in query
    assert "lease_owner = ''" in query
    assert "lease_expires_at = NULL" in query
    assert "last_error_code = %s" in query
    assert "last_error_message = %s" in query
    assert params == ("max_attempts_exhausted", "redis unavailable", "trace_1", "core")
    assert connection.committed is True
