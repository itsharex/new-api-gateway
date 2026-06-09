#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="${COMPOSE_FILE:-deploy/docker-compose.yml}"
ENV_FILE="${ENV_FILE:-.env.local}"
DB_NAME="${1:-migration_reentrancy_test}"

if [ ! -f "$ENV_FILE" ] && [ -f "../../.env.local" ]; then
  ENV_FILE="../../.env.local"
fi

cleanup() {
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" exec -T postgres \
    psql -U audit -d postgres -c "DROP DATABASE IF EXISTS ${DB_NAME} WITH (FORCE)" >/dev/null
}
trap cleanup EXIT

docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d postgres >/dev/null

docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" exec -T postgres \
  psql -U audit -d postgres -c "DROP DATABASE IF EXISTS ${DB_NAME} WITH (FORCE)" >/dev/null
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" exec -T postgres \
  psql -U audit -d postgres -c "CREATE DATABASE ${DB_NAME}" >/dev/null

echo "== First migrate run =="
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" --profile tools \
  run --rm -e POSTGRES_DB="${DB_NAME}" migrate

echo
echo "== Second migrate run =="
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" --profile tools \
  run --rm -e POSTGRES_DB="${DB_NAME}" migrate
