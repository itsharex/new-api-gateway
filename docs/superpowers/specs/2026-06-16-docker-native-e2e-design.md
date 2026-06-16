# Docker-Native E2E 测试重构设计

- 日期：2026-06-16
- 状态：待实施
- 相关：`2026-05-03-e2e-test-expansion-design.md`、`2026-05-05-e2e-runner-unification-design.md`、`2026-05-05-oss-e2e-test-design.md`

## 背景

当前 e2e（`e2e/run_all.py`）采用「宿主机 `go run` 网关 + 直连 docker 依赖」模式。该模式在已全面容器化部署的架构下已过时，且实测脆弱：

- `go run` 编译出的网关子进程在 `GatewayManager.stop()` 杀主进程时变成孤儿，持续占用 8080，导致 docker 网关容器无法重启。
- compose 的 postgres/redis **故意不发布宿主端口**（内部网络设计，仅以 service 名供网关/worker 容器访问），宿主机进程连不上 `localhost:5432/6379`。
- 常驻 worker（analysis-worker / analysis-enrichment-worker / analysis-batch）在共享 redis 上抢走 worker 类测试投递的任务。
- new-api 启用模型与 e2e 期望模型不匹配（实测 new-api 仅 `gpt-5.4`，e2e 请求 `gpt-5.2`/`claude-sonnet-4-6`/`gpt-4.1` → 503 `model_not_found`）。

实测结果：0/9 通过。基础设施层（prerequisites、go run 网关 lifecycle、DB/redis 连接）正常，全部失败为上述架构/配置问题。

## 目标

- e2e 完全基于 docker 部署后运行，复用部署的网关与常驻 worker，不再 `go run`、不发布任何端口、不产生孤儿进程。
- 测试用例适配容器化架构，移除与 pytest 重叠的部分，收敛为纯端到端验收。
- 单命令触发，作为部署后的验收手段。

## 非目标

- 不取代 worker 逻辑的单元测试（`workers/analysis_worker/tests/` pytest）。
- 不在 e2e 内切换 filesystem/oss backend——backend 由部署决定，e2e 适配当前部署。
- 不引入 CI 流水线编排（本设计只定义可单命令触发的测试套件）。

## 决策摘要

| # | 决策点 | 选择 |
|---|---|---|
| 1 | 运行形态 | e2e 作为 compose 的 `profile=e2e` on-demand 服务 |
| 2 | worker 类用例 | 移出 e2e，回归 pytest（逻辑已被 `test_rules`/`test_pipeline`/`test_work_relevance` 覆盖） |
| 3 | OSS 双模式 | 适配部署 backend，不内置切换；OSS 存储逻辑由 pytest 兜底 |
| 4 | 模型对齐 | 模型可配（env）+ prerequisites 校验；OpenAI=`gpt-5.4`，Claude=`claude-sonnet-4-6` |

## 架构：e2e 作为 compose on-demand 服务

在 `deploy/docker-compose.yml` 新增：

```yaml
  # ── On-demand services ─────────────────────────────
  e2e:
    profiles:
      - e2e
    image: ghcr.io/astral-sh/uv:python3.11-bookworm      # 复用 worker 同款基础镜像
    working_dir: /workspace
    depends_on:
      audit-gateway: { condition: service_healthy }      # 复用部署的网关，不 go run
      postgres:      { condition: service_healthy }
      redis:         { condition: service_healthy }
    extra_hosts:
      - "host.docker.internal:host-gateway"               # 访问宿主 new-api:3000 / new-api pg:5433
    environment:
      AUDIT_GATEWAY_URL: http://audit-gateway:8080        # service 名直连，无需端口发布
      POSTGRES_DSN: postgres://audit:audit@postgres:5432/audit_gateway?sslmode=disable
      REDIS_URL: redis://redis:6379/0
      NEW_API_BASE_URL: http://host.docker.internal:3000
      NEW_API_POSTGRES_DSN: ${NEW_API_POSTGRES_DSN}
      E2E_OPENAI_MODEL: ${E2E_OPENAI_MODEL:-gpt-5.4}
      E2E_CLAUDE_MODEL: ${E2E_CLAUDE_MODEL:-claude-sonnet-4-6}
      EVIDENCE_STORAGE_BACKEND: ${EVIDENCE_STORAGE_BACKEND:-filesystem}
      OSS_ENDPOINT: ${OSS_ENDPOINT:-}
      OSS_BUCKET: ${OSS_BUCKET:-}
      OSS_ACCESS_KEY_ID: ${OSS_ACCESS_KEY_ID:-}
      OSS_ACCESS_KEY_SECRET: ${OSS_ACCESS_KEY_SECRET:-}
    volumes:
      - ..:/workspace
    command: ["uv", "run", "pytest", "e2e/", "-q"]
```

要点：
- 复用部署的 **网关 + 常驻 worker**，不 `go run`、不发布端口、不留孤儿进程。
- e2e 容器加入 `new-api-gateway_default` 网络，以 service 名直连内部 postgres/redis，以 `host.docker.internal` 访问宿主上独立的 new-api 容器（与现有 audit-gateway 的访问方式一致）。
- 触发命令：

  ```bash
  docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e
  ```

- 测试改 **pytest 风格**，依赖通过环境变量注入。

## 测试用例梳理

| 现有（9 个） | 处理 | 去向 |
|---|---|---|
| `test_worker_anomaly_coverage.py` | 移除 | pytest 已覆盖（`test_rules` / `test_pipeline` / `test_isolation_forest`） |
| `test_worker_work_relevance.py` | 移除 | pytest 已覆盖（`test_work_relevance`） |
| `test_smoke.py` | 保留 → backend-agnostic | e2e |
| `test_gateway_openai.py` | 保留 → 模型可配 | e2e |
| `test_gateway_claude.py` | 保留 → 模型可配 | e2e |
| `test_gateway_worker_pipeline.py` | 保留（网关 → **常驻 worker** 真实全链路） | e2e |
| `test_media_extraction.py` | 保留 → backend-agnostic | e2e |
| `test_gateway_worker_pipeline_oss.py` | 合并（不再单独 oss 专属） | — |
| `test_media_extraction_oss.py` | 合并 | — |

结果：e2e 收敛为 **5 个 backend-agnostic 端到端用例 + prerequisites 校验**。

- 全链路用例（`test_gateway_worker_pipeline`）**不隔离**，复用常驻 worker 真实消费——这正是它应验证的；被移除的两个 worker 单元用例才需要隔离队列，现已回归 pytest。
- 证据 `object_ref` 断言从部署 backend 探测前缀（`file:///` 或 `oss://`），不内置切换。

## 数据流与隔离

**端到端请求流**（pytest 用例内）：

```
pytest → POST audit-gateway:8080 (API_KEY, E2E_*_MODEL)
      → 网关转发 host.docker.internal:3000 (new-api) → 上游 LLM
      → 网关写 traces(postgres) + 证据(部署 backend) + 投 job 到 redis analysis.core
      → 常驻 analysis-worker 消费 → 写 anomaly/work_relevance 结果(postgres)
      → pytest 按 trace_id 轮询 postgres，断言 trace 字段 + worker 结果
```

**连接**（service 名 / host-gateway，容器内）：
- `audit-gateway:8080`（网关）、`postgres:5432`（断言 traces / evidence / analysis 结果）、`redis:6379`（全链路可观察 stream，一般不直接操作）
- `host.docker.internal:3000`（new-api，仅 prerequisites）、`host.docker.internal:5433`（new-api postgres，身份解析校验）

**隔离策略**（开发环境）：
- **不独立建库、不 flush redis**——常驻 worker 在用，独立库/flush 会让全链路验证失效。
- 当前为开发环境，e2e 写入共享 `traces` 表的数据污染可接受。
- 断言仍以 **trace_id 精确定位**（保留 `wait_for_traces`），保证各用例之间互不干扰。
- **确定性**：常驻 worker 异步消费，全链路用例用轮询 + 超时（约 30s）等待结果，不 sleep 固定；网关 `depends_on healthy` 后才发请求。

## prerequisites 与触发

prerequisites（`e2e/conftest.py` session 级 fixture）：
- `audit-gateway/healthz` 返回 200。
- postgres 可连，且 `schema_migrations` 行数符合预期。
- redis `ping` 通过。
- new-api `host.docker.internal:3000` 可达。
- **模型可用性校验**：确认 `E2E_OPENAI_MODEL`（`gpt-5.4`）与 `E2E_CLAUDE_MODEL`（`claude-sonnet-4-6`）在 new-api 存在（向 new-api 发探测请求或查 `abilities`）；缺则 fail-fast，明确报错并指明缺失模型。

`.env.example` 增补 `E2E_OPENAI_MODEL` / `E2E_CLAUDE_MODEL` 默认值。

## 迁移步骤

1. **compose**：在 `deploy/docker-compose.yml` 新增 `e2e` service（profile=e2e，见上）。
2. **e2e/ 重构为 pytest**：保留 `helpers.py` 的 DB 断言工具（`check` / `eq` / `wait_for_traces` / `assert_trace_fields` / `assert_evidence_objects` 等），改为 pytest 友好（fixture / 模块级函数）；**删除 `GatewayManager`**；删除 `run_all.py`（pytest 直接跑 `e2e/`）。新增 `e2e/conftest.py` 承载 prerequisites。
3. **删除** `test_worker_anomaly_coverage.py`、`test_worker_work_relevance.py`（逻辑回归 pytest）。
4. **合并 oss 变体**：`test_gateway_worker_pipeline.py` + `test_media_extraction.py` 改 backend-agnostic（从 `EVIDENCE_STORAGE_BACKEND` / 实际 `object_ref` 探测前缀）；删除 `_oss` 两个文件。
5. **模型可配**：`helpers.py` 的模型从 env 读（`E2E_OPENAI_MODEL` / `E2E_CLAUDE_MODEL`）；测试用 API key 同样从 env 读（`E2E_API_KEY`，默认沿用现有测试 token，对应 new-api 的 dave.zhao）。
6. **pytest 侧核对**：确认 `test_rules` / `test_pipeline` / `test_work_relevance` 覆盖了原 worker e2e 的断言点（异常覆盖计数、work_relevance 标签）；有缺口则补。
7. **文档同步**：`CLAUDE.md` / `README.md` / `AGENTS.md` / `ARCHITECTURE.md` 更新 e2e 触发命令、放弃 go run、worker e2e 回归 pytest 的说明。

## 风险

- **常驻 worker 的 LLM judge**：`.env.local` 设了 `LLM_JUDGE_BASE_URL`/`MODEL` 但无 `LLM_JUDGE_API_KEY`。work_relevance 使用 4 层规则、anomaly 使用统计基线，规则路径不依赖 judge；需在实现时验证 judge 配置缺失不会让 worker 写出影响断言的错误结果或卡住任务。
- **Claude 协议依赖 claude 系列模型**：`claude-sonnet-4-6` 必须在 new-api 配齐（用户负责补充），否则 Claude 协议用例在 prerequisites 阶段 fail-fast。
- **常驻 worker 处理时延**：全链路断言依赖轮询超时窗口；若部署负载高，需适当放宽超时。

## 验收标准

- `docker compose -f deploy/docker-compose.yml --profile e2e --env-file .env.local run --rm e2e` 一条命令跑完整套 e2e，5 个端到端用例 + prerequisites 全绿（前提：new-api 已配齐 `gpt-5.4` 与 `claude-sonnet-4-6`）。
- 过程中不出现 `go run`、不发布宿主端口、无孤儿进程。
- `make test` / 仓库既有 pytest 不回归。
- `CLAUDE.md` / `README.md` / `AGENTS.md` / `ARCHITECTURE.md` 的 e2e 相关说明与新方式一致。
