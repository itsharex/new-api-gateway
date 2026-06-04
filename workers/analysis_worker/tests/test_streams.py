import json

from models import AnalysisStage
from streams import StreamConsumer, publish_stream_message


class FakeRedisClient:
    def __init__(self):
        self.acked = []
        self.added = []
        self.claimed = []
        self.readgroup_calls = []

    def xautoclaim(self, name, groupname, consumername, min_idle_time, start_id="0-0", count=None, justid=False):
        self.claimed.append((name, groupname, consumername, min_idle_time, start_id, count, justid))
        return ["0-0", [], []]

    def xreadgroup(self, groupname, consumername, streams, count=1, block=0):
        self.readgroup_calls.append((groupname, consumername, streams, count, block))
        assert groupname == "analysis-core-workers"
        assert consumername == "worker-1"
        assert streams == {"analysis.core": ">"}
        assert count == 1
        assert block == 5000
        return [
            (
                "analysis.core",
                [
                    (
                        "1748944471000-0",
                        {
                            "trace_id": "trace_1",
                            "stage": "core",
                            "enqueued_at": "2026-06-03T10:00:00Z",
                            "attempt": "1",
                            "hints": json.dumps({"protocol_family": "openai_chat"}),
                        },
                    )
                ],
            )
        ]

    def xack(self, stream_name, group_name, message_id):
        self.acked.append((stream_name, group_name, message_id))
        return 1

    def xadd(self, stream_name, fields):
        self.added.append((stream_name, fields))
        return "1748944471000-1"


def test_stream_consumer_reads_claims_and_acks_message():
    client = FakeRedisClient()
    consumer = StreamConsumer(
        client,
        stream_name="analysis.core",
        group_name="analysis-core-workers",
        consumer_name="worker-1",
    )

    message = consumer.read_one(count=1, block_ms=5000)

    assert message is not None
    assert message.stream_name == "analysis.core"
    assert message.message_id == "1748944471000-0"
    assert message.envelope.trace_id == "trace_1"
    assert message.envelope.stage == AnalysisStage.CORE
    assert message.envelope.attempt == 1
    assert message.envelope.hints == {"protocol_family": "openai_chat"}

    consumer.ack(message.message_id)

    assert client.acked == [("analysis.core", "analysis-core-workers", "1748944471000-0")]


def test_stream_consumer_reclaims_idle_pending_message():
    class FakePendingRedisClient(FakeRedisClient):
        def xreadgroup(self, groupname, consumername, streams, count=1, block=0):
            return []

        def xautoclaim(self, name, groupname, consumername, min_idle_time, start_id="0-0", count=None, justid=False):
            assert name == "analysis.core"
            assert groupname == "analysis-core-workers"
            assert consumername == "worker-1"
            assert min_idle_time == 300000
            assert start_id == "0-0"
            assert count == 1
            assert justid is False
            return [
                "0-0",
                [
                    (
                        "1748944471000-2",
                        {
                            "trace_id": "trace_reclaim",
                            "stage": "core",
                            "enqueued_at": "2026-06-03T10:01:00Z",
                            "attempt": "2",
                            "hints": json.dumps({"reclaimed": "true"}),
                        },
                    )
                ],
                [],
            ]

    client = FakePendingRedisClient()
    consumer = StreamConsumer(
        client,
        stream_name="analysis.core",
        group_name="analysis-core-workers",
        consumer_name="worker-1",
    )

    message = consumer.read_one(count=1, block_ms=5000)

    assert message is not None
    assert message.message_id == "1748944471000-2"
    assert message.envelope.trace_id == "trace_reclaim"
    assert message.envelope.attempt == 2
    assert message.envelope.hints == {"reclaimed": "true"}


def test_publish_stream_message_serializes_envelope_for_xadd():
    client = FakeRedisClient()

    message_id = publish_stream_message(
        client,
        stream_name="analysis.enrichment",
        trace_id="trace_2",
        stage=AnalysisStage.ENRICHMENT,
        enqueued_at="2026-06-03T10:10:00Z",
        attempt=2,
        hints={"reason": "media"},
    )

    assert message_id == "1748944471000-1"
    assert client.added == [(
        "analysis.enrichment",
        {
            "trace_id": "trace_2",
            "stage": "enrichment",
            "enqueued_at": "2026-06-03T10:10:00Z",
            "attempt": 2,
            "hints": json.dumps({"reason": "media"}, sort_keys=True),
        },
    )]


def test_stream_consumer_reclaims_idle_pending_message_before_reading_new_entries():
    class PendingFirstRedisClient(FakeRedisClient):
        def xautoclaim(self, name, groupname, consumername, min_idle_time, start_id="0-0", count=None, justid=False):
            self.claimed.append((name, groupname, consumername, min_idle_time, start_id, count, justid))
            return [
                "1748944471000-0",
                [
                    (
                        "1748944471000-0",
                        {
                            "trace_id": "trace_reclaimed",
                            "stage": "core",
                            "enqueued_at": "2026-06-03T10:05:00Z",
                            "attempt": "2",
                            "hints": json.dumps({"retry": "pending-reclaim"}),
                        },
                    )
                ],
                [],
            ]

        def xreadgroup(self, *args, **kwargs):
            raise AssertionError("should not read new entries before reclaiming idle pending work")

    client = PendingFirstRedisClient()
    consumer = StreamConsumer(
        client,
        stream_name="analysis.core",
        group_name="analysis-core-workers",
        consumer_name="worker-1",
    )

    message = consumer.read_one(count=1, block_ms=5000)

    assert message is not None
    assert message.message_id == "1748944471000-0"
    assert message.envelope.trace_id == "trace_reclaimed"
    assert message.envelope.attempt == 2
    assert message.envelope.hints == {"retry": "pending-reclaim"}
    assert client.claimed == [("analysis.core", "analysis-core-workers", "worker-1", 300000, "0-0", 1, False)]
