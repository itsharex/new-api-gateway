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
        "test_smoke.py",
        "冒烟测试：三种协议端点各一请求",
        needs_gateway=True,
    ),
    TestSpec(
        "test_gateway_openai.py",
        "OpenAI 协议网关代理与 trace 持久化",
        needs_gateway=True,
    ),
    TestSpec(
        "test_gateway_claude.py",
        "Claude 协议网关代理与 trace 持久化",
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
    try:
        result = subprocess.run(
            ["uv", "run", spec.file],
            cwd=str(Path(__file__).parent),
            timeout=300,
        )
    except subprocess.TimeoutExpired:
        elapsed = time.time() - start
        print(f"      ✗ TIMEOUT ({elapsed:.1f}s)")
        return False
    elapsed = time.time() - start

    passed = result.returncode == 0
    status = "✓ PASSED" if passed else "✗ FAILED"
    print(f"      {status} ({elapsed:.1f}s)")

    # Rate-limit cooldown: upstream allows 15 req/min
    if spec.needs_gateway:
        print("      (cooldown 8s)")
        time.sleep(8)

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
