from context_repository import PostgresContextRepository
from models import AnalysisStage
from repository import CoreStageAnalysisRepository


def default_process_job_line(*args, **kwargs):
    from main import process_job_line

    return process_job_line(*args, **kwargs)


class CoreStageProcessor:
    def __init__(
        self,
        connection,
        evidence_store,
        storage_backend: str = "filesystem",
        llm_judge=None,
        enrichment_stream_name: str = "analysis.enrichment",
        redis_client=None,
        process_job_line_fn=None,
    ):
        self.connection = connection
        self.evidence_store = evidence_store
        self.storage_backend = storage_backend
        self.llm_judge = llm_judge
        self.redis_client = redis_client
        self.process_job_line_fn = process_job_line_fn or default_process_job_line

    def process(self, trace_id: str) -> dict:
        repository = CoreStageAnalysisRepository(self.connection)
        payload = repository.load_trace_job_json(trace_id)
        result = self.process_job_line_fn(
            payload,
            self.evidence_store,
            repository,
            PostgresContextRepository(self.connection),
            storage_backend=self.storage_backend,
            llm_judge=self.llm_judge,
            allow_llm=False,
            enable_media_derivation=False,
        )
        enrichment_reasons = _effective_enrichment_reasons(result, llm_judge=self.llm_judge)
        result["enrichment_required"] = bool(enrichment_reasons)
        result["enrichment_reasons"] = enrichment_reasons
        enrichment_required = _trace_needs_enrichment(self.connection, trace_id, result)
        result["enrichment_required"] = enrichment_required

        _update_trace_stage_state(
            self.connection,
            trace_id,
            enrichment_required=enrichment_required,
            enrichment_status="pending" if enrichment_required else "not_required",
            enrichment_queued=False,
        )
        return result


def _trace_needs_enrichment(connection, trace_id: str, result: dict | None = None) -> bool:
    if (result or {}).get("enrichment_required"):
        return True
    if (result or {}).get("llm_judge_status") == "degraded":
        return True
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


def _effective_enrichment_reasons(result: dict | None, *, llm_judge) -> list[str]:
    reasons = list((result or {}).get("enrichment_reasons") or [])
    filtered: list[str] = []
    for reason in reasons:
        if reason == "llm_judge" and llm_judge is None:
            continue
        filtered.append(reason)
    return filtered


def _update_trace_stage_state(
    connection,
    trace_id: str,
    *,
    enrichment_required: bool,
    enrichment_status: str,
    enrichment_queued: bool,
    last_error_code: str = "",
) -> None:
    cursor = connection.cursor()
    cursor.execute(
        """
        UPDATE traces
        SET core_status = 'completed',
            core_completed_at = now(),
            last_analysis_error_code = %s,
            enrichment_required = %s,
            enrichment_status = %s,
            enrichment_queued_at = CASE WHEN %s THEN now() ELSE NULL END,
            updated_at = now()
        WHERE trace_id = %s
        """,
        (
            last_error_code,
            enrichment_required,
            enrichment_status,
            enrichment_queued,
            trace_id,
        ),
    )
