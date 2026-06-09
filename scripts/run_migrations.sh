#!/bin/sh
set -eu

PGHOST="${PGHOST:-postgres}"
PGUSER="${PGUSER:-audit}"
POSTGRES_DB="${POSTGRES_DB:-audit_gateway}"
export PGPASSWORD="${PGPASSWORD:-audit}"

psql_exec() {
  psql -h "$PGHOST" -U "$PGUSER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 "$@"
}

psql_query() {
  psql -h "$PGHOST" -U "$PGUSER" -d "$POSTGRES_DB" -tA "$@"
}

sql_literal() {
  printf "%s" "$1" | sed "s/'/''/g"
}

ensure_schema_migrations_table() {
  psql_exec <<'SQL'
CREATE TABLE IF NOT EXISTS schema_migrations (
    filename TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
SQL
}

migration_recorded() {
  filename="$1"
  escaped_filename="$(sql_literal "$filename")"
  value="$(psql_query -c "SELECT 1 FROM schema_migrations WHERE filename = '${escaped_filename}' LIMIT 1;")"
  [ "$value" = "1" ]
}

mark_migration_recorded() {
  filename="$1"
  escaped_filename="$(sql_literal "$filename")"
  psql_exec -c "
    INSERT INTO schema_migrations (filename)
    VALUES ('${escaped_filename}')
    ON CONFLICT (filename) DO NOTHING;
  " >/dev/null
}

legacy_migration_already_applied() {
  filename="$1"
  case "$filename" in
    0016_analysis_streams_redesign.sql)
      value="$(psql_query -c "
        SELECT CASE WHEN
          EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'traces'
              AND column_name = 'core_status'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'traces'
              AND column_name = 'enrichment_required'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'traces'
              AND column_name = 'enrichment_status'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'traces'
              AND column_name = 'last_analysis_error_code'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.table_constraints
            WHERE table_schema = 'public'
              AND table_name = 'traces'
              AND constraint_name = 'chk_traces_core_status'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.table_constraints
            WHERE table_schema = 'public'
              AND table_name = 'traces'
              AND constraint_name = 'chk_traces_enrichment_status'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'analysis_results'
              AND column_name = 'stage'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'analysis_results'
              AND column_name = 'producer'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'analysis_results'
              AND column_name = 'result_key'
          )
          AND EXISTS (
            SELECT 1
            FROM pg_indexes
            WHERE schemaname = 'public'
              AND indexname = 'idx_analysis_results_trace_stage_producer_result_key'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'raw_evidence_objects'
              AND column_name = 'variant'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'raw_evidence_objects'
              AND column_name = 'derived_from_object_ref'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.tables
            WHERE table_schema = 'public'
              AND table_name = 'analysis_tasks'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.tables
            WHERE table_schema = 'public'
              AND table_name = 'trace_usage_facts'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.tables
            WHERE table_schema = 'public'
              AND table_name = 'analysis_runtime_samples'
          )
        THEN 1 ELSE 0 END;
      ")"
      [ "$value" = "1" ]
      ;;
    0017_analysis_runtime_rate_kpis.sql)
      value="$(psql_query -c "
        SELECT CASE WHEN
          EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'analysis_runtime_samples'
              AND column_name = 'success_rate'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'analysis_runtime_samples'
              AND column_name = 'retryable_fail_rate'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'analysis_runtime_samples'
              AND column_name = 'terminal_fail_rate'
          )
          AND EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'analysis_runtime_samples'
              AND column_name = 'llm_judge_timeout_rate'
          )
        THEN 1 ELSE 0 END;
      ")"
      [ "$value" = "1" ]
      ;;
    *)
      return 1
      ;;
  esac
}

ensure_schema_migrations_table

for path in /migrations/*.sql; do
  [ -e "$path" ] || continue
  filename="$(basename "$path")"

  if migration_recorded "$filename"; then
    echo "skipping $path (already recorded)"
    continue
  fi

  if legacy_migration_already_applied "$filename"; then
    echo "recording $path (already present in legacy schema)"
    mark_migration_recorded "$filename"
    continue
  fi

  echo "applying $path"
  psql_exec -f "$path"
  mark_migration_recorded "$filename"
done
