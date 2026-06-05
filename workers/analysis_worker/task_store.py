from models import AnalysisStage, AnalysisTask, TaskStatus


def _task_from_row(row) -> AnalysisTask:
    values = list(row) + [""] * max(0, 15 - len(row))
    return AnalysisTask(
        trace_id=values[0],
        stage=AnalysisStage(values[1]),
        status=TaskStatus(values[2]),
        attempt_count=values[3],
        max_attempts=values[4],
        lease_owner=values[5],
        lease_expires_at=values[6] or "",
        stream_name=values[7],
        stream_message_id=values[8],
        queued_at=values[9] or "",
        started_at=values[10] or "",
        completed_at=values[11] or "",
        last_error_code=values[12],
        last_error_message=values[13],
        updated_at=values[14] or "",
    )


class AnalysisTaskStore:
    def __init__(self, connection, worker_id: str):
        self.connection = connection
        self.worker_id = worker_id

    def insert_task(
        self,
        trace_id: str,
        stage: str,
        stream_name: str,
        stream_message_id: str,
        queued_at: str = "",
        max_attempts: int = 5,
    ) -> None:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            INSERT INTO analysis_tasks (
                trace_id, stage, status, max_attempts, stream_name, stream_message_id, queued_at
            ) VALUES (%s, %s, 'queued', %s, %s, %s, %s::timestamptz)
            ON CONFLICT (trace_id, stage) DO UPDATE SET
                stream_name = EXCLUDED.stream_name,
                stream_message_id = EXCLUDED.stream_message_id,
                queued_at = COALESCE(analysis_tasks.queued_at, EXCLUDED.queued_at),
                updated_at = now()
            """,
            (trace_id, stage, max_attempts, stream_name, stream_message_id, queued_at),
        )
        self.connection.commit()

    def claim_task(
        self,
        trace_id: str,
        stage: str,
        lease_seconds: int,
    ) -> AnalysisTask | None:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            UPDATE analysis_tasks
            SET status = 'leased',
                lease_owner = %s,
                lease_expires_at = now() + (%s * interval '1 second'),
                attempt_count = attempt_count + 1,
                started_at = COALESCE(started_at, now()),
                updated_at = now()
            WHERE trace_id = %s
              AND stage = %s
              AND attempt_count < max_attempts
              AND (
                  status = 'queued'
                  OR status = 'failed_retryable'
                  OR (status = 'leased' AND lease_expires_at < now())
              )
            RETURNING
                trace_id, stage, status, attempt_count, max_attempts,
                lease_owner, lease_expires_at, stream_name, stream_message_id,
                queued_at, started_at, completed_at,
                last_error_code, last_error_message, updated_at
            """,
            (self.worker_id, lease_seconds, trace_id, stage),
        )
        row = cursor.fetchone()
        self.connection.commit()
        if row is None:
            return None
        return _task_from_row(row)

    def get_task(self, trace_id: str, stage: str) -> AnalysisTask | None:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            SELECT
                trace_id, stage, status, attempt_count, max_attempts,
                lease_owner, lease_expires_at, stream_name, stream_message_id,
                queued_at, started_at, completed_at,
                last_error_code, last_error_message, updated_at
            FROM analysis_tasks
            WHERE trace_id = %s AND stage = %s
            """,
            (trace_id, stage),
        )
        row = cursor.fetchone()
        if row is None:
            return None
        return _task_from_row(row)

    def mark_succeeded(self, trace_id: str, stage: str) -> None:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            UPDATE analysis_tasks
            SET status = 'succeeded',
                completed_at = now(),
                lease_owner = '',
                lease_expires_at = NULL,
                updated_at = now()
            WHERE trace_id = %s
              AND stage = %s
              AND status = 'leased'
              AND lease_owner = %s
              AND lease_expires_at >= now()
            RETURNING 1
            """,
            (trace_id, stage, self.worker_id),
        )
        return cursor.fetchone() is not None

    def mark_failed_retryable(
        self,
        trace_id: str,
        stage: str,
        error_code: str,
        error_message: str,
    ) -> bool:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            UPDATE analysis_tasks
            SET status = 'failed_retryable',
                lease_owner = '',
                lease_expires_at = NULL,
                last_error_code = %s,
                last_error_message = %s,
                updated_at = now()
            WHERE trace_id = %s
              AND stage = %s
              AND status = 'leased'
              AND lease_owner = %s
              AND lease_expires_at >= now()
            RETURNING 1
            """,
            (error_code, error_message, trace_id, stage, self.worker_id),
        )
        return cursor.fetchone() is not None

    def mark_failed_terminal(
        self,
        trace_id: str,
        stage: str,
        error_code: str,
        error_message: str,
    ) -> bool:
        cursor = self.connection.cursor()
        cursor.execute(
            """
            UPDATE analysis_tasks
            SET status = 'failed_terminal',
                completed_at = now(),
                lease_owner = '',
                lease_expires_at = NULL,
                last_error_code = %s,
                last_error_message = %s,
                updated_at = now()
            WHERE trace_id = %s
              AND stage = %s
              AND status = 'leased'
              AND lease_owner = %s
              AND lease_expires_at >= now()
            RETURNING 1
            """,
            (error_code, error_message, trace_id, stage, self.worker_id),
        )
        return cursor.fetchone() is not None
