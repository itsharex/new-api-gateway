#!/usr/bin/env bash
set -euo pipefail

# OSS evidence storage e2e test runner.
# Stops the current Gateway, restarts it with EVIDENCE_STORAGE_BACKEND=oss,
# runs the OSS e2e tests, and cleans up on exit.
#
# Required environment variables:
#   OSS_ENDPOINT, OSS_BUCKET, OSS_ACCESS_KEY_ID, OSS_ACCESS_KEY_SECRET
#
# Usage:
#   export OSS_ENDPOINT=oss-cn-hangzhou.aliyuncs.com
#   export OSS_BUCKET=my-audit-evidence
#   export OSS_ACCESS_KEY_ID=...
#   export OSS_ACCESS_KEY_SECRET=...
#   ./scripts/e2e_oss_pipeline.sh

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly GATEWAY_URL="${AUDIT_GATEWAY_URL:-http://localhost:8080}"

# ---------------------------------------------------------------------------
# Check prerequisites
# ---------------------------------------------------------------------------

for var in OSS_ENDPOINT OSS_BUCKET OSS_ACCESS_KEY_ID OSS_ACCESS_KEY_SECRET; do
    if [[ -z "${!var:-}" ]]; then
        echo "ERROR: $var is required. Set it before running this script." >&2
        exit 1
    fi
done

echo "=== OSS E2E Test Runner ==="
echo "  OSS_ENDPOINT=$OSS_ENDPOINT"
echo "  OSS_BUCKET=$OSS_BUCKET"

# ---------------------------------------------------------------------------
# Stop existing Gateway
# ---------------------------------------------------------------------------

echo ""
echo "=== Stopping existing Gateway ==="
gateway_pid=$(pgrep -f "audit-gateway" || true)
if [[ -n "$gateway_pid" ]]; then
    echo "  Found Gateway PID(s): $(echo $gateway_pid | tr '\n' ' ')"
    echo $gateway_pid | xargs kill 2>/dev/null || true
    sleep 2
    # Force kill if still running
    for pid in $gateway_pid; do
        if kill -0 "$pid" 2>/dev/null; then
            echo "  Force killing PID $pid"
            kill -9 "$pid" 2>/dev/null || true
        fi
    done
    echo "  Gateway stopped"
else
    echo "  No running Gateway found"
fi

# ---------------------------------------------------------------------------
# Start Gateway with OSS backend
# ---------------------------------------------------------------------------

echo ""
echo "=== Starting Gateway with OSS backend ==="
(
    cd "$REPO_ROOT"
    EVIDENCE_STORAGE_BACKEND=oss \
    OSS_ENDPOINT="$OSS_ENDPOINT" \
    OSS_BUCKET="$OSS_BUCKET" \
    OSS_ACCESS_KEY_ID="$OSS_ACCESS_KEY_ID" \
    OSS_ACCESS_KEY_SECRET="$OSS_ACCESS_KEY_SECRET" \
    go run ./cmd/audit-gateway &>/tmp/oss-e2e-gateway.log
) &
GATEWAY_PID=$!
echo "  Gateway PID: $GATEWAY_PID"

# Ensure cleanup on exit
cleanup() {
    echo ""
    echo "=== Cleaning up ==="
    if kill -0 "$GATEWAY_PID" 2>/dev/null; then
        echo "  Stopping Gateway PID $GATEWAY_PID"
        kill "$GATEWAY_PID" 2>/dev/null || true
        sleep 1
        kill -9 "$GATEWAY_PID" 2>/dev/null || true
    fi
    echo "  Done"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Wait for Gateway to be ready
# ---------------------------------------------------------------------------

echo ""
echo "=== Waiting for Gateway ==="
max_attempts=30
for i in $(seq 1 $max_attempts); do
    if curl -sf "$GATEWAY_URL/healthz" >/dev/null 2>&1; then
        echo "  Gateway ready (attempt $i)"
        break
    fi
    if [[ $i -eq $max_attempts ]]; then
        echo "ERROR: Gateway did not become ready after $max_attempts attempts" >&2
        echo "  Last log lines:"
        tail -20 /tmp/oss-e2e-gateway.log 2>/dev/null || true
        exit 1
    fi
    sleep 1
done

# ---------------------------------------------------------------------------
# Run e2e tests
# ---------------------------------------------------------------------------

echo ""
echo "=== Running OSS pipeline e2e test ==="
cd "$REPO_ROOT/e2e"
uv run test_gateway_worker_pipeline_oss.py

echo ""
echo "=== Running OSS media extraction e2e test ==="
uv run test_media_extraction_oss.py

echo ""
echo "=== All OSS e2e tests passed ==="
