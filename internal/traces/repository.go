package traces

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	InsertTrace(ctx context.Context, trace Trace) error
	InsertRawEvidence(ctx context.Context, object RawEvidenceObject) error
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) PostgresRepository {
	return PostgresRepository{pool: pool}
}

func (r PostgresRepository) InsertTrace(ctx context.Context, trace Trace) error {
	_, err := r.pool.Exec(ctx, `
INSERT INTO traces (
  trace_id, method, path, route_pattern, protocol_family, capture_mode,
  status_code, upstream_status_code, stream, request_started_at, response_finished_at,
  duration_ms, request_body_size, response_body_size, request_body_sha256, response_body_sha256,
  request_raw_ref, response_raw_ref, token_fingerprint, fingerprint_display,
  new_api_token_id_snapshot, token_name_snapshot, employee_no_snapshot,
  identity_resolution_status, identity_cache_status, model_requested, analysis_status, created_at
) VALUES (
  $1,$2,$3,$4,$5,$6,
  $7,$8,$9,$10,$11,
  $12,$13,$14,$15,$16,
  $17,$18,$19,$20,
  $21,$22,$23,
  $24,$25,$26,$27,$28
)`,
		trace.TraceID, trace.Method, trace.Path, trace.RoutePattern, trace.ProtocolFamily, trace.CaptureMode,
		trace.StatusCode, trace.UpstreamStatusCode, trace.Stream, trace.RequestStartedAt, trace.ResponseFinishedAt,
		trace.DurationMillis, trace.RequestBodySize, trace.ResponseBodySize, trace.RequestBodySHA256, trace.ResponseBodySHA256,
		trace.RequestRawRef, trace.ResponseRawRef, trace.TokenFingerprint, trace.FingerprintDisplay,
		trace.NewAPITokenIDSnapshot, trace.TokenNameSnapshot, trace.EmployeeNoSnapshot,
		trace.IdentityResolutionStatus, trace.IdentityCacheStatus, trace.ModelRequested, trace.AnalysisStatus, trace.CreatedAt,
	)
	return err
}

func (r PostgresRepository) InsertRawEvidence(ctx context.Context, object RawEvidenceObject) error {
	_, err := r.pool.Exec(ctx, `
INSERT INTO raw_evidence_objects (
  trace_id, object_type, object_ref, storage_backend, content_type, size_bytes, sha256, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		object.TraceID, object.ObjectType, object.ObjectRef, object.StorageBackend,
		object.ContentType, object.SizeBytes, object.SHA256, object.CreatedAt,
	)
	return err
}
