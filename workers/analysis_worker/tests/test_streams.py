import json

from models import AnalysisStage
from streams import StreamConsumer, publish_stream_message


class FakeRedisClient:
    def __init__(self):
        self.acked = []
        self.added = []

    def xreadgroup(self, groupname, consumername, streams, count=1, block=0):
        assert groupname == "analysis-core"
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
        group_name="analysis-core",
        consumer_name="worker-1",
        block_ms=5000,
    )

    message = consumer.read_one()

    assert message is not None
    assert message.stream_name == "analysis.core"
    assert message.message_id == "1748944471000-0"
    assert message.envelope.trace_id == "trace_1"
    assert message.envelope.stage == AnalysisStage.CORE
    assert message.envelope.attempt == 1
    assert message.envelope.hints == {"protocol_family": "openai_chat"}

    consumer.ack(message)

    assert client.acked == [("analysis.core", "analysis-core", "1748944471000-0")]


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
