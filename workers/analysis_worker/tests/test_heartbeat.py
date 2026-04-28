import json

from heartbeat import HeartbeatRepository


class RecordingCursor:
    def __init__(self):
        self.sql = ""
        self.args = None

    def execute(self, sql, args):
        self.sql = sql
        self.args = args


class RecordingConnection:
    def __init__(self):
        self.cursor_obj = RecordingCursor()
        self.committed = False

    def cursor(self):
        return self.cursor_obj

    def commit(self):
        self.committed = True


def test_heartbeat_repository_upserts_worker_row():
    connection = RecordingConnection()
    repo = HeartbeatRepository(connection)

    repo.record(
        worker_id="worker-1",
        worker_kind="analysis",
        status="processed",
        queue_name="analysis_jobs",
        processed_count=3,
        error_count=1,
        metadata={"trace_id": "trace_1"},
    )

    assert "INSERT INTO worker_heartbeats" in connection.cursor_obj.sql
    assert "ON CONFLICT (worker_id) DO UPDATE" in connection.cursor_obj.sql
    assert connection.cursor_obj.args[:6] == (
        "worker-1",
        "analysis",
        "processed",
        "analysis_jobs",
        3,
        1,
    )
    assert json.loads(connection.cursor_obj.args[6]) == {"trace_id": "trace_1"}
    assert connection.committed
