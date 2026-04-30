package traces

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errNilDatabasePool         = errors.New("traces repository: nil database pool")
	errTraceStartedAtRequired  = errors.New("traces repository: request_started_at is required")
	errTraceCreatedAtRequired  = errors.New("traces repository: created_at is required")
	errObjectCreatedAtRequired = errors.New("traces repository: created_at is required")
)

type Repository interface {
	InsertTrace(ctx context.Context, trace Trace) error
	InsertRawEvidence(ctx context.Context, object RawEvidenceObject) error
}

type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

var _ execer = (*pgxpool.Pool)(nil)

type PostgresRepository struct {
	execer execer
}

func NewPostgresRepository(pool *pgxpool.Pool) PostgresRepository {
	if pool == nil {
		return PostgresRepository{}
	}
	return PostgresRepository{execer: pool}
}

func (r PostgresRepository) InsertTrace(ctx context.Context, trace Trace) error {
	if r.execer == nil {
		return errNilDatabasePool
	}
	if trace.RequestStartedAt.IsZero() {
		return errTraceStartedAtRequired
	}
	if trace.CreatedAt.IsZero() {
		return errTraceCreatedAtRequired
	}

	if trace.UpdatedAt.IsZero() {
		trace.UpdatedAt = trace.CreatedAt
	}

	_, err := r.execer.Exec(ctx, `
INSERT INTO traces (
  trace_id, parent_trace_id, request_id_from_client, new_api_request_id,
  method, path, route_pattern, protocol_family, capture_mode, route_support_level, body_kind,
  status_code, upstream_status_code, stream, request_started_at, response_started_at,
  response_finished_at, duration_ms, client_ip_hash, user_agent_hash,
  request_body_size, response_body_size, request_body_sha256, response_body_sha256,
  request_raw_ref, request_headers_ref, response_raw_ref, response_headers_ref,
  token_fingerprint, fingerprint_display,
  new_api_token_id_snapshot, token_name_snapshot, employee_no_snapshot,
  audit_subject_display_name_snapshot, department_snapshot,
  identity_resolution_status, identity_cache_status, identity_resolved_at,
  model_requested, model_upstream,
  usage_prompt_tokens, usage_completion_tokens, usage_total_tokens, usage_reasoning_tokens,
  usage_cached_tokens, estimated_cost, error_type, error_message_redacted,
  analysis_status, created_at, updated_at
) VALUES (
  $1,$2,$3,$4,
  $5,$6,$7,$8,$9,$10,$11,
  $12,$13,$14,$15,$16,
  $17,$18,$19,$20,
  $21,$22,$23,$24,
  $25,$26,$27,$28,
  $29,$30,
  $31,$32,$33,
  $34,$35,
  $36,$37,$38,
  $39,$40,
  $41,$42,$43,$44,
  $45,$46,$47,$48,
  $49,$50,$51
)`,
		trace.TraceID, trace.ParentTraceID, trace.RequestIDFromClient, trace.NewAPIRequestID,
		trace.Method, trace.Path, trace.RoutePattern, trace.ProtocolFamily, trace.CaptureMode, trace.RouteSupportLevel, trace.BodyKind,
		trace.StatusCode, trace.UpstreamStatusCode, trace.Stream, trace.RequestStartedAt, nullableTime(trace.ResponseStartedAt),
		nullableTime(trace.ResponseFinishedAt), trace.DurationMillis, trace.ClientIPHash, trace.UserAgentHash,
		trace.RequestBodySize, trace.ResponseBodySize, trace.RequestBodySHA256, trace.ResponseBodySHA256,
		trace.RequestRawRef, trace.RequestHeadersRef, trace.ResponseRawRef, trace.ResponseHeadersRef,
		trace.TokenFingerprint, trace.FingerprintDisplay,
		trace.NewAPITokenIDSnapshot, trace.TokenNameSnapshot, trace.EmployeeNoSnapshot,
		trace.AuditSubjectDisplayNameSnapshot, trace.DepartmentSnapshot,
		trace.IdentityResolutionStatus, trace.IdentityCacheStatus, nullableTime(trace.IdentityResolvedAt),
		trace.ModelRequested, trace.ModelUpstream,
		trace.UsagePromptTokens, trace.UsageCompletionTokens, trace.UsageTotalTokens, trace.UsageReasoningTokens,
		trace.UsageCachedTokens, trace.EstimatedCost, trace.ErrorType, trace.ErrorMessageRedacted,
		trace.AnalysisStatus, trace.CreatedAt, trace.UpdatedAt,
	)
	return err
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func (r PostgresRepository) InsertRawEvidence(ctx context.Context, object RawEvidenceObject) error {
	if r.execer == nil {
		return errNilDatabasePool
	}
	if object.CreatedAt.IsZero() {
		return errObjectCreatedAtRequired
	}

	_, err := r.execer.Exec(ctx, `
INSERT INTO raw_evidence_objects (
  trace_id, object_type, object_ref, storage_backend, content_type,
  content_encoding, original_filename, size_bytes, sha256,
  redaction_status, encryption_status, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		object.TraceID, object.ObjectType, object.ObjectRef, object.StorageBackend,
		object.ContentType,
		object.ContentEncoding,
		object.OriginalFilename,
		object.SizeBytes,
		object.SHA256,
		defaultString(object.RedactionStatus, "not_redacted"),
		defaultString(object.EncryptionStatus, "filesystem_permissions"),
		object.CreatedAt,
	)
	return err
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
