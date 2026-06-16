# Docker-Native E2E 重构实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 e2e 从「宿主机 `go run` 网关 + 直连依赖」改为「compose `profile=e2e` 服务,复用部署的网关与常驻 worker」,收敛为 5 个 backend-agnostic 端到端用例。

**Architecture:** e2e 作为 `deploy/docker-compose.yml` 的 on-demand 服务,加入 compose 网络用 service 名直连;测试改 pytest;网关→worker 全链路依赖常驻 worker 消费 Redis Streams(`analysis.core`),用例轮询 `analysis_results` 而非手动投 list/跑 `--redis-once`。

**Tech Stack:** docker compose、pytest、psycopg、redis-py、requests、uv。

**Spec:** `docs/superpowers/specs/2026-06-16-docker-native-e2e-design.md`

**重要说明 — 偏离标准 TDD:** 本计划重构的是**测试基础设施本身**(无产品功能可测),故采用「实现 → 验证 → 提交」节奏。验证手段:compose 改动用 `docker compose config`;Python 改动用 `python -m py_compile`(语法)+ e2e 容器内 import;最终在 Task 12 用 `docker compose --profile e2e run` 跑全套集成验证。

**前置条件(执行前确认):**
- docker 栈已 `make dev` / `docker compose up -d` 运行(audit-gateway、analysis-worker、analysis-enrichment-worker、analysis-batch、postgres、redis、new-api)。
- new-api 已配置 `gpt-5.4`(OpenAI 系)与 `claude-sonnet-4-6`(Claude 系)两个可用 channel。
- `.env.local` 存在,含 `AUDIT_HMAC_SECRET`、`NEW_API_BASE_URL`、`NEW_API_POSTGRES_DSN`、`OSS_*` 等。

---

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `deploy/docker-compose.yml` | 修改 | 给 `audit-gateway` 加 healthcheck;新增 `e2e` service(profile=e2e) |
| `e2e/pyproject.toml` | 修改 | 加 `pytest` 依赖 |
| `e2e/helpers.py` | 重写 | 保留 HTTP + DB 断言工具,删 worker/go-run/list-job 旧逻辑;env 统一 |
| `e2e/conftest.py` | 新建 | session 级 prerequisites、autouse errors 重置、db fixture |
| `e2e/test_smoke.py` | 重写 | pytest 化 |
| `e2e/test_gateway_openai.py` | 重写 | pytest 化,去重 helpers |
| `e2e/test_gateway_claude.py` | 重写 | pytest 化 |
| `e2e/test_gateway_worker_pipeline.py` | 重写 | pytest 化 + streams 常驻 worker |
| `e2e/test_media_extraction.py` | 重写 | pytest 化 + streams + 共享 evidence volume |
| `e2e/test_worker_anomaly_coverage.py` | 删除 | 回归 pytest |
| `e2e/test_worker_work_relevance.py` | 删除 | 回归 pytest |
| `e2e/test_gateway_worker_pipeline_oss.py` | 删除 | 合并,backend-agnostic 取代 |
| `e2e/test_media_extraction_oss.py` | 删除 | 合并 |
| `e2e/run_all.py` | 删除 | pytest 直接跑 `e2e/` 取代 |
| `.env.example` | 修改 | 增补 `E2E_OPENAI_MODEL`/`E2E_CLAUDE_MODEL`/`E2E_API_KEY` |
| `CLAUDE.md`/`README.md`/`AGENTS.md`/`ARCHITECTURE.md` | 修改 | e2e 触发命令、放弃 go run、worker e2e 回归 pytest |

---

## Task 1: compose — 加 audit-gateway healthcheck 与 e2e service

**Files:**
- Modify: `deploy/docker-compose.yml`

**背景:** `audit-gateway` 当前无 healthcheck,e2e 的 `depends_on: audit-gateway: { condition: service_healthy }` 需要它。`postgres`/`redis` 不发布宿主端口(内部网络设计),e2e 容器在 compose 网络内用 service 名直连,无需端口发布。e2e 与网关/worker 共享同一个 host evidence 目录,以便媒体用例读取证据文件。

- [ ] **Step 1: 给 `audit-gateway` 加 healthcheck**

在 `deploy/docker-compose.yml` 的 `audit-gateway` service(当前约 30-54 行)内,`restart: unless-stopped` 之前加入 healthcheck。最终该 service 的结尾应是:

```yaml
    volumes:
      - ${EVIDENCE_HOST_DIR:-./var/evidence}:/evidence
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://localhost:8080/healthz | grep -q '\"status\":\"ok\"' || exit 1"]
      interval: 5s
      timeout: 3s
      retries: 20
      start_period: 5s
    restart: unless-stopped
```

(镜像基于常用 Linux,含 `wget`。若实测镜像无 `wget`,改用 `curl`;`Dockerfile` 在 `deploy/Dockerfile`,确认其中安装了其一。)

- [ ] **Step 2: 在 on-demand 区段新增 `e2e` service**

在 `deploy/docker-compose.yml` 文件末尾 `migrate:` service 之后、`volumes:` 之前,加入:

```yaml
  e2e:
    profiles:
      - e2e
    image: ghcr.io/astral-sh/uv:python3.11-bookworm
    working_dir: /workspace/e2e
    depends_on:
      audit-gateway: { condition: service_healthy }
      postgres:      { condition: service_healthy }
      redis:         { condition: service_healthy }
    extra_hosts:
      - "host.docker.internal:host-gateway"
    environment:
      AUDIT_GATEWAY_URL: http://audit-gateway:8080
      NEW_API_BASE_URL: ${NEW_API_BASE_URL}
      NEW_API_POSTGRES_DSN: ${NEW_API_POSTGRES_DSN:-}
      POSTGRES_DSN: postgres://audit:audit@postgres:5432/audit_gateway?sslmode=disable
      REDIS_URL: redis://redis:6379/0
      E2E_API_KEY: ${E2E_API_KEY:-sk-G0YzOkt9WQAwp8S9DL9mLKlcFNEYRjdnA4x6PMrNRgZA05l8}
      E2E_OPENAI_MODEL: ${E2E_OPENAI_MODEL:-gpt-5.4}
      E2E_CLAUDE_MODEL: ${E2E_CLAUDE_MODEL:-claude-sonnet-4-6}
      EVIDENCE_STORAGE_BACKEND: ${EVIDENCE_STORAGE_BACKEND:-filesystem}
      EVIDENCE_STORAGE_DIR: /evidence
      OSS_ENDPOINT: ${OSS_ENDPOINT:-}
      OSS_BUCKET: ${OSS_BUCKET:-}
      OSS_ACCESS_KEY_ID: ${OSS_ACCESS_KEY_ID:-}
      OSS_ACCESS_KEY_SECRET: ${OSS_ACCESS_KEY_SECRET:-}
      UV_PROJECT_ENVIRONMENT: /tmp/e2e-venv
    volumes:
      - ..:/workspace
      - ${EVIDENCE_HOST_DIR:-./var/evidence}:/evidence
    command: ["uv", "run", "pytest", "-q"]
```

要点:`working_dir: /workspace/e2e`(e2e 有自己的 `pyproject.toml`,`uv` 在此解析依赖);`..:/workspace` 挂载仓库根;evidence volume 与网关/worker 共享同一 host 目录,媒体用例经 `/evidence` 读取证据。

- [ ] **Step 3: 校验 compose 语法 + 应用 healthcheck**

```bash
docker compose -f deploy/docker-compose.yml --env-file .env.local config -q && echo "config OK"
docker compose -f deploy/docker-compose.yml --env-file .env.local up -d audit-gateway
docker inspect --format '{{.State.Health.Status}}' new-api-gateway-audit-gateway-1
```
Expected: `config OK`;最后输出 `healthy`(等 ~10s)。

- [ ] **Step 4: Commit**

```bash
git add deploy/docker-compose.yml
git commit -m "feat(compose): add audit-gateway healthcheck and profile=e2e service"
```

---

## Task 2: e2e/pyproject.toml 加 pytest 依赖

**Files:**
- Modify: `e2e/pyproject.toml`

- [ ] **Step 1: 加 pytest**

把 `dependencies` 改为:

```toml
[project]
name = "new-api-gateway-e2e"
version = "0.1.0"
description = "End-to-end tests for the new-api audit gateway"
requires-python = ">=3.11"
dependencies = [
    "requests>=2.31.0",
    "psycopg[binary]>=3.2.0",
    "redis>=5.0.0",
    "oss2>=2.19.0",
    "pytest>=8.0.0",
]

[tool.uv]
package = false
```

- [ ] **Step 2: 校验 lockfile 更新**

```bash
cd e2e && uv lock -q && uv run --frozen python -c "import pytest; print(pytest.__version__)" && cd ..
```
Expected: 打印 pytest 版本号,无报错。

- [ ] **Step 3: Commit**

```bash
git add e2e/pyproject.toml e2e/uv.lock
git commit -m "chore(e2e): add pytest dependency"
```

---

## Task 3: helpers.py 重写 + conftest.py 新建

**Files:**
- Rewrite: `e2e/helpers.py`
- Create: `e2e/conftest.py`

**背景:** 删除所有 worker/go-run/redis-list-job 相关(`GatewayManager`、`run_worker_once`、`create_test_database`、`apply_migrations`、`flush_redis`、`build_job_payload`、`read_trace_for_job`、`TRACE_FIELDS_FOR_JOB`、`report_results`)。保留 HTTP + DB 断言工具。env 统一为 `E2E_*`。`errors` 保持模块级 list,由 conftest autouse fixture 在每个 test 前清空。

- [ ] **Step 1: 重写 `e2e/helpers.py`**

完整新内容:

```python
#!/usr/bin/env python3
"""Shared helpers for e2e tests (pytest).

Connects to the deployed docker stack via service names (no host port
publishing, no go-run gateway). All endpoints/models are env-driven.
"""

from __future__ import annotations

import os
import sys
import time

import psycopg
import requests

# ---------------------------------------------------------------------------
# Config (env-driven; defaults match the compose e2e service)
# ---------------------------------------------------------------------------

GATEWAY_URL = os.environ.get("AUDIT_GATEWAY_URL", "http://audit-gateway:8080").rstrip("/")
UPSTREAM_URL = os.environ.get("NEW_API_BASE_URL", "http://host.docker.internal:3000").rstrip("/")
API_KEY = os.environ.get("E2E_API_KEY", "sk-G0YzOkt9WQAwp8S9DL9mLKlcFNEYRjdnA4x6PMrNRgZA05l8")
OPENAI_MODEL = os.environ.get("E2E_OPENAI_MODEL", "gpt-5.4")
CLAUDE_MODEL = os.environ.get("E2E_CLAUDE_MODEL", "claude-sonnet-4-6")
PG_DSN = os.environ.get(
    "POSTGRES_DSN",
    "postgres://audit:audit@postgres:5432/audit_gateway?sslmode=disable",
)
REDIS_URL = os.environ.get("REDIS_URL", "redis://redis:6379/0")
EVIDENCE_STORAGE_BACKEND = os.environ.get("EVIDENCE_STORAGE_BACKEND", "filesystem")
EVIDENCE_STORAGE_DIR = os.environ.get("EVIDENCE_STORAGE_DIR", "/evidence")
EXPECTED_USERNAME = "dave.zhao"

HEADERS = {
    "Authorization": f"Bearer {API_KEY}",
    "Content-Type": "application/json",
}

_http = requests.Session()
_http.trust_env = False  # bypass local proxies (Clash/V2Ray)

# ---------------------------------------------------------------------------
# Assertion helpers (collect into module-level `errors`; conftest resets it)
# ---------------------------------------------------------------------------

TraceResult = dict[str, str | int | None]

errors: list[str] = []


def check(label: str, condition: bool, detail: str = "") -> None:
    if not condition:
        msg = f"FAIL [{label}] {detail}" if detail else f"FAIL [{label}]"
        errors.append(msg)
        print(msg)


def eq(context: str, field: str, got: object, want: object) -> None:
    check(f"{context}.{field}", got == want, f"got={got!r} want={want!r}")


def not_empty(context: str, field: str, got: object) -> None:
    check(f"{context}.{field}", bool(got), "got empty/zero value")


def starts_with(context: str, field: str, got: str, prefix: str) -> None:
    check(f"{context}.{field}", got.startswith(prefix), f"got={got!r} want prefix={prefix!r}")


def gt(context: str, field: str, got: int, threshold: int) -> None:
    check(f"{context}.{field}", got > threshold, f"got={got} want > {threshold}")


def bail(msg: str) -> None:
    print(f"FATAL: {msg}", file=sys.stderr)
    raise AssertionError(msg)


def assert_no_errors() -> None:
    """Call at the end of each test: fail if any assertion was recorded."""
    if errors:
        raise AssertionError(f"{len(errors)} assertion(s) failed:\n" + "\n".join(f"  {e}" for e in errors))


# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------


def gateway_post(endpoint: str, body: dict, label: str) -> tuple[requests.Response | None, str]:
    url = f"{GATEWAY_URL}{endpoint}"
    try:
        resp = _http.post(url, headers=HEADERS, json=body, timeout=60)
    except requests.RequestException as exc:
        msg = f"connection error: {exc}"
        check(label, False, msg)
        return None, msg
    if resp.status_code >= 300:
        msg = f"status={resp.status_code} body={resp.text[:200]}"
        check(label, False, msg)
        return resp, msg
    return resp, ""


def gateway_stream(endpoint: str, body: dict, label: str) -> tuple[requests.Response | None, str]:
    url = f"{GATEWAY_URL}{endpoint}"
    try:
        resp = _http.post(url, headers=HEADERS, json=body, stream=True, timeout=60)
    except requests.RequestException as exc:
        msg = f"connection error: {exc}"
        check(label, False, msg)
        return None, msg
    if resp.status_code >= 300:
        msg = f"status={resp.status_code}"
        check(label, False, msg)
        return resp, msg
    for _ in resp.iter_lines():
        pass
    return resp, ""


def preflight(endpoint: str, body: dict, model_label: str = "") -> None:
    """Verify upstream new-api is reachable and the model resolves."""
    print("=== Pre-flight check ===")
    if not API_KEY:
        bail("E2E_API_KEY is empty")
    try:
        resp = _http.post(f"{UPSTREAM_URL}{endpoint}", headers=HEADERS, json=body, timeout=30)
    except requests.ConnectionError as exc:
        bail(f"Cannot reach upstream at {UPSTREAM_URL}: {exc}")
    if resp.status_code != 200:
        label = f"model={model_label}" if model_label else f"endpoint={endpoint}"
        bail(f"Upstream returned {resp.status_code} for {label}: {resp.text[:500]}")
    info = f"model={model_label}" if model_label else f"endpoint={endpoint}"
    print(f"  Upstream OK ({info}, status={resp.status_code})")


# ---------------------------------------------------------------------------
# DB helpers
# ---------------------------------------------------------------------------

TRACE_FIELDS = """
    trace_id, identity_resolution_status, username_snapshot,
    token_fingerprint, fingerprint_display,
    protocol_family, capture_mode, status_code,
    request_body_size, response_body_size,
    request_raw_ref, response_raw_ref,
    model_requested, model_upstream,
    usage_total_tokens, usage_prompt_tokens, usage_completion_tokens
""".strip().replace("\n", "").replace("  ", " ")


def wait_for_traces(trace_ids: list[str], timeout: int = 10) -> None:
    print(f"\n  Waiting for {len(trace_ids)} trace(s) to appear in DB ...")
    found = 0
    for attempt in range(timeout):
        with psycopg.connect(PG_DSN) as conn:
            found = conn.execute(
                "SELECT count(*) FROM traces WHERE trace_id = ANY(%s)",
                (trace_ids,),
            ).fetchone()[0]
            if found >= len(trace_ids):
                print(f"  All {found} trace(s) found (attempt {attempt + 1})")
                return
        print(f"  Waiting ({found}/{len(trace_ids)}, attempt {attempt + 1})...")
        time.sleep(1)
    print(f"  WARNING: only {found}/{len(trace_ids)} traces found after {timeout}s")


def wait_for_rows(sql: str, params: tuple, expected: int, timeout: int = 30, label: str = "rows") -> int:
    """Poll until a query returns >= expected rows; returns the count found."""
    for attempt in range(timeout):
        with psycopg.connect(PG_DSN) as conn:
            n = conn.execute(sql, params).fetchone()[0]
        if n >= expected:
            print(f"  {label}: {n} found (attempt {attempt + 1})")
            return n
        print(f"  {label}: {n}/{expected} (attempt {attempt + 1})...")
        time.sleep(1)
    print(f"  WARNING: only {n}/{expected} {label} after {timeout}s")
    return n


def assert_trace_fields(
    conn: psycopg.Connection,
    trace_id: str,
    ctx: str,
    protocol_family: str,
    model: str | None = None,
    require_usage: bool = True,
) -> str | None:
    print(f"  Checking {ctx} (trace_id={trace_id})")
    cur = conn.execute(f"SELECT {TRACE_FIELDS} FROM traces WHERE trace_id = %s", (trace_id,))
    row = cur.fetchone()
    if row is None:
        check(f"{ctx}.trace_exists", False, "no row in traces table")
        return None
    col_names = [desc.name for desc in cur.description]
    t = dict(zip(col_names, row))

    eq(ctx, "identity_resolution_status", t["identity_resolution_status"], "resolved")
    eq(ctx, "username_snapshot", t["username_snapshot"], EXPECTED_USERNAME)
    not_empty(ctx, "token_fingerprint", t["token_fingerprint"])
    starts_with(ctx, "fingerprint_display", t["fingerprint_display"], "tkfp_")
    eq(ctx, "protocol_family", t["protocol_family"], protocol_family)
    eq(ctx, "capture_mode", t["capture_mode"], "raw_and_normalized")
    eq(ctx, "status_code", t["status_code"], 200)
    gt(ctx, "request_body_size", t["request_body_size"], 0)
    gt(ctx, "response_body_size", t["response_body_size"], 0)
    not_empty(ctx, "request_raw_ref", t["request_raw_ref"])
    not_empty(ctx, "response_raw_ref", t["response_raw_ref"])
    if model:
        eq(ctx, "model_requested", t["model_requested"], model)
    if require_usage:
        gt(ctx, "usage_total_tokens", t["usage_total_tokens"], 0)
        gt(ctx, "usage_prompt_tokens", t["usage_prompt_tokens"], 0)
    else:
        print(f"    usage_total_tokens={t['usage_total_tokens']} (upstream may omit usage)")
    if t["model_upstream"]:
        not_empty(ctx, "model_upstream", t["model_upstream"])
    return t.get("token_fingerprint")


def assert_evidence_objects(conn: psycopg.Connection, trace_id: str, ctx: str) -> None:
    rows = conn.execute(
        "SELECT object_type FROM raw_evidence_objects WHERE trace_id = %s",
        (trace_id,),
    ).fetchall()
    types_found = {row[0] for row in rows}
    check(f"{ctx}.evidence.request_body", "request_body" in types_found, f"object_types={types_found}")
    check(f"{ctx}.evidence.response_body", "response_body" in types_found, f"object_types={types_found}")


def assert_identity_cache(conn: psycopg.Connection, fingerprint: str) -> None:
    row = conn.execute(
        "SELECT username FROM token_identity_cache WHERE token_fingerprint = %s",
        (fingerprint,),
    ).fetchone()
    check(f"identity_cache({fingerprint[:12]}…)", row is not None, "no cache entry")
    if row:
        eq("identity_cache.username", "username", row[0], EXPECTED_USERNAME)


def read_request_raw_ref(trace_id: str) -> str:
    """Return the trace's request_raw_ref (for evidence-file inspection)."""
    with psycopg.connect(PG_DSN) as conn:
        row = conn.execute(
            "SELECT request_raw_ref FROM traces WHERE trace_id = %s",
            (trace_id,),
        ).fetchone()
    return row[0] if row else ""
```

- [ ] **Step 2: 新建 `e2e/conftest.py`**

```python
"""pytest config: prerequisites gate + per-test errors reset + fixtures."""

from __future__ import annotations

import os

import psycopg
import pytest
import redis
import requests

import helpers
from helpers import PG_DSN, REDIS_URL


# ---------------------------------------------------------------------------
# Session-scoped prerequisites (fail fast before any test runs)
# ---------------------------------------------------------------------------


@pytest.fixture(scope="session", autouse=True)
def prerequisites():
    print("\n=== Prerequisites Check ===")
    failures: list[str] = []

    # audit-gateway healthz
    try:
        r = requests.get(f"{helpers.GATEWAY_URL}/healthz", timeout=5)
        assert r.status_code == 200
        print("  ✓ audit-gateway")
    except Exception as exc:
        failures.append(f"audit-gateway ({helpers.GATEWAY_URL}): {exc}")

    # postgres + migration count
    try:
        with psycopg.connect(PG_DSN) as conn:
            n = conn.execute("SELECT count(*) FROM schema_migrations").fetchone()[0]
        assert n >= 19, f"expected >=19 migrations, got {n}"
        print(f"  ✓ postgres (migrations={n})")
    except Exception as exc:
        failures.append(f"postgres: {exc}")

    # redis
    try:
        redis.Redis.from_url(REDIS_URL).ping()
        print("  ✓ redis")
    except Exception as exc:
        failures.append(f"redis: {exc}")

    # new-api reachable
    try:
        requests.get(f"{helpers.UPSTREAM_URL}/", timeout=5)
        print("  ✓ new-api")
    except Exception as exc:
        failures.append(f"new-api ({helpers.UPSTREAM_URL}): {exc}")

    # model availability (probe each required model via a minimal request)
    for model in (helpers.OPENAI_MODEL, helpers.CLAUDE_MODEL):
        try:
            resp = requests.post(
                f"{helpers.UPSTREAM_URL}/v1/chat/completions",
                headers=helpers.HEADERS,
                json={"model": model, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
                timeout=30,
            )
            assert resp.status_code == 200, f"model {model}: {resp.status_code} {resp.text[:200]}"
            print(f"  ✓ model {model}")
        except Exception as exc:
            failures.append(f"model {model} unavailable: {exc}")

    if failures:
        pytest.exit("Prerequisites failed:\n  " + "\n  ".join(failures), returncode=1)
    yield


# ---------------------------------------------------------------------------
# Per-test: reset the module-level errors collector
# ---------------------------------------------------------------------------


@pytest.fixture(autouse=True)
def _reset_errors():
    helpers.errors.clear()
    yield


# ---------------------------------------------------------------------------
# Shared fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def db():
    conn = psycopg.connect(PG_DSN)
    yield conn
    conn.close()
```

- [ ] **Step 3: 语法校验**

```bash
python -m py_compile e2e/helpers.py e2e/conftest.py && echo "compile OK"
```
Expected: `compile OK`。

- [ ] **Step 4: 容器内 import 校验**

```bash
docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e uv run python -c "import helpers, conftest; print('import OK')"
```
Expected: `import OK`。(首次会 `uv sync` 安装 pytest,稍慢。)

- [ ] **Step 5: Commit**

```bash
git add e2e/helpers.py e2e/conftest.py
git commit -m "refactor(e2e): rewrite helpers (drop go-run/list-job), add conftest prerequisites"
```

---

## Task 4: 删除 worker 单元 e2e + run_all.py,核对 pytest 覆盖

**Files:**
- Delete: `e2e/test_worker_anomaly_coverage.py`
- Delete: `e2e/test_worker_work_relevance.py`
- Delete: `e2e/run_all.py`

**背景:** 两个 worker 单元用例在隔离库/队列跑一次 worker 验证逻辑,与 `workers/analysis_worker/tests/` 的 pytest 重叠(`test_rules`/`test_pipeline`/`test_work_relevance`/`test_isolation_forest`)。docker-native e2e 聚焦端到端,worker 单元逻辑回归 pytest。`run_all.py`(GatewayManager/go-run 编排)被 pytest 取代。

- [ ] **Step 1: 核对 pytest 覆盖了原 worker e2e 的断言点**

```bash
cd workers/analysis_worker
grep -rn "coverage_alert\|anomaly_count\|work_relevance" tests/ | head -40
```
Expected: 看到 `test_rules.py`/`test_pipeline.py`/`test_work_relevance.py` 中对异常覆盖计数、work_relevance 标签的断言。若关键断言缺失,在对应 pytest 文件补一个用例再继续(本计划假定已覆盖)。

- [ ] **Step 2: 删除三个文件**

```bash
git rm e2e/test_worker_anomaly_coverage.py e2e/test_worker_work_relevance.py e2e/run_all.py
```

- [ ] **Step 3: 确认 worker pytest 仍绿**

```bash
cd workers/analysis_worker && uv run pytest -q && cd ../..
```
Expected: 全部 PASS(确认删除 e2e worker 用例未丢逻辑覆盖,pytest 仍在)。

- [ ] **Step 4: Commit**

```bash
git commit -m "refactor(e2e): remove worker unit e2e and run_all.py (covered by pytest)"
```

---

## Task 5: test_smoke.py pytest 化

**Files:**
- Rewrite: `e2e/test_smoke.py`

**背景:** 三协议各一请求(`openai_chat`/`openai_responses`/`claude_messages`),验证 trace 落库 + 证据对象。改 pytest,模型从 `helpers.OPENAI_MODEL`/`CLAUDE_MODEL` 读,断言用 helpers 工具 + 末尾 `assert_no_errors()`。

- [ ] **Step 1: 重写 `e2e/test_smoke.py`**

```python
"""Smoke test: one request per protocol endpoint, verify trace captured.

Covers OpenAI Chat, OpenAI Responses, Claude Messages.
Backend-agnostic: evidence object_ref prefix is not asserted here.
"""

from __future__ import annotations

import psycopg

import helpers
from helpers import (
    CLAUDE_MODEL,
    OPENAI_MODEL,
    assert_evidence_objects,
    assert_no_errors,
    assert_trace_fields,
    bail,
    gateway_post,
    preflight,
    wait_for_traces,
)

CASES = [
    ("/v1/chat/completions",
     {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 5},
     "openai_chat"),
    ("/v1/responses",
     {"model": OPENAI_MODEL, "input": "ping", "max_output_tokens": 5},
     "openai_responses"),
    ("/v1/messages",
     {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 5},
     "claude_messages"),
]


def test_smoke_three_protocols():
    preflight(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=OPENAI_MODEL,
    )

    results = []
    for endpoint, body, proto in CASES:
        resp, err = gateway_post(endpoint, body, proto)
        if err or resp is None:
            continue
        trace_id = resp.headers.get("x-audit-trace-id", "")
        print(f"  {proto}: status={resp.status_code} trace_id={trace_id}")
        results.append((proto, trace_id))

    trace_ids = [tid for _, tid in results if tid]
    if not trace_ids:
        bail("No trace IDs received from any endpoint")

    wait_for_traces(trace_ids)

    with psycopg.connect(helpers.PG_DSN) as conn:
        for proto, trace_id in results:
            if not trace_id:
                continue
            assert_trace_fields(conn, trace_id, proto, proto, require_usage=False)
            assert_evidence_objects(conn, trace_id, proto)

    assert_no_errors()
```

- [ ] **Step 2: 语法校验**

```bash
python -m py_compile e2e/test_smoke.py && echo "compile OK"
```

- [ ] **Step 3: 容器内单跑(集成验证)**

```bash
docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e uv run pytest test_smoke.py -q
```
Expected: 1 passed。

- [ ] **Step 4: Commit**

```bash
git add e2e/test_smoke.py
git commit -m "refactor(e2e): convert test_smoke to pytest"
```

---

## Task 6: test_gateway_openai.py pytest 化(去重 helpers)

**Files:**
- Rewrite: `e2e/test_gateway_openai.py`

**背景:** 原文件自带一份 helpers 副本(`_gateway_post`/`check`/`eq`/`preflight`/`TRACE_FIELDS`/`assert_traces`)。统一改用共享 `helpers`。保留覆盖:chat 两轮、responses 两轮、chat stream、responses stream;trace 字段 + 证据 + identity cache。

- [ ] **Step 1: 重写 `e2e/test_gateway_openai.py`**

```python
"""E2E: gateway captures OpenAI-format requests (chat + responses, stream/non-stream)."""

from __future__ import annotations

import psycopg

import helpers
from helpers import (
    OPENAI_MODEL,
    assert_evidence_objects,
    assert_identity_cache,
    assert_no_errors,
    assert_trace_fields,
    gateway_post,
    gateway_stream,
    preflight,
    wait_for_traces,
)

PROTOCOL_FAMILY = {
    "/v1/chat/completions": "openai_chat",
    "/v1/responses": "openai_responses",
}


def _post(endpoint, body, label):
    return gateway_post(endpoint, body, label)


def test_gateway_openai_capture():
    preflight(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=OPENAI_MODEL,
    )

    results = []  # (endpoint, turn, trace_id)

    # chat turn 1
    r1, e1 = _post("/v1/chat/completions",
                   {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "hello"}], "max_tokens": 10},
                   "/v1/chat/completions:turn1")
    if not e1 and r1 is not None:
        reply1 = r1.json()["choices"][0]["message"].get("content", "") or ""
        results.append(("/v1/chat/completions", 1, r1.headers.get("x-audit-trace-id", "")))
        # chat turn 2 (multi-turn)
        r2, e2 = _post("/v1/chat/completions",
                       {"model": OPENAI_MODEL,
                        "messages": [{"role": "user", "content": "hello"},
                                     {"role": "assistant", "content": reply1},
                                     {"role": "user", "content": "what is 1+1?"}],
                        "max_tokens": 10},
                       "/v1/chat/completions:turn2")
        if not e2 and r2 is not None:
            results.append(("/v1/chat/completions", 2, r2.headers.get("x-audit-trace-id", "")))

    # responses turn 1
    r3, e3 = _post("/v1/responses",
                   {"model": OPENAI_MODEL, "input": "hello", "max_output_tokens": 10},
                   "/v1/responses:turn1")
    if not e3 and r3 is not None:
        results.append(("/v1/responses", 1, r3.headers.get("x-audit-trace-id", "")))
        # responses turn 2
        r4, e4 = _post("/v1/responses",
                       {"model": OPENAI_MODEL, "input": "what is 1+1?", "max_output_tokens": 10},
                       "/v1/responses:turn2")
        if not e4 and r4 is not None:
            results.append(("/v1/responses", 2, r4.headers.get("x-audit-trace-id", "")))

    # chat stream
    r5, e5 = gateway_stream("/v1/chat/completions",
                            {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "hello"}],
                             "max_tokens": 10, "stream": True},
                            "/v1/chat/completions:stream")
    if not e5 and r5 is not None:
        results.append(("/v1/chat/completions", 0, r5.headers.get("x-audit-trace-id", "")))

    # responses stream
    r6, e6 = gateway_stream("/v1/responses",
                            {"model": OPENAI_MODEL, "input": "hello", "max_output_tokens": 10, "stream": True},
                            "/v1/responses:stream")
    if not e6 and r6 is not None:
        results.append(("/v1/responses", 0, r6.headers.get("x-audit-trace-id", "")))

    trace_ids = [tid for _, _, tid in results if tid]
    wait_for_traces(trace_ids)

    fingerprints = set()
    with psycopg.connect(helpers.PG_DSN) as conn:
        for endpoint, turn, trace_id in results:
            if not trace_id:
                continue
            ctx = f"{endpoint}:turn{turn}"
            fp = assert_trace_fields(conn, trace_id, ctx, PROTOCOL_FAMILY[endpoint], model=OPENAI_MODEL)
            if fp:
                fingerprints.add(fp)
            assert_evidence_objects(conn, trace_id, ctx)
        for fp in fingerprints:
            assert_identity_cache(conn, fp)

    assert_no_errors()
```

- [ ] **Step 2: 语法校验**

```bash
python -m py_compile e2e/test_gateway_openai.py && echo "compile OK"
```

- [ ] **Step 3: 容器内单跑**

```bash
docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e uv run pytest test_gateway_openai.py -q
```
Expected: 1 passed。

- [ ] **Step 4: Commit**

```bash
git add e2e/test_gateway_openai.py
git commit -m "refactor(e2e): convert test_gateway_openai to pytest, drop duplicated helpers"
```

---

## Task 7: test_gateway_claude.py pytest 化

**Files:**
- Rewrite: `e2e/test_gateway_claude.py`

**背景:** 单轮 + 多轮 + stream,trace + 证据 + identity cache。模型 `helpers.CLAUDE_MODEL`。

- [ ] **Step 1: 重写 `e2e/test_gateway_claude.py`**

```python
"""E2E: gateway captures Claude /v1/messages requests."""

from __future__ import annotations

import psycopg

import helpers
from helpers import (
    CLAUDE_MODEL,
    assert_evidence_objects,
    assert_identity_cache,
    assert_no_errors,
    assert_trace_fields,
    gateway_post,
    gateway_stream,
    preflight,
    wait_for_traces,
)

ENDPOINT = "/v1/messages"
PROTOCOL_FAMILY = "claude_messages"


def test_gateway_claude_capture():
    preflight(
        ENDPOINT,
        {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=CLAUDE_MODEL,
    )

    results = []  # (turn, trace_id)

    # single turn
    r1, e1 = gateway_post(ENDPOINT,
                          {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "hello"}], "max_tokens": 10},
                          "/v1/messages:turn1")
    if not e1 and r1 is not None:
        results.append((1, r1.headers.get("x-audit-trace-id", "")))

    # multi-turn
    reply = ""
    r2, e2 = gateway_post(ENDPOINT,
                          {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "hello"}], "max_tokens": 10},
                          "/v1/messages:multi1")
    if not e2 and r2 is not None:
        tid2 = r2.headers.get("x-audit-trace-id", "")
        results.append((1, tid2))
        for block in r2.json().get("content", []):
            if block.get("type") == "text":
                reply = block.get("text", "")
                break
        r3, e3 = gateway_post(ENDPOINT,
                              {"model": CLAUDE_MODEL,
                               "messages": [{"role": "user", "content": "hello"},
                                            {"role": "assistant", "content": reply},
                                            {"role": "user", "content": "what is 1+1?"}],
                               "max_tokens": 10},
                              "/v1/messages:multi2")
        if not e3 and r3 is not None:
            results.append((2, r3.headers.get("x-audit-trace-id", "")))

    # stream
    r4, e4 = gateway_stream(ENDPOINT,
                            {"model": CLAUDE_MODEL, "messages": [{"role": "user", "content": "hello"}],
                             "max_tokens": 10, "stream": True},
                            "/v1/messages:stream")
    if not e4 and r4 is not None:
        results.append((0, r4.headers.get("x-audit-trace-id", "")))

    trace_ids = [tid for _, tid in results if tid]
    wait_for_traces(trace_ids)

    fingerprints = set()
    with psycopg.connect(helpers.PG_DSN) as conn:
        for turn, trace_id in results:
            if not trace_id:
                continue
            ctx = f"{ENDPOINT}:turn{turn}"
            fp = assert_trace_fields(conn, trace_id, ctx, PROTOCOL_FAMILY, model=CLAUDE_MODEL, require_usage=False)
            if fp:
                fingerprints.add(fp)
            assert_evidence_objects(conn, trace_id, ctx)
        for fp in fingerprints:
            assert_identity_cache(conn, fp)

    assert_no_errors()
```

- [ ] **Step 2: 语法校验**

```bash
python -m py_compile e2e/test_gateway_claude.py && echo "compile OK"
```

- [ ] **Step 3: 容器内单跑**

```bash
docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e uv run pytest test_gateway_claude.py -q
```
Expected: 1 passed(前提:new-api 已配 `claude-sonnet-4-6`)。

- [ ] **Step 4: Commit**

```bash
git add e2e/test_gateway_claude.py
git commit -m "refactor(e2e): convert test_gateway_claude to pytest"
```

---

## Task 8: test_gateway_worker_pipeline.py — streams + 常驻 worker

**Files:**
- Rewrite: `e2e/test_gateway_worker_pipeline.py`

**背景:** 这是核心改动。旧版用 redis list(`rpush analysis_jobs`)+ `stop_background_workers`(pgrep 杀本机 worker)+ `run_worker --redis-once`,全是已废弃的 list 心智。新版:发请求 → 网关自动投 job 到 `analysis.core` stream → **常驻 analysis-worker 消费** → 写 `analysis_results`;用例只轮询 `analysis_results`。删除所有手动投递/跑 worker/杀进程逻辑。

- [ ] **Step 1: 重写 `e2e/test_gateway_worker_pipeline.py`**

```python
"""E2E: gateway capture -> resident worker analysis (Redis Streams).

The gateway publishes a trace_captured job to the analysis.core stream on
every captured request; the resident analysis-worker consumes it and writes
analysis_results. This test does NOT push jobs or run the worker itself — it
verifies the real deployed pipeline end-to-end by polling analysis_results.
"""

from __future__ import annotations

import psycopg

import helpers
from helpers import (
    OPENAI_MODEL,
    assert_no_errors,
    assert_trace_fields,
    bail,
    gateway_post,
    preflight,
    wait_for_rows,
    wait_for_traces,
)


def test_gateway_worker_pipeline():
    preflight(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=OPENAI_MODEL,
    )

    # 1. send request through gateway
    resp, err = gateway_post(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "hello"}], "max_tokens": 10},
        "worker-pipeline:request",
    )
    if err or resp is None:
        bail("gateway request failed")
    trace_id = resp.headers.get("x-audit-trace-id", "")
    if not trace_id:
        bail("No trace_id returned from gateway request")
    print(f"  Request: trace_id={trace_id}")

    # 2. wait for trace row
    wait_for_traces([trace_id])

    # 3. assert capture fields
    with psycopg.connect(helpers.PG_DSN) as conn:
        assert_trace_fields(conn, trace_id, "gateway-capture", "openai_chat")

    # 4. wait for resident worker to produce analysis_results (streams)
    n = wait_for_rows(
        "SELECT count(*) FROM analysis_results WHERE trace_id = %s",
        (trace_id,),
        expected=1,
        timeout=30,
        label="analysis_results",
    )
    helpers.check("analysis_results.exists", n > 0, f"no analysis_results for {trace_id}")

    # 5. inspect analysis_results rows
    with psycopg.connect(helpers.PG_DSN) as conn:
        rows = conn.execute(
            "SELECT analyzer_name, category, label FROM analysis_results WHERE trace_id = %s",
            (trace_id,),
        ).fetchall()
        for analyzer_name, category, label in rows:
            helpers.not_empty("analysis_results", "analyzer_name", analyzer_name)
            helpers.not_empty("analysis_results", "category", category)
            helpers.not_empty("analysis_results", "label", label)
            print(f"    analyzer={analyzer_name} category={category} label={label}")

    assert_no_errors()
```

- [ ] **Step 2: 语法校验**

```bash
python -m py_compile e2e/test_gateway_worker_pipeline.py && echo "compile OK"
```

- [ ] **Step 3: 容器内单跑**

```bash
docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e uv run pytest test_gateway_worker_pipeline.py -q
```
Expected: 1 passed。若 `analysis_results` 30s 内未出现,确认常驻 `analysis-worker` 容器健康(`docker logs new-api-gateway-analysis-worker-1`)且网关确实投递到 `analysis.core` stream。

- [ ] **Step 4: Commit**

```bash
git add e2e/test_gateway_worker_pipeline.py
git commit -m "refactor(e2e): worker pipeline uses Redis Streams + resident worker"
```

---

## Task 9: test_media_extraction.py — streams + 共享 evidence volume

**Files:**
- Rewrite: `e2e/test_media_extraction.py`

**背景:** 同 Task 8,改用常驻 worker(streams)。媒体断言:常驻 worker 提取二进制 → `raw_evidence_objects` 出现 `media_asset_*` → 证据 JSON 改写为 `audit-media:` 引用 → `traces.request_body_sha256` 更新。证据文件经共享 `/evidence` volume 读取(e2e 容器与网关/worker 挂同一 host 目录)。

- [ ] **Step 1: 重写 `e2e/test_media_extraction.py`**

```python
"""E2E: media base64 extraction via resident worker (Redis Streams).

Sends a chat completion with a base64 PNG; the resident worker extracts the
binary asset, rewrites the evidence JSON with audit-media: references, and
records a media_asset row. Backend-agnostic: reads the evidence file via the
shared /evidence volume regardless of filesystem/oss backend (oss backend
asserts only DB rows, skipping file checks).
"""

from __future__ import annotations

import base64
import os

import psycopg

import helpers
from helpers import (
    OPENAI_MODEL,
    assert_no_errors,
    assert_trace_fields,
    bail,
    gateway_post,
    preflight,
    read_request_raw_ref,
    wait_for_rows,
    wait_for_traces,
)

# Minimal valid 1x1 RGBA PNG (70 bytes)
SMALL_PNG = (
    b"\x89PNG\r\n\x1a\n"
    b"\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01"
    b"\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"
    b"\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f"
    b"\x00\x05\x00\x01\xff\x89\x99=\x1d"
    b"\x00\x00\x00\x00IEND\xaeB`\x82"
)
SMALL_PNG_B64 = base64.b64encode(SMALL_PNG).decode("ascii")
DATA_URL = f"data:image/png;base64,{SMALL_PNG_B64}"


def _check_evidence_file(request_ref: str) -> None:
    """Filesystem-only: verify the rewritten evidence + extracted binary on /evidence."""
    if helpers.EVIDENCE_STORAGE_BACKEND != "filesystem":
        print("  (skip file checks: backend is not filesystem)")
        return
    if not request_ref:
        helpers.check("evidence.request_raw_ref", False, "empty request_raw_ref")
        return
    ref = request_ref[len("file:///"):] if request_ref.startswith("file:///") else request_ref
    path = os.path.join(helpers.EVIDENCE_STORAGE_DIR, ref)
    if not os.path.exists(path):
        helpers.check("evidence.file_exists", False, f"not found: {path}")
        return
    body = open(path, "r", encoding="utf-8").read()
    helpers.check("evidence.contains_audit_media_ref",
                  "audit-media:media_asset_000001" in body, "audit-media ref missing")
    helpers.check("evidence.base64_removed", SMALL_PNG_B64 not in body, "base64 still present")
    asset_path = os.path.join(os.path.dirname(path), "media_asset_000001.bin")
    helpers.check("evidence.asset_file_exists", os.path.exists(asset_path), asset_path)
    if os.path.exists(asset_path):
        helpers.check("evidence.asset_content_matches",
                      open(asset_path, "rb").read() == SMALL_PNG, "binary mismatch")


def test_media_extraction():
    preflight(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL, "messages": [{"role": "user", "content": "ping"}], "max_tokens": 1},
        model_label=OPENAI_MODEL,
    )

    resp, err = gateway_post(
        "/v1/chat/completions",
        {"model": OPENAI_MODEL,
         "messages": [{"role": "user", "content": [
             {"type": "text", "text": "describe this image"},
             {"type": "image_url", "image_url": {"url": DATA_URL}},
         ]}],
         "max_tokens": 10},
        "media-extraction:request",
    )
    if err or resp is None:
        bail("gateway request failed")
    trace_id = resp.headers.get("x-audit-trace-id", "")
    if not trace_id:
        bail("No trace_id returned from gateway request")
    print(f"  Request: trace_id={trace_id}")

    wait_for_traces([trace_id])

    with psycopg.connect(helpers.PG_DSN) as conn:
        assert_trace_fields(conn, trace_id, "media-capture", "openai_chat")

    # wait for resident worker to extract media asset
    n = wait_for_rows(
        "SELECT count(*) FROM raw_evidence_objects "
        "WHERE trace_id = %s AND object_type LIKE 'media_asset_%%'",
        (trace_id,),
        expected=1,
        timeout=30,
        label="media_assets",
    )
    helpers.check("media_assets.exists", n > 0, f"no media_asset for {trace_id}")

    with psycopg.connect(helpers.PG_DSN) as conn:
        rows = conn.execute(
            "SELECT object_type, object_ref, content_type, size_bytes "
            "FROM raw_evidence_objects WHERE trace_id = %s AND object_type LIKE 'media_asset_%%'",
            (trace_id,),
        ).fetchall()
        for asset_type, asset_ref, content_type, size_bytes in rows:
            helpers.eq("media_assets", "object_type", asset_type, "media_asset_000001")
            helpers.not_empty("media_assets", "object_ref", asset_ref)
            helpers.eq("media_assets", "content_type", content_type, "image/png")
            helpers.gt("media_assets", "size_bytes", size_bytes, 0)
            print(f"    media_asset: type={asset_type} size={size_bytes}")

        # sha256 updated
        row = conn.execute("SELECT request_body_sha256 FROM traces WHERE trace_id = %s", (trace_id,)).fetchone()
        helpers.check("sha256.trace_exists", row is not None, "no trace row")
        if row:
            helpers.not_empty("sha256", "request_body_sha256", row[0])

    # evidence file rewritten (filesystem only)
    _check_evidence_file(read_request_raw_ref(trace_id))

    assert_no_errors()
```

- [ ] **Step 2: 语法校验**

```bash
python -m py_compile e2e/test_media_extraction.py && echo "compile OK"
```

- [ ] **Step 3: 容器内单跑**

```bash
docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e uv run pytest test_media_extraction.py -q
```
Expected: 1 passed。

- [ ] **Step 4: Commit**

```bash
git add e2e/test_media_extraction.py
git commit -m "refactor(e2e): media extraction uses Redis Streams + shared evidence volume"
```

---

## Task 10: 删除 OSS 专属变体

**Files:**
- Delete: `e2e/test_gateway_worker_pipeline_oss.py`
- Delete: `e2e/test_media_extraction_oss.py`

**背景:** backend-agnostic 用例(Task 8/9)取代它们;OSS 存储逻辑由 `workers/analysis_worker/tests/test_oss_evidence.py`、`test_oss_integration.py` 兜底。

- [ ] **Step 1: 删除**

```bash
git rm e2e/test_gateway_worker_pipeline_oss.py e2e/test_media_extraction_oss.py
```

- [ ] **Step 2: Commit**

```bash
git commit -m "refactor(e2e): drop oss-specific variants (backend-agnostic tests replace)"
```

---

## Task 11: 文档同步 + .env.example

**Files:**
- Modify: `.env.example`
- Modify: `CLAUDE.md`
- Modify: `README.md`(若有 e2e 章节)
- Modify: `AGENTS.md`(若有 e2e 章节)
- Modify: `ARCHITECTURE.md`(若有 e2e 章节)

- [ ] **Step 1: `.env.example` 增补 e2e 变量**

在 `.env.example` 末尾加入:

```
# --- E2E (docker-native, profile=e2e) ---
E2E_API_KEY=sk-G0YzOkt9WQAwp8S9DL9mLKlcFNEYRjdnA4x6PMrNRgZA05l8
E2E_OPENAI_MODEL=gpt-5.4
E2E_CLAUDE_MODEL=claude-sonnet-4-6
```

- [ ] **Step 2: 更新 `CLAUDE.md` 的 E2E 与 Testing 段**

把现有「E2E 总入口 `uv run e2e/run_all.py`」相关内容替换为 docker-native 描述:

- Commands 区的 e2e 条目改为:

```bash
# E2E（docker 部署后，profile=e2e on-demand 容器；要求 postgres/redis/new-api/网关/常驻 worker 已部署，且 new-api 配齐 E2E_OPENAI_MODEL 与 E2E_CLAUDE_MODEL）
docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e
```

- Testing Gotchas 段:把「worker 类 e2e 改用隔离 Redis DB」一条改为「worker 单元逻辑已回归 `workers/analysis_worker/tests/`(pytest);e2e 只保留端到端,网关→worker 全链路依赖常驻 worker 消费 `analysis.core` stream,e2e 不再手动投 list/跑 `--redis-once`」。

- 删除任何残留的「go run 网关」「run_all.py」描述。

- [ ] **Step 3: 检查并同步 README/AGENTS/ARCHITECTURE**

```bash
grep -rln "run_all\|go run.*audit-gateway\|e2e/run_all" README.md AGENTS.md ARCHITECTURE.md 2>/dev/null
```
对命中的文件,把 e2e 触发方式更新为 `docker compose --profile e2e run --rm e2e`,移除 go run 描述。

- [ ] **Step 4: Commit**

```bash
git add .env.example CLAUDE.md README.md AGENTS.md ARCHITECTURE.md
git commit -m "docs: sync e2e to docker-native profile=e2e flow"
```

---

## Task 12: 集成验证(全套)

**Files:** 无(仅验证)

**前置:new-api 已配齐 `gpt-5.4` 与 `claude-sonnet-4-6`。**

- [ ] **Step 1: 跑全套 e2e**

```bash
docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e
```
Expected: 5 passed(test_smoke、test_gateway_openai、test_gateway_claude、test_gateway_worker_pipeline、test_media_extraction),prerequisites 全绿,无 `go run`、无端口发布、无孤儿进程。

- [ ] **Step 2: 跑 worker pytest 确认无回归**

```bash
cd workers/analysis_worker && uv run pytest -q && cd ../..
```
Expected: 全 PASS。

- [ ] **Step 3: 跑 `make test` 确认 Go + admin UI 无回归**

```bash
make test
```
Expected: Go 测试 + `node --test` 全 PASS。

- [ ] **Step 4: 确认清理(无残留进程/容器)**

```bash
docker ps --filter "name=e2e" --format '{{.Names}}'
lsof -nP -iTCP:8080 -sTCP:LISTEN | grep -v com.docke || echo "8080 only held by docker proxy"
```
Expected: e2e 容器已 `--rm` 清理(无输出);8080 仅由 docker proxy 持有(docker 网关容器)。

---

## Self-Review(计划作者已执行)

1. **Spec 覆盖**:决策 1(compose service)→ Task 1;决策 2(worker 移出)→ Task 4;决策 3(backend-agnostic)→ Task 8/9/10;决策 4(模型可配)→ Task 3 helpers env + conftest prerequisites 模型校验。5 个端到端用例→ Task 5-9。文档→ Task 11。验证→ Task 12。✓
2. **占位符扫描**:无 TBD/TODO;所有代码块均为可直接使用的最终内容。✓
3. **类型/命名一致**:`assert_no_errors`、`wait_for_rows`、`read_request_raw_ref` 在 helpers(Task 3)定义,在 Task 5-9 使用,签名一致;`OPENAI_MODEL`/`CLAUDE_MODEL`/`PG_DSN`/`errors` 均从 helpers 导出。✓
4. **风险落点**:LLM judge 缺 API key → 全链路依赖常驻 worker,Task 8/3 Step 3 验证时检查 worker 日志;Claude 模型缺失 → conftest prerequisites fail-fast。✓
