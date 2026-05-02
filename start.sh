#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="deploy/docker-compose.yml"
ENV_FILE=".env.local"
ENV_EXAMPLE=".env.example"
EVIDENCE_DIR="./var/evidence"
GATEWAY_PORT="${AUDIT_GATEWAY_LISTEN_ADDR:-:8080}"
# 去掉前缀冒号，提取端口号
GATEWAY_PORT_NUM="${GATEWAY_PORT#:}"

# ── 颜色 ──────────────────────────────────────────────
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }

# ── 1. 检查 .env.local ───────────────────────────────
if [ ! -f "$ENV_FILE" ]; then
    warn "$ENV_FILE 不存在，从 $ENV_EXAMPLE 复制"
    cp "$ENV_EXAMPLE" "$ENV_FILE"
    warn "请编辑 $ENV_FILE 设置 NEW_API_BASE_URL 等变量后重新运行"
    exit 1
fi

set -a
source "$ENV_FILE"
set +a

# ── 2. 检查 Docker ───────────────────────────────────
if ! command -v docker &>/dev/null; then
    error "未找到 docker，请先安装 Docker"
    exit 1
fi

if ! docker info &>/dev/null; then
    error "Docker 守护进程未运行，请先启动 Docker"
    exit 1
fi

info "Docker 环境正常"

# ── 3. 启动 PostgreSQL 和 Redis ──────────────────────
info "检查 Docker Compose 服务状态..."
COMPOSE_UP=$(docker compose -f "$COMPOSE_FILE" ps --services --filter "status=running" 2>/dev/null || true)

if echo "$COMPOSE_UP" | grep -q "postgres" && echo "$COMPOSE_UP" | grep -q "redis"; then
    info "PostgreSQL 和 Redis 已在运行"
else
    info "启动 PostgreSQL 和 Redis..."
    docker compose -f "$COMPOSE_FILE" up -d postgres redis

    info "等待服务健康..."
    MAX_WAIT=60
    ELAPSED=0
    while [ $ELAPSED -lt $MAX_WAIT ]; do
        HEALTHY=$(docker compose -f "$COMPOSE_FILE" ps --format json 2>/dev/null \
            | python3 -c "
import sys, json
ok = True
for line in sys.stdin:
    obj = json.loads(line)
    if obj.get('Service') in ('postgres', 'redis'):
        if obj.get('Health') != 'healthy':
            ok = False
print('yes' if ok else 'no')
" 2>/dev/null || echo "no")
        if [ "$HEALTHY" = "yes" ]; then
            break
        fi
        sleep 2
        ELAPSED=$((ELAPSED + 2))
    done

    if [ $ELAPSED -ge $MAX_WAIT ]; then
        error "服务启动超时，请检查 Docker 状态"
        docker compose -f "$COMPOSE_FILE" ps
        exit 1
    fi
    info "PostgreSQL 和 Redis 已启动并健康"
fi

# ── 4. 检查数据库迁移 ────────────────────────────────
MIGRATION_COUNT=$(ls migrations/*.sql 2>/dev/null | wc -l | tr -d ' ')
TABLE_COUNT=$(docker compose -f "$COMPOSE_FILE" exec -T postgres \
    psql -U audit -d audit_gateway -t -c \
    "SELECT count(*) FROM pg_tables WHERE schemaname = 'public';" 2>/dev/null | tr -d ' ')

if [ "$TABLE_COUNT" -ge "${MIGRATION_COUNT:-0}" ] 2>/dev/null; then
    info "数据库迁移已完成 ($TABLE_COUNT 张表)"
else
    info "数据库需要迁移 (当前 $TABLE_COUNT 张表，${MIGRATION_COUNT} 个迁移文件)"
    info "执行数据库迁移..."
    docker compose -f "$COMPOSE_FILE" run --rm migrate
    info "数据库迁移完成"
fi

# ── 5. 创建 evidence 目录 ────────────────────────────
EVIDENCE_ABS="${EVIDENCE_STORAGE_DIR:-$EVIDENCE_DIR}"
mkdir -p "$EVIDENCE_ABS"

# ── 6. 清理函数 ──────────────────────────────────────
cleanup() {
    echo ""
    info "正在停止服务..."
    [ -n "${GO_PID:-}" ] && kill "$GO_PID" 2>/dev/null || true
    [ -n "${PY_PID:-}" ] && kill "$PY_PID" 2>/dev/null || true
    wait 2>/dev/null
    info "服务已停止"
}
trap cleanup EXIT INT TERM

# ── 7. 启动 Go 网关 ──────────────────────────────────
info "启动 Go 网关..."
go run ./cmd/audit-gateway &
GO_PID=$!

# 等待网关端口可用
GATEWAY_READY=false
for i in $(seq 1 15); do
    if curl -sf "http://localhost:${GATEWAY_PORT_NUM}/healthz" >/dev/null 2>&1; then
        GATEWAY_READY=true
        break
    fi
    sleep 1
done

if [ "$GATEWAY_READY" = true ]; then
    info "Go 网关已就绪"
else
    warn "Go 网关在 15 秒内未响应 /healthz，可能仍在启动中"
fi

# ── 8. 启动 Python 分析 Worker ───────────────────────
if command -v uv &>/dev/null; then
    info "启动 Python 分析 Worker..."
    (cd workers/analysis_worker && uv sync --quiet 2>/dev/null && exec uv run python main.py --redis) &
    PY_PID=$!
    info "Python 分析 Worker 已启动 (Redis 模式)"
else
    warn "未找到 uv，跳过 Python 分析 Worker"
    warn "安装 uv: curl -LsSf https://astral.sh/uv/install.sh | sh"
fi

# ── 9. 打印访问信息 ──────────────────────────────────
echo ""
echo -e "${BOLD}${CYAN}══════════════════════════════════════════════════${NC}"
echo -e "${BOLD}${CYAN}  new-api-gateway 已启动${NC}"
echo -e "${BOLD}${CYAN}══════════════════════════════════════════════════${NC}"
echo -e ""
echo -e "  ${BOLD}网关代理:${NC}    http://localhost:${GATEWAY_PORT_NUM}"
echo -e "  ${BOLD}Admin UI:${NC}    http://localhost:${GATEWAY_PORT_NUM}/admin"
echo -e "  ${BOLD}健康检查:${NC}    http://localhost:${GATEWAY_PORT_NUM}/healthz"
echo -e "  ${BOLD}就绪检查:${NC}    http://localhost:${GATEWAY_PORT_NUM}/readyz"
echo -e "  ${BOLD}Prometheus:${NC}  http://localhost:${GATEWAY_PORT_NUM}/metrics"
echo ""
echo -e "  ${BOLD}PostgreSQL:${NC}  localhost:5432  (audit / audit_gateway)"
echo -e "  ${BOLD}Redis:${NC}       localhost:6379"
echo ""
echo -e "  按 ${BOLD}Ctrl+C${NC} 停止所有服务"
echo -e "${BOLD}${CYAN}══════════════════════════════════════════════════${NC}"
echo ""

# 等待任意子进程退出
wait -n 2>/dev/null || wait
