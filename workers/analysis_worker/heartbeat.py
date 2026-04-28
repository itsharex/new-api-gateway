import json


class HeartbeatRepository:
    def __init__(self, connection):
        self.connection = connection

    def record(
        self,
        worker_id: str,
        worker_kind: str,
        status: str,
        queue_name: str,
        processed_count: int,
        error_count: int,
        metadata: dict,
    ) -> None:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            INSERT INTO worker_heartbeats (
                worker_id, worker_kind, status, queue_name,
                processed_count, error_count, metadata_json,
                last_seen_at, updated_at
            ) VALUES (%s,%s,%s,%s,%s,%s,%s::jsonb,now(),now())
            ON CONFLICT (worker_id) DO UPDATE SET
                worker_kind = EXCLUDED.worker_kind,
                status = EXCLUDED.status,
                queue_name = EXCLUDED.queue_name,
                processed_count = EXCLUDED.processed_count,
                error_count = EXCLUDED.error_count,
                metadata_json = EXCLUDED.metadata_json,
                last_seen_at = now(),
                updated_at = now()
            """,
            (
                worker_id,
                worker_kind,
                status,
                queue_name,
                processed_count,
                error_count,
                json.dumps(metadata, sort_keys=True),
            ),
        )
        self.connection.commit()
