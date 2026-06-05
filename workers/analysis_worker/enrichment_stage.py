from context_repository import PostgresContextRepository
from main import llm_judge_metadata
from media_extraction import MediaExtractionContext
from models import AnalysisStage, parse_job
from normalizers import normalize_json_trace
from repository import PostgresAnalysisRepository
from work_relevance import classify_work_relevance


def default_process_enrichment(trace_id: str, **_kwargs) -> dict:
    connection = _kwargs["connection"]
    evidence_store = _kwargs["evidence_store"]
    llm_judge = _kwargs.get("llm_judge")
    repository = PostgresAnalysisRepository(connection)
    payload = repository.load_trace_job_json(trace_id)
    job = parse_job(payload)
    request_body = evidence_store.read_text(job.request_raw_ref) if job.request_raw_ref else ""
    response_body = evidence_store.read_text(job.response_raw_ref) if job.response_raw_ref else ""
    extraction_context = None
    if job.request_raw_ref:
        evidence_dir = job.request_raw_ref.rsplit("/", 1)[0]
        extraction_context = MediaExtractionContext(evidence_store, evidence_dir, trace_id)
    messages, _ = normalize_json_trace(job, request_body, response_body, extraction_context)
    media_assets_extracted = len(extraction_context.assets) if extraction_context else 0
    if extraction_context and extraction_context.assets:
        repository.save_media_assets(
            trace_id,
            extraction_context.assets,
            derived_from=job.request_raw_ref,
            storage_backend=_kwargs.get("storage_backend", "filesystem"),
        )
    if extraction_context and extraction_context.replacements:
        sanitized_ref = extraction_context.write_sanitized_copy(job.request_raw_ref)
        repository.save_derived_evidence_object(
            trace_id,
            sanitized_ref,
            job.request_content_type or "application/json",
            "sanitized",
            job.request_raw_ref,
            storage_backend=_kwargs.get("storage_backend", "filesystem"),
        )

    analysis_result_count = 0
    llm_metadata = {}
    if llm_judge is not None and _core_requested_llm_judge(connection, trace_id):
        contexts = PostgresContextRepository(connection).list_active_contexts()
        assessment = classify_work_relevance(
            job,
            messages,
            contexts,
            llm_judge=llm_judge,
            allow_llm=True,
            strict_llm_errors=True,
        )
        if _assessment_used_llm_judge(assessment):
            result = assessment.to_analysis_result(
                stage=AnalysisStage.ENRICHMENT,
                producer="llm_judge",
                result_key="work_relevance_secondary",
            )
            repository.save_trace_analysis([], [result], [])
            analysis_result_count = 1
            llm_metadata = llm_judge_metadata(assessment)
    return {
        "accepted_trace_id": trace_id,
        "worker_status": "processed",
        "analysis_result_count": analysis_result_count,
        "media_assets_extracted": media_assets_extracted,
        **llm_metadata,
    }


def _core_requested_llm_judge(connection, trace_id: str) -> bool:
    cursor = connection.cursor()
    cursor.execute(
        """
        SELECT COALESCE((result_json->>'llm_judge_requested')::boolean, false)
        FROM analysis_results
        WHERE trace_id = %s
          AND stage = 'core'
          AND category = 'work_relevance'
        ORDER BY id DESC
        LIMIT 1
        """,
        (trace_id,),
    )
    row = cursor.fetchone()
    return bool(row and row[0])


def _assessment_used_llm_judge(assessment) -> bool:
    for item in assessment.evidence:
        if not isinstance(item, dict):
            continue
        if item.get("kind") == "llm_judge" and item.get("source") == "llm_judge":
            return True
    return False


class EnrichmentStageProcessor:
    def __init__(
        self,
        connection,
        evidence_store,
        storage_backend: str = "filesystem",
        llm_judge=None,
        redis_client=None,
        process_enrichment_fn=None,
    ):
        self.connection = connection
        self.evidence_store = evidence_store
        self.storage_backend = storage_backend
        self.llm_judge = llm_judge
        self.redis_client = redis_client
        self.process_enrichment_fn = process_enrichment_fn or default_process_enrichment

    def process(self, trace_id: str) -> dict:
        result = self.process_enrichment_fn(
            trace_id,
            connection=self.connection,
            evidence_store=self.evidence_store,
            storage_backend=self.storage_backend,
            llm_judge=self.llm_judge,
            redis_client=self.redis_client,
        ) or {}
        _mark_trace_enrichment_completed(self.connection, trace_id)
        return {
            "accepted_trace_id": trace_id,
            "worker_status": "processed",
            **result,
        }


def _mark_trace_enrichment_completed(connection, trace_id: str) -> None:
    cursor = connection.cursor()
    cursor.execute(
        """
        UPDATE traces
        SET enrichment_status = 'completed',
            enrichment_completed_at = now(),
            last_analysis_error_code = '',
            updated_at = now()
        WHERE trace_id = %s
        """,
        (trace_id,),
    )
