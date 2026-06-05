from runtime_metrics import RuntimeMetricsSampler


class RecordingCursor:
    def __init__(self):
        self.executed = []
        self.fetch_queue = [
            (0, 2, 1, 0, 7, 125.0, 480.0, 210.0, 840.0, 2, 1, 1),
        ]

    def execute(self, sql, params):
        self.executed.append((" ".join(sql.split()), params))

    def fetchone(self):
        return self.fetch_queue.pop(0)


class RecordingConnection:
    def __init__(self):
        self.cursor_obj = RecordingCursor()
        self.commits = 0

    def cursor(self):
        return self.cursor_obj

    def commit(self):
        self.commits += 1


class FakeRedisClient:
    def __init__(self):
        self.pending_calls = []

    def xinfo_groups(self, stream_name):
        assert stream_name == "analysis.core"
        return [
            {"name": "analysis-core-workers", "pending": 2, "consumers": 2, "lag": 5},
        ]

    def xpending_range(self, stream_name, group_name, min, max, count):
        self.pending_calls.append((min, max, count))
        assert stream_name == "analysis.core"
        assert group_name == "analysis-core-workers"
        assert max == "+"
        assert count == 100
        return [
            {"message_id": "1-0", "time_since_delivered": 12000},
            {"message_id": "2-0", "time_since_delivered": 45000},
        ]


def test_runtime_metrics_sampler_persists_sample_row():
    connection = RecordingConnection()
    redis_client = FakeRedisClient()
    sampler = RuntimeMetricsSampler(connection, redis_client)

    snapshot = sampler.sample("analysis.core", "analysis-core-workers")

    assert snapshot == {
        "stage": "core",
        "queue_depth": 7,
        "pending_count": 5,
        "leased_count": 2,
        "oldest_pending_age_seconds": 45,
        "throughput_per_minute": 7,
        "success_rate": 0.7,
        "retryable_fail_rate": 0.2,
        "terminal_fail_rate": 0.1,
        "llm_judge_timeout_rate": 0.1,
        "queue_wait_p50_ms": 125,
        "queue_wait_p95_ms": 480,
        "processing_p50_ms": 210,
        "processing_p95_ms": 840,
        "retryable_fail_count": 1,
        "terminal_fail_count": 0,
        "active_consumers": 2,
    }
    inserts = [sql for sql, _ in connection.cursor_obj.executed if "INSERT INTO analysis_runtime_samples" in sql]
    assert inserts
    assert "success_rate" in inserts[0]
    assert "retryable_fail_rate" in inserts[0]
    assert "terminal_fail_rate" in inserts[0]
    assert "llm_judge_timeout_rate" in inserts[0]
    assert connection.commits == 1
    assert redis_client.pending_calls == [("-", "+", 100)]


class PaginatedRedisClient(FakeRedisClient):
    def xpending_range(self, stream_name, group_name, min, max, count):
        self.pending_calls.append((min, max, count))
        assert stream_name == "analysis.core"
        assert group_name == "analysis-core-workers"
        assert max == "+"
        assert count == 100
        if min == "-":
            return [{"message_id": f"{index}-0", "time_since_delivered": 1000} for index in range(1, 101)]
        if min == "(100-0":
            return [
                {"message_id": "101-0", "time_since_delivered": 125000},
                {"message_id": "102-0", "time_since_delivered": 2000},
            ]
        return []


def test_runtime_metrics_sampler_pages_pending_entries_to_find_true_oldest_age():
    connection = RecordingConnection()
    redis_client = PaginatedRedisClient()
    sampler = RuntimeMetricsSampler(connection, redis_client)

    snapshot = sampler.sample("analysis.core", "analysis-core-workers")

    assert snapshot["oldest_pending_age_seconds"] == 125
    assert redis_client.pending_calls == [
        ("-", "+", 100),
        ("(100-0", "+", 100),
    ]
