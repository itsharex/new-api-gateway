# OSS Evidence Storage Backend Design

Date: 2026-05-05

## 概述

为证据存储增加阿里云 OSS 后端支持。Go 和 Python 两端通过必填的 `EVIDENCE_STORAGE_BACKEND` 环境变量切换后端（`filesystem` 或 `oss`）。接口定义不变，新增 OSS 实现类和工厂函数。

## 决策记录

| 决策 | 选择 | 理由 |
|------|------|------|
| object_ref 格式 | 统一 scheme 前缀（`file:///` / `oss://bucket/`） | 格式自描述，与 storage_backend 列一致 |
| 旧数据迁移 | 一次性迁移脚本加 `file:///` 前缀，代码不兼容无前缀格式 | 避免长期维护双格式解析逻辑 |
| OSS SDK | 阿里云官方 SDK（Go: aliyun-oss-go-sdk, Python: oss2） | 最稳定，文档全 |
| Go 接口 | `evidence.Store` 接口不变 | 调用方无感知，实现替换 |
| 实现方式 | 独立 OSSStore + 工厂函数 | 职责单一，不需要混合后端路由 |
| OSS 失败策略 | 尽力写入，不阻断用户请求 | OSS 涉及网络，失败概率高于磁盘 |
| 配置方式 | 全部从环境变量读取，不新增 CLI 参数 | Go/Python 配置方式统一 |

## 第 1 节：object_ref 格式与 scheme 规范

### 格式定义

| 后端 | object_ref 格式 | 示例 |
|------|----------------|------|
| filesystem | `file:///<relative-path>` | `file:///raw/2026/05/05/trace_abc/request_body.bin` |
| oss | `oss://<bucket>/<object-key>` | `oss://my-evidence/raw/2026/05/05/trace_abc/request_body.bin` |

### 解析规则

- `file:///` 后面是相对于 `EVIDENCE_STORAGE_DIR` 的路径（三个斜杠，第三个是路径起始）
- `oss://<bucket>/` 后面是 OSS object key
- bucket 名称从 ref 中提取，不硬编码

### 写入规则

- `FilesystemStore.Put()` 返回 `file:///<relative-path>` 格式的 ref
- `OSSStore.Put()` 返回 `oss://<bucket>/<key>` 格式的 ref
- `storage_backend` 列同步更新为 `"filesystem"` 或 `"oss"`

### 迁移

- 一次性迁移 SQL 给所有现有记录加 `file:///` 前缀
- 迁移后代码不兼容无前缀格式

## 第 2 节：配置与环境变量

### 必填环境变量

| 变量名 | 说明 | 示例 |
|--------|------|------|
| `EVIDENCE_STORAGE_BACKEND` | 存储后端类型，`filesystem` 或 `oss` | `oss` |

### filesystem 后端额外必填

| 变量名 | 说明 |
|--------|------|
| `EVIDENCE_STORAGE_DIR` | 文件存储根目录 |

### oss 后端额外必填

| 变量名 | 说明 |
|--------|------|
| `OSS_ENDPOINT` | OSS endpoint，如 `oss-cn-hangzhou.aliyuncs.com` |
| `OSS_BUCKET` | bucket 名称 |
| `OSS_ACCESS_KEY_ID` | 阿里云 AccessKey ID |
| `OSS_ACCESS_KEY_SECRET` | 阿里云 AccessKey Secret |

### 两端统一规则

- 所有配置从环境变量读取，不新增 CLI 参数
- Python 端移除 `--evidence-root` CLI 参数，改从 `EVIDENCE_STORAGE_DIR` 环境变量读取
- 启动时校验：`EVIDENCE_STORAGE_BACKEND` 必填且合法，然后根据值校验对应环境变量组

### Go 端加载逻辑（`internal/config/config.go`）

- `Config` 新增 `EvidenceStorageBackend`、`OSSEndpoint`、`OSSBucket`、`OSSAccessKeyID`、`OSSAccessKeySecret` 字段
- `EVIDENCE_STORAGE_BACKEND` 必填，值必须为 `filesystem` 或 `oss`
- 根据 backend 值校验对应环境变量组

### Python 端加载逻辑（`workers/analysis_worker/main.py`）

- 从 `EVIDENCE_STORAGE_BACKEND` 环境变量读取，不新增 CLI 参数
- 启动时根据 backend 值校验对应环境变量组

## 第 3 节：Go 端实现

### 新增文件 `internal/evidence/oss_store.go`

`OSSStore` struct 实现 `Store` 接口，持有 OSS 客户端和 bucket 名称。

**Put：**
- 上传到 OSS，object key 格式 `raw/<YYYY>/<MM>/<DD>/<traceID>/<objectType>.bin`
- SHA256 在内存中同步计算
- 返回 `oss://<bucket>/<key>` 格式的 ref，`StorageBackend` 为 `"oss"`

**Get：**
- 解析 `oss://<bucket>/` 前缀，提取 object key
- 调用 OSS `Get` API 返回 `io.ReadCloser`

### 修改 `internal/evidence/store.go`

- 新增工厂函数，根据 backend 类型返回 `FilesystemStore` 或 `OSSStore`
- `FilesystemStore.Put` 返回 `file:///<relative-path>` 格式 ref
- `FilesystemStore.Get` 解析 `file:///` 前缀提取相对路径

### 修改 `internal/config/config.go`

- 新增配置字段和校验逻辑

### 修改 `cmd/audit-gateway/main.go`

- 三处 `evidence.NewFilesystemStore(...)` 改为工厂函数调用

### 依赖

- 新增 `github.com/aliyun/aliyun-oss-go-sdk/oss`

## 第 4 节：Python 端实现

### 新增文件 `workers/analysis_worker/oss_evidence.py`

`OSSEvidenceStore` 实现 `EvidenceStore` Protocol，持有 oss2 Bucket 实例。

- `read_text`：解析 `oss://<bucket>/` 前缀，提取 key，下载并解码 UTF-8
- `read_bytes`：同上，返回原始 bytes
- `write_text`：上传 UTF-8 编码内容，返回带前缀的 ref
- `write_bytes`：上传二进制内容，返回带前缀的 ref

### 修改 `workers/analysis_worker/evidence.py`

- `EvidenceStore` Protocol 的 `write_text`/`write_bytes` 返回类型从 `None` 改为 `str`（返回带 scheme 前缀的 object_ref）
- `FilesystemEvidenceStore` ref 格式改为 `file:///<relative-path>`
- 读取时解析 `file:///` 前缀提取相对路径

### 修改 `workers/analysis_worker/main.py`

- 移除 `--evidence-root` CLI 参数
- 工厂函数根据 `EVIDENCE_STORAGE_BACKEND` 创建对应 store
- 三处实例化点统一调用工厂函数

### 修改 `workers/analysis_worker/media_extraction.py`

- `storage_backend` 从环境变量获取，不再硬编码

### 修改 `workers/analysis_worker/repository.py`

- `save_media_assets` 的 `storage_backend` 参数从调用方传入

### 依赖

- 新增 `oss2` 到 `pyproject.toml`

## 第 5 节：数据库迁移

### 新增迁移文件 `migrations/0013_object_ref_scheme_prefix.sql`

```sql
UPDATE raw_evidence_objects
SET object_ref = 'file:///' || object_ref
WHERE storage_backend = 'filesystem'
  AND object_ref NOT LIKE 'file:///%';
```

幂等设计：`NOT LIKE 'file:///%'` 确保重复执行不会重复加前缀。

## 第 6 节：错误处理、可观测性与测试

### 错误处理核心策略：尽力写入，不阻断用户请求

**Go 端网关 proxy handler：**
- `evidenceStore.Put()` 失败时：记录 error 级别日志（含 trace_id、object_ref、错误详情），递增失败指标
- 用户请求正常返回，不感知证据存储失败
- 修改当前 proxy handler 中 `Put` 错误处理逻辑，从返回 500 改为记录并继续

**Python 端 worker：**
- OSS 读写失败时记录错误日志，job 标记为部分失败
- 不影响其他处理步骤

### 可观测性

| 通道 | 内容 |
|------|------|
| 日志 | error 级别，含 trace_id、object_ref、操作类型、错误详情 |
| Prometheus 指标 | `evidence_store_ops_total{backend,operation,status}` 计数器 |
| 告警 | 基于指标阈值触发（如 5 分钟内失败超过 N 次） |

### 测试策略

| 层面 | Go | Python |
|------|-----|--------|
| 单元测试 | `OSSStore` 用 mock，测试 Put/Get 返回正确 ref 格式 | `OSSEvidenceStore` 用 mock |
| 集成测试 | 真实 OSS，`// +build integration` | 真实 OSS，`@pytest.mark.integration` |
| 失败不阻断测试 | mock Put 返回错误，断言 proxy 仍返回 200 | mock read 失败，断言 job 不完全中断 |
| 现有测试 | `FilesystemStore` 更新 ref 断言为 `file:///` 格式 | 同理 |
| 迁移脚本 | 测试 SQL 幂等性 | — |

## 第 7 节：影响范围

### 新增文件

- `internal/evidence/oss_store.go`
- `internal/evidence/oss_store_test.go`
- `internal/evidence/oss_integration_test.go`
- `workers/analysis_worker/oss_evidence.py`
- `workers/analysis_worker/test_oss_evidence.py`
- `migrations/0013_object_ref_scheme_prefix.sql`

### 修改文件

- `internal/evidence/store.go` — FilesystemStore ref 格式 + 工厂函数
- `internal/evidence/store_test.go` — 更新 ref 断言
- `internal/config/config.go` — 新增 OSS 配置
- `cmd/audit-gateway/main.go` — 工厂函数替换
- `workers/analysis_worker/evidence.py` — FilesystemStore ref 格式
- `workers/analysis_worker/main.py` — 移除 CLI 参数 + 工厂函数
- `workers/analysis_worker/media_extraction.py` — storage_backend 参数化
- `workers/analysis_worker/repository.py` — storage_backend 参数化
- `go.mod` / `go.sum` — 新增 aliyun-oss-go-sdk
- `workers/analysis_worker/pyproject.toml` — 新增 oss2

### 不改动

- `Store` / `EvidenceStore` 接口定义
- `audit-media:` 引用机制
- Admin API 路由结构
