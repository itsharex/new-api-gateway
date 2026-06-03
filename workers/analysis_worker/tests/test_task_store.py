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
    store = AnalysisTaskStore(connection)

    task = store.claim_task(
        trace_id="trace_1",
        stage="core",
        lease_owner="worker-1",
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
