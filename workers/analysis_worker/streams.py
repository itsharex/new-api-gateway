import json
from dataclasses import dataclass

from models import AnalysisStage, StreamEnvelope


@dataclass(frozen=True)
class StreamMessage:
    stream_name: str
    message_id: str
    envelope: StreamEnvelope


class StreamConsumer:
    def __init__(
        self,
        client,
        stream_name: str,
        group_name: str,
        consumer_name: str,
        block_ms: int = 5000,
        count: int = 1,
    ):
        self.client = client
        self.stream_name = stream_name
        self.group_name = group_name
        self.consumer_name = consumer_name
        self.block_ms = block_ms
        self.count = count

    def read_one(self) -> StreamMessage | None:
        response = self.client.xreadgroup(
            self.group_name,
            self.consumer_name,
            {self.stream_name: ">"},
            count=self.count,
            block=self.block_ms,
        )
        if not response:
            return None
        stream_name, entries = response[0]
        if not entries:
            return None
        message_id, payload = entries[0]
        hints_raw = payload.get("hints", "{}") or "{}"
        return StreamMessage(
            stream_name=stream_name,
            message_id=message_id,
            envelope=StreamEnvelope(
                trace_id=payload["trace_id"],
                stage=AnalysisStage(payload.get("stage", AnalysisStage.CORE.value)),
                enqueued_at=payload.get("enqueued_at", ""),
                attempt=int(payload.get("attempt", 1)),
                hints=json.loads(hints_raw),
            ),
        )

    def ack(self, message: StreamMessage) -> int:
        return self.client.xack(message.stream_name, self.group_name, message.message_id)


def publish_stream_message(
    client,
    stream_name: str,
    trace_id: str,
    stage: AnalysisStage,
    enqueued_at: str,
    attempt: int = 1,
    hints: dict[str, str] | None = None,
):
    return client.xadd(
        stream_name,
        {
            "trace_id": trace_id,
            "stage": stage.value,
            "enqueued_at": enqueued_at,
            "attempt": attempt,
            "hints": json.dumps(hints or {}, sort_keys=True),
        },
    )
