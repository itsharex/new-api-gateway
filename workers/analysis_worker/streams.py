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
        reclaim_idle_ms: int = 300_000,
    ):
        self.client = client
        self.stream_name = stream_name
        self.group_name = group_name
        self.consumer_name = consumer_name
        self.reclaim_idle_ms = reclaim_idle_ms

    def read_one(self, count: int = 1, block_ms: int = 5000) -> StreamMessage | None:
        reclaimed = self.client.xautoclaim(
            self.stream_name,
            self.group_name,
            self.consumer_name,
            min_idle_time=self.reclaim_idle_ms,
            start_id="0-0",
            count=count,
        )
        reclaimed_entries = reclaimed[1] if len(reclaimed) > 1 else []
        if reclaimed_entries:
            return _message_from_entry(self.stream_name, reclaimed_entries[0])

        response = self.client.xreadgroup(
            self.group_name,
            self.consumer_name,
            {self.stream_name: ">"},
            count=count,
            block=block_ms,
        )
        if not response:
            return None
        stream_name, entries = response[0]
        if not entries:
            return None
        return _message_from_entry(stream_name, entries[0])

    def ack(self, message_id: str) -> int:
        return self.client.xack(self.stream_name, self.group_name, message_id)


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


def _message_from_entry(stream_name: str, entry) -> StreamMessage:
    message_id, payload = entry
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
