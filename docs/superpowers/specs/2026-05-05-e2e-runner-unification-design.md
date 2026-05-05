# E2E 测试统一入口设计

## 背景

当前 `e2e/` 目录下有 7 个 Python 测试和 1 个 shell 脚本，运行方式不统一：
- 大部分 Python 测试直接 `uv run` 运行，假设服务已就绪
- OSS 测试必须通过 `e2e_oss_pipeline.sh` 运行，该脚本负责重启 gateway 为 OSS 模式
- 从文件名无法判断哪些测试能单独运行、哪些需要编排

## 目标

提供一个 `e2e/run_all.py` 统一入口，一条命令跑完全部 e2e 测试，包含完整的 gateway 启停和模式切换。

## 测试清单与依赖

| # | 测试文件 | 描述 | 需要 |
|---|---------|------|------|
| 1 | `test_worker_anomaly_coverage.py` | Worker 异常/覆盖告警检测 | postgres, redis |
| 2 | `test_worker_work_relevance.py` | Worker 工作相关性分类 | postgres, redis |
| 3 | `test_gateway_openai.py` | OpenAI 协议网关代理与 trace 持久化 | postgres, redis, gateway, new-api |
| 4 | `test_gateway_worker_pipeline.py` | 网关采集 → Worker 分析全链路 | postgres, redis, gateway, new-api |
| 5 | `test_media_extraction.py` | 媒体资源提取（filesystem 后端） | postgres, redis, gateway, new-api |
| 6 | `test_gateway_worker_pipeline_oss.py` | OSS 后端全链路验证 | postgres, redis, gateway, new-api, oss |
| 7 | `test_media_extraction_oss.py` | OSS 后端媒体资源提取 | postgres, redis, gateway, new-api, oss |

## Gateway 启动策略

Gateway 需要启动两次以覆盖两种存储后端：

1. **阶段一**：worker 测试（#1-#2），无需 gateway
2. **阶段二**：启动 gateway（filesystem 模式），跑测试 #3-#5
3. **阶段三**：重启 gateway（OSS 模式），跑测试 #6-#7
4. **清理**：停掉 gateway

## 架构

### `e2e/run_all.py` — 统一入口

核心流程：

```python
def main():
    print_banner()
    check_prerequisites()           # 前置检查
    run_no_gateway_tests()          # worker 测试
    gw = GatewayManager()
    gw.start("filesystem")          # 第一次启动
    run_filesystem_tests()          # gateway 默认模式测试
    gw.restart("oss")               # 切换 OSS
    run_oss_tests()                 # OSS 专属测试
    gw.stop()                       # 清理
    print_summary()                 # 汇总结果
```

用 `atexit` 注册 gateway 清理，确保即使中断也能停掉进程。

### `e2e/helpers.py` — 新增 `GatewayManager`

```python
class GatewayManager:
    def start(self, mode: Literal["filesystem", "oss"]) -> None:
        # pgrep 查找并 kill 现有 gateway 进程
        # 根据 mode 设置环境变量：
        #   filesystem: 默认，不加 EVIDENCE_STORAGE_BACKEND
        #   oss: EVIDENCE_STORAGE_BACKEND=oss + OSS_* 四个变量
        # subprocess.Popen("go run ./cmd/audit-gateway")
        # 轮询 /healthz 最多 30 秒

    def stop(self) -> None:
        # SIGTERM → 等待 2s → SIGKILL

    def restart(self, mode: Literal["filesystem", "oss"]) -> None:
        self.stop()
        self.start(mode)
```

Gateway 日志写到 `/tmp/e2e-gateway.log`，测试失败时打印最后 20 行辅助排查。

### 测试注册表

每条测试注册为 `TestSpec(name, description, needs)`，运行时按序执行并打印进度：

```
=== E2E Test Suite (7 tests) ===

[1/7] test_worker_anomaly_coverage.py — Worker 异常/覆盖告警检测
      ✓ PASSED (3.2s)
[2/7] test_worker_work_relevance.py — Worker 工作相关性分类
      ✓ PASSED (2.1s)
--- Starting gateway (filesystem mode) ---
[3/7] test_gateway_openai.py — OpenAI 协议网关代理与 trace 持久化
      ✓ PASSED (5.4s)
...
--- Restarting gateway (OSS mode) ---
[6/7] test_gateway_worker_pipeline_oss.py — OSS 后端全链路验证
      ✓ PASSED (4.1s)
[7/7] test_media_extraction_oss.py — OSS 后端媒体资源提取
      ✗ FAILED (8.3s)

=== Results ===
  PASSED: 6   FAILED: 1
  FAILED: test_media_extraction_oss.py
```

## 前置检查

启动时逐项检查，任一失败直接报错退出：

| 检查项 | 方式 |
|--------|------|
| postgres 可连接 | `psycopg.connect(PG_DSN)` |
| redis 可连接 | `redis.Redis.from_url(REDIS_URL).ping()` |
| new-api 可达 | `requests.get(UPSTREAM_URL/healthz)` |
| OSS 凭据完整 | 4 个环境变量非空 |

## 错误处理

- 单个测试失败**不中断**整体流程，记录失败继续下一个
- 前置检查失败直接退出
- Gateway 启动超时（30s）直接退出并打印日志
- 最终汇总打印所有通过/失败的测试

## 文件变更

| 操作 | 文件 |
|------|------|
| 新增 | `e2e/run_all.py` |
| 修改 | `e2e/helpers.py` — 新增 `GatewayManager` 类 |
| 删除 | `e2e/e2e_oss_pipeline.sh` |
| 更新 | `CLAUDE.md` — 常用命令更新为 `uv run e2e/run_all.py` |
