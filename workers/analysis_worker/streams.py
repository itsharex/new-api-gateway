import json
from datetime import datetime, timezone
from dataclasses import dataclass

import redis

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
        self._group_ready = False

    def ensure_group(self) -> None:
        if self._group_ready:
            return
        try:
            self.client.xgroup_create(
                self.stream_name,
                self.group_name,
                id="0",
                mkstream=True,
            )
        except redis.ResponseError as exc:
            if "BUSYGROUP" not in str(exc):
                raise
        self._group_ready = True

    def read_one(self, count: int = 1, block_ms: int = 5000) -> StreamMessage | None:
        messages = self.read_batch(count=count, block_ms=block_ms)
        if not messages:
            return None
        return messages[0]

    def read_batch(self, count: int = 1, block_ms: int = 5000) -> list[StreamMessage]:
        self.ensure_group()
        try:
            reclaimed = self.client.xautoclaim(
                self.stream_name,
                self.group_name,
                self.consumer_name,
                min_idle_time=self.reclaim_idle_ms,
                start_id="0-0",
                count=count,
            )
        except redis.TimeoutError:
            return []
        reclaimed_entries = reclaimed[1] if len(reclaimed) > 1 else []
        if reclaimed_entries:
            return [_message_from_entry(self.stream_name, entry) for entry in reclaimed_entries]

        try:
            response = self.client.xreadgroup(
                self.group_name,
                self.consumer_name,
                {self.stream_name: ">"},
                count=count,
                block=block_ms,
            )
        except redis.TimeoutError:
            return []
        if not response:
            return []
        stream_name, entries = response[0]
        if not entries:
            return []
        return [_message_from_entry(stream_name, entry) for entry in entries]

    def ack(self, message_id: str) -> int:
        return self.client.xack(self.stream_name, self.group_name, message_id)


def publish_stream_message(
    client,
    stream_name: str,
    trace_id: str,
    stage: AnalysisStage,
    enqueued_at: str = "",
    attempt: int = 1,
    hints: dict[str, str] | None = None,
):
    payload = {
        "trace_id": trace_id,
        "stage": stage.value,
        "attempt": attempt,
        "hints": json.dumps(hints or {}, sort_keys=True),
    }
    if enqueued_at:
        payload["enqueued_at"] = enqueued_at
    return client.xadd(stream_name, payload)


def stream_message_id_to_enqueued_at(message_id: str) -> str:
    timestamp_ms = int(str(message_id).split("-", 1)[0])
    return datetime.fromtimestamp(timestamp_ms / 1000, tz=timezone.utc).isoformat()


def _default_stage_for_stream(stream_name: str) -> AnalysisStage:
    if stream_name == "analysis.enrichment":
        return AnalysisStage.ENRICHMENT
    return AnalysisStage.CORE


def _parse_stage(stream_name: str, raw_stage) -> AnalysisStage:
    default_stage = _default_stage_for_stream(stream_name)
    candidate = str(raw_stage or "").strip()
    if not candidate:
        return default_stage
    try:
        return AnalysisStage(candidate)
    except ValueError:
        return default_stage


def _parse_attempt(raw_attempt) -> int:
    try:
        attempt = int(raw_attempt)
    except (TypeError, ValueError):
        return 1
    return attempt if attempt > 0 else 1


def _parse_hints(hints_raw) -> dict:
    if isinstance(hints_raw, dict):
        return hints_raw
    raw = hints_raw or "{}"
    try:
        parsed = json.loads(raw)
    except (TypeError, ValueError, json.JSONDecodeError):
        return {}
    if isinstance(parsed, dict):
        return parsed
    return {}


def _message_from_entry(stream_name: str, entry) -> StreamMessage:
    message_id, payload = entry
    return StreamMessage(
        stream_name=stream_name,
        message_id=message_id,
        envelope=StreamEnvelope(
            trace_id=str(payload.get("trace_id", "") or "").strip(),
            stage=_parse_stage(stream_name, payload.get("stage")),
            enqueued_at=payload.get("enqueued_at", ""),
            attempt=_parse_attempt(payload.get("attempt", 1)),
            hints=_parse_hints(payload.get("hints", "{}")),
        ),
    )
