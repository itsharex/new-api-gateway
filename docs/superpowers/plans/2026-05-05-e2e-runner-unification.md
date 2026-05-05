# E2E 测试统一入口实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 创建 `e2e/run_all.py` 统一入口，一条命令跑完全部 e2e 测试，包含 gateway 启停和 filesystem/OSS 模式切换。

**Architecture:** `run_all.py` 作为编排器，按三阶段顺序执行测试：worker 测试（无 gateway） → filesystem 模式 gateway 测试 → OSS 模式 gateway 测试。`GatewayManager` 类封装在 `helpers.py` 中，负责 gateway 进程的生命周期管理。

**Tech Stack:** Python 3.11+, subprocess, signal, psycopg, redis-py, requests

**Spec:** `docs/superpowers/specs/2026-05-05-e2e-runner-unification-design.md`

---

## File Structure

| 操作 | 文件 | 职责 |
|------|------|------|
| 修改 | `e2e/helpers.py` | 新增 `GatewayManager` 类 + `import signal` |
| 新增 | `e2e/run_all.py` | 统一入口：前置检查、测试编排、进度展示、汇总 |
| 删除 | `e2e/e2e_oss_pipeline.sh` | 不再需要 |
| 修改 | `CLAUDE.md` | 常用命令更新为 `uv run e2e/run_all.py` |

---

### Task 1: Add GatewayManager to helpers.py

**Files:**
- Modify: `e2e/helpers.py:5-16` (imports), append class after line 391

- [ ] **Step 1: Add `import signal` to helpers.py imports**

在 `e2e/helpers.py` 第 11 行 `import sys` 之后添加 `import signal`：

```python
import signal
import subprocess
import sys
```

- [ ] **Step 2: Add GatewayManager class to helpers.py**

在 `e2e/helpers.py` 文件末尾（`run_worker_once` 函数之后）添加：

```python


# ---------------------------------------------------------------------------
# Gateway process management
# ---------------------------------------------------------------------------

class GatewayManager:
    """Manages the audit-gateway process lifecycle for e2e tests."""

    def __init__(self) -> None:
        self._process: subprocess.Popen | None = None
        self._log_path = "/tmp/e2e-gateway.log"

    def start(self, mode: str) -> None:
        """Start gateway with the given storage backend mode (filesystem/oss)."""
        self._kill_existing()

        env = dict(os.environ)
        if mode == "oss":
            for var in (
                "OSS_ENDPOINT",
                "OSS_BUCKET",
                "OSS_ACCESS_KEY_ID",
                "OSS_ACCESS_KEY_SECRET",
            ):
                if not os.environ.get(var):
                    bail(f"{var} is required for OSS mode")
            env["EVIDENCE_STORAGE_BACKEND"] = "oss"

        log = open(self._log_path, "w")
        self._process = subprocess.Popen(
            ["go", "run", "./cmd/audit-gateway"],
            cwd=REPO_ROOT,
            env=env,
            stdout=log,
            stderr=log,
        )
        print(f"  Gateway starting (mode={mode}, pid={self._process.pid})")

        for attempt in range(1, 31):
            try:
                resp = requests.get(f"{GATEWAY_URL}/healthz", timeout=1)
                if resp.status_code == 200:
                    print(f"  Gateway ready ({mode} mode, attempt {attempt})")
                    return
            except requests.RequestException:
                pass
            time.sleep(1)

        self._print_log_tail()
        bail(f"Gateway did not become ready after 30s (mode={mode})")

    def stop(self) -> None:
        """Stop the gateway process."""
        if self._process is None:
            return
        self._process.terminate()
        try:
            self._process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            self._process.kill()
            self._process.wait()
        print("  Gateway stopped")
        self._process = None

    def restart(self, mode: str) -> None:
        """Stop then start with a new mode."""
        self.stop()
        self.start(mode)

    def _kill_existing(self) -> None:
        """Kill any running audit-gateway processes."""
        result = subprocess.run(
            ["pgrep", "-f", "audit-gateway"],
            capture_output=True,
            text=True,
        )
        pids = [p.strip() for p in result.stdout.splitlines() if p.strip()]
        if not pids:
            return
        print(f"  Killing existing gateway PIDs: {' '.join(pids)}")
        for pid in pids:
            try:
                os.kill(int(pid), signal.SIGTERM)
            except (ProcessLookupError, ValueError):
                pass
        time.sleep(2)
        for pid in pids:
            try:
                os.kill(int(pid), signal.SIGKILL)
            except (ProcessLookupError, ValueError):
                pass

    def _print_log_tail(self) -> None:
        """Print last 20 lines of gateway log for debugging."""
        try:
            with open(self._log_path) as f:
                lines = f.readlines()
            if lines:
                print("  Last gateway log lines:")
                for line in lines[-20:]:
                    print(f"    {line.rstrip()}")
        except FileNotFoundError:
            pass
```

- [ ] **Step 3: Verify import works**

Run: `cd e2e && uv run python -c "from helpers import GatewayManager; print('OK')"`
Expected: `OK`

- [ ] **Step 4: Commit**

```bash
git add e2e/helpers.py
git commit -m "feat(e2e): add GatewayManager to helpers for gateway lifecycle management"
```

---

### Task 2: Create run_all.py

**Files:**
- Create: `e2e/run_all.py`

- [ ] **Step 1: Write run_all.py**

创建 `e2e/run_all.py`：

```python
#!/usr/bin/env python3
"""E2E test suite runner.

Unified entry point for all end-to-end tests. Handles gateway
startup/shutdown, filesystem/OSS mode switching, and sequential
test execution with progress reporting.

Prerequisites:
  - postgres, redis running (docker compose up -d postgres redis)
  - new-api running
  - OSS credentials set (OSS_ENDPOINT, OSS_BUCKET, OSS_ACCESS_KEY_ID, OSS_ACCESS_KEY_SECRET)

Usage:
  uv run e2e/run_all.py
"""

from __future__ import annotations

import os
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path

import psycopg
import redis
import requests

from helpers import (
    GATEWAY_URL,
    PG_DSN,
    REDIS_URL,
    UPSTREAM_URL,
    GatewayManager,
)

# ---------------------------------------------------------------------------
# Test registry
# ---------------------------------------------------------------------------


@dataclass
class TestSpec:
    file: str
    description: str
    needs_gateway: bool = False
    needs_oss: bool = False


TESTS = [
    # Phase 1: Worker tests (no gateway needed)
    TestSpec("test_worker_anomaly_coverage.py", "Worker 异常/覆盖告警检测"),
    TestSpec("test_worker_work_relevance.py", "Worker 工作相关性分类"),
    # Phase 2: Gateway filesystem mode
    TestSpec(
        "test_gateway_openai.py",
        "OpenAI 协议网关代理与 trace 持久化",
        needs_gateway=True,
    ),
    TestSpec(
        "test_gateway_worker_pipeline.py",
        "网关采集 → Worker 分析全链路",
        needs_gateway=True,
    ),
    TestSpec(
        "test_media_extraction.py",
        "媒体资源提取（filesystem 后端）",
        needs_gateway=True,
    ),
    # Phase 3: Gateway OSS mode
    TestSpec(
        "test_gateway_worker_pipeline_oss.py",
        "OSS 后端全链路验证",
        needs_gateway=True,
        needs_oss=True,
    ),
    TestSpec(
        "test_media_extraction_oss.py",
        "OSS 后端媒体资源提取",
        needs_gateway=True,
        needs_oss=True,
    ),
]

# ---------------------------------------------------------------------------
# Prerequisites check
# ---------------------------------------------------------------------------


def check_prerequisites() -> None:
    """Verify all required services are accessible."""
    print("\n=== Prerequisites Check ===")

    failed: list[str] = []

    try:
        psycopg.connect(PG_DSN).close()
        print("  ✓ postgres")
    except Exception as exc:
        failed.append(f"postgres: {exc}")

    try:
        redis.Redis.from_url(REDIS_URL).ping()
        print("  ✓ redis")
    except Exception as exc:
        failed.append(f"redis: {exc}")

    try:
        requests.get(f"{UPSTREAM_URL}/", timeout=5)
        print("  ✓ new-api")
    except Exception as exc:
        failed.append(f"new-api ({UPSTREAM_URL}): {exc}")

    oss_vars = (
        "OSS_ENDPOINT",
        "OSS_BUCKET",
        "OSS_ACCESS_KEY_ID",
        "OSS_ACCESS_KEY_SECRET",
    )
    missing = [v for v in oss_vars if not os.environ.get(v)]
    if missing:
        failed.append(f"OSS credentials missing: {', '.join(missing)}")
    else:
        print("  ✓ OSS credentials")

    if failed:
        print("\nPrerequisites failed:")
        for msg in failed:
            print(f"  ✗ {msg}")
        sys.exit(1)


# ---------------------------------------------------------------------------
# Test runner
# ---------------------------------------------------------------------------


def run_test(spec: TestSpec, index: int, total: int) -> bool:
    """Run a single test file, return True if passed."""
    prefix = f"[{index}/{total}]"
    print(f"\n{prefix} {spec.file} — {spec.description}")

    start = time.time()
    result = subprocess.run(
        ["uv", "run", spec.file],
        cwd=str(Path(__file__).parent),
        timeout=300,
    )
    elapsed = time.time() - start

    passed = result.returncode == 0
    status = "✓ PASSED" if passed else "✗ FAILED"
    print(f"      {status} ({elapsed:.1f}s)")
    return passed


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    total = len(TESTS)
    print(f"=== E2E Test Suite ({total} tests) ===")

    check_prerequisites()

    results: list[tuple[TestSpec, bool]] = []
    gateway = GatewayManager()

    try:
        # Phase 1: Worker tests (no gateway)
        for i, spec in enumerate(TESTS, 1):
            if spec.needs_gateway:
                continue
            passed = run_test(spec, i, total)
            results.append((spec, passed))

        # Phase 2: Filesystem mode
        print("\n--- Starting gateway (filesystem mode) ---")
        gateway.start("filesystem")
        for i, spec in enumerate(TESTS, 1):
            if not spec.needs_gateway or spec.needs_oss:
                continue
            passed = run_test(spec, i, total)
            results.append((spec, passed))

        # Phase 3: OSS mode
        print("\n--- Restarting gateway (OSS mode) ---")
        gateway.restart("oss")
        for i, spec in enumerate(TESTS, 1):
            if not spec.needs_oss:
                continue
            passed = run_test(spec, i, total)
            results.append((spec, passed))

    finally:
        gateway.stop()

    # Summary
    passed_count = sum(1 for _, p in results if p)
    failed_count = sum(1 for _, p in results if not p)
    print(f"\n{'=' * 50}")
    print(f"PASSED: {passed_count}   FAILED: {failed_count}")
    if failed_count:
        for spec, p in results:
            if not p:
                print(f"  FAILED: {spec.file}")
        sys.exit(1)
    print("All tests passed.")


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Verify syntax**

Run: `cd e2e && uv run python -c "import ast; ast.parse(open('run_all.py').read()); print('syntax OK')"`
Expected: `syntax OK`

- [ ] **Step 3: Commit**

```bash
git add e2e/run_all.py
git commit -m "feat(e2e): add unified run_all.py test suite runner"
```

---

### Task 3: Delete e2e_oss_pipeline.sh and update docs

**Files:**
- Delete: `e2e/e2e_oss_pipeline.sh`
- Modify: `CLAUDE.md:32` (项目地图), `CLAUDE.md:59-68` (常用命令)

- [ ] **Step 1: Delete e2e_oss_pipeline.sh**

```bash
rm e2e/e2e_oss_pipeline.sh
```

- [ ] **Step 2: Update CLAUDE.md project map**

将 `CLAUDE.md` 第 32 行：
```
- `e2e/`：端到端测试（Python 测试用例 + OSS 编排脚本），依赖本地 postgres/redis。
```
改为：
```
- `e2e/`：端到端测试（`run_all.py` 统一入口），依赖本地 postgres/redis。
```

- [ ] **Step 3: Update CLAUDE.md commands section**

将 `CLAUDE.md` 第 59-68 行：
```bash
# e2e 测试（需要 postgres/redis 运行中）
cd e2e && uv run test_worker_anomaly_coverage.py
cd e2e && uv run test_worker_work_relevance.py
# 网关集成测试（还需要 new-api、audit-gateway 运行中）
cd e2e && uv run test_gateway_openai.py
cd e2e && uv run test_gateway_worker_pipeline.py
# OSS 端到端（需要 OSS 凭据环境变量）
cd e2e && ./e2e_oss_pipeline.sh
```
改为：
```bash
# e2e 测试（需要 postgres/redis/new-api 运行中 + OSS 凭据）
cd e2e && uv run run_all.py
```

- [ ] **Step 4: Commit**

```bash
git add -A e2e/e2e_oss_pipeline.sh CLAUDE.md
git commit -m "chore: remove e2e_oss_pipeline.sh, update CLAUDE.md for run_all.py"
```

---

### Task 4: End-to-end verification

**Files:** None (verification only)

- [ ] **Step 1: Verify run_all.py can be imported**

Run: `cd e2e && uv run python -c "from run_all import TESTS, check_prerequisites, GatewayManager; print(f'{len(TESTS)} tests registered')"`
Expected: `7 tests registered`

- [ ] **Step 2: Verify test file references are correct**

Run: `cd e2e && uv run python -c "from pathlib import Path; from run_all import TESTS; missing = [t.file for t in TESTS if not Path(t.file).exists()]; print('All found' if not missing else f'Missing: {missing}')"`
Expected: `All found`

- [ ] **Step 3: Verify deleted script is gone**

Run: `test -f e2e/e2e_oss_pipeline.sh && echo "STILL EXISTS" || echo "deleted OK"`
Expected: `deleted OK`
