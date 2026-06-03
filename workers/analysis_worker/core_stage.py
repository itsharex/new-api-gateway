import json
from datetime import datetime, timezone

from context_repository import PostgresContextRepository
from main import process_job_line
from models import AnalysisStage
from repository import PostgresAnalysisRepository
from streams import publish_stream_message


class CoreStageProcessor:
    def __init__(
        self,
        connection,
        evidence_store,
        storage_backend: str = "filesystem",
        llm_judge=None,
        enrichment_stream_name: str = "analysis.enrichment",
        redis_client=None,
    ):
        self.connection = connection
        self.evidence_store = evidence_store
        self.storage_backend = storage_backend
        self.llm_judge = llm_judge
        self.enrichment_stream_name = enrichment_stream_name
        self.redis_client = redis_client

    def process(self, trace_id: str) -> dict:
        payload = _load_trace_job_json(self.connection, trace_id)
        result = process_job_line(
            payload,
            self.evidence_store,
            PostgresAnalysisRepository(self.connection),
            PostgresContextRepository(self.connection),
            storage_backend=self.storage_backend,
            llm_judge=self.llm_judge,
        )
        enrichment_required = _trace_needs_enrichment(self.connection, trace_id)
        cursor = self.connection.cursor()
        cursor.execute(
            """
            UPDATE traces
            SET core_status = 'completed',
                core_completed_at = now(),
                enrichment_required = %s,
                enrichment_status = %s,
                enrichment_queued_at = CASE WHEN %s THEN now() ELSE NULL END,
                updated_at = now()
            WHERE trace_id = %s
            """,
            (
                enrichment_required,
                "pending" if enrichment_required else "not_required",
                enrichment_required,
                trace_id,
            ),
        )
        self.connection.commit()
        if enrichment_required and self.redis_client is not None:
            publish_stream_message(
                self.redis_client,
                stream_name=self.enrichment_stream_name,
                trace_id=trace_id,
                stage=AnalysisStage.ENRICHMENT,
                enqueued_at=datetime.now(timezone.utc).isoformat(),
                hints={"source_stage": AnalysisStage.CORE.value},
            )
        return result


def _load_trace_job_json(connection, trace_id: str) -> str:
    cursor = connection.cursor()
    cursor.execute(
        """
        SELECT
            trace_id,
            route_pattern,
            protocol_family,
            capture_mode,
            username_snapshot,
            request_raw_ref,
            request_headers_ref,
            response_raw_ref,
            response_headers_ref,
            model_requested,
            usage_prompt_tokens,
            usage_completion_tokens,
            usage_total_tokens,
            usage_reasoning_tokens,
            usage_cached_tokens,
            token_fingerprint,
            fingerprint_display,
            new_api_token_id_snapshot,
            token_name_snapshot,
            identity_resolution_status,
            client_ip_hash,
            user_agent_hash,
            status_code,
            upstream_status_code,
            stream,
            request_started_at,
            request_body_size,
            response_body_size
        FROM traces
        WHERE trace_id = %s
        """,
        (trace_id,),
    )
    row = cursor.fetchone()
    if row is None:
        raise ValueError(f"trace not found: {trace_id}")
    payload = {
        "type": "trace_captured",
        "trace_id": row[0],
        "route_pattern": row[1],
        "protocol_family": row[2],
        "capture_mode": row[3],
        "username": row[4],
        "request_raw_ref": row[5],
        "request_headers_ref": row[6],
        "response_raw_ref": row[7],
        "response_headers_ref": row[8],
        "model_requested": row[9],
        "usage_prompt_tokens": row[10],
        "usage_completion_tokens": row[11],
        "usage_total_tokens": row[12],
        "usage_reasoning_tokens": row[13],
        "usage_cached_tokens": row[14],
        "token_fingerprint": row[15],
        "fingerprint_display": row[16],
        "new_api_token_id": row[17],
        "token_name_snapshot": row[18],
        "identity_resolution_status": row[19],
        "client_ip_hash": row[20],
        "user_agent_hash": row[21],
        "status_code": row[22],
        "upstream_status_code": row[23],
        "stream": row[24],
        "request_started_at": row[25].isoformat() if hasattr(row[25], "isoformat") else (row[25] or ""),
        "request_body_size": row[26],
        "response_body_size": row[27],
    }
    return json.dumps(payload, sort_keys=True)


def _trace_needs_enrichment(connection, trace_id: str) -> bool:
    cursor = connection.cursor()
    cursor.execute(
        """
        SELECT 1
        FROM media_snapshot_jobs
        WHERE trace_id = %s
        LIMIT 1
        """,
        (trace_id,),
    )
    return cursor.fetchone() is not None
