# API Key → new-api 用户身份解析 重设计

日期：2026-05-02
状态：待审批

## 背景

当前网关通过 API key 的 `name` 字段作为 username/工号进行审计身份标识。但 new-api 中 token 的 `name` 是创建时由用户自定义的标签，与 key 本身无任何关联，也不代表实际用户身份。

new-api 的数据模型中，`tokens.user_id` 外键指向 `users.id`，`users.username` 是唯一且稳定的主登录标识符。正确做法是通过 API key 查询 new-api 数据库，找到 key 所属用户的 username。

## 决策

1. **查询方式**：新增指向 new-api 数据库的只读 PostgreSQL 连接池，通过 JOIN 查询解析身份
2. **用户字段**：使用 `users.username`（唯一、稳定）作为审计身份标识
3. **工号概念**：移除 `employee.Normalize()`、`EMPLOYEE_NO_PATTERN` 等工号相关逻辑
4. **方案选择**：方案 A（单次 JOIN 查询），配合 Redis 缓存

## 设计

### 1. 配置变更

**新增** `internal/config/config.go`：

```go
NewAPIPostgresDSN string // env: NEW_API_POSTGRES_DSN（必填）
```

**移除**：
- `EmployeeNoPattern` 字段
- `EMPLOYEE_NO_PATTERN` 环境变量

网关将同时连接两个 PostgreSQL：
- `POSTGRES_DSN`：网关自身数据库（traces、evidence、admin）
- `NEW_API_POSTGRES_DSN`：new-api 数据库（只读，查 tokens + users）

### 2. 新增 new-api 数据库查询组件

新增 `internal/identity/newapi_lookup.go`：

```go
type NewAPILookup struct {
    db *sql.DB
}

func NewNewAPILookup(dsn string) (*NewAPILookup, error)
func (l *NewAPILookup) LookupUsername(ctx context.Context, canonicalKey string) (string, error)
func (l *NewAPILookup) Close() error
```

`LookupUsername` 执行：

```sql
SELECT u.username
FROM tokens t
JOIN users u ON t.user_id = u.id
WHERE t.key = $1
  AND t.deleted_at IS NULL
  AND u.status = 1
LIMIT 1
```

条件说明：
- `t.deleted_at IS NULL`：排除已软删除的 token
- `u.status = 1`：只查启用状态的用户

### 3. 身份解析流程

**新流程**：

```
请求 → 提取 API key → canonicalize → 计算 HMAC 指纹
  → 查 Redis 缓存（fingerprint → username）
  → 缓存命中 → 返回 username
  → 缓存未命中 → NewAPILookup.LookupUsername(canonicalKey)
    → 成功 → 写入 Redis 缓存（TTL 15min）→ 返回 username
    → 未找到 → 写入 Redis 空值缓存（TTL 5min）→ 降级为指纹标识
    → 数据库错误 → 降级为指纹标识
```

### 4. 缓存策略

- **缓存键**：`identity:{fingerprint_value}` → `username`
- **成功缓存 TTL**：15 分钟（复用现有 TTL）
- **空值缓存 TTL**：5 分钟（防止缓存穿透）
- **缓存层**：Redis（一级）→ PostgreSQL（二级，复用现有 `token_identity_cache` 表）
- **缓存内容变更**：从缓存 `{token_name, employee_no}` 改为缓存 `{username}`

### 5. 降级策略

new-api 数据库不可用时：
- 用 HMAC 指纹值（`tkfp_xxxxxxxxxxxx`）作为降级标识符
- 不影响请求代理（网关仍然正常转发）
- 审计日志中标记为降级状态（`ResolutionStatusResolveFailed`）
- 与现有降级机制保持一致

### 6. 移除清单

| 文件/目录 | 操作 | 说明 |
|-----------|------|------|
| `internal/employee/` | 删除 | 整个包，包含 Normalize() 和工号验证逻辑 |
| `internal/config/config.go` | 修改 | 移除 EmployeeNoPattern 和相关 env |
| `internal/identity/resolver.go` | 修改 | 移除 employee.Normalize() 调用和工号验证 |

### 7. 新增/修改清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/config/config.go` | 修改 | 新增 NewAPIPostgresDSN |
| `internal/identity/newapi_lookup.go` | 新增 | new-api 数据库查询组件 |
| `internal/identity/resolver.go` | 修改 | 集成 NewAPILookup 替换旧逻辑 |
| `internal/identity/postgres_token_lookup.go` | 修改 | 替换为 new-api JOIN 查询 |
| `internal/identity/redis_cache.go` | 修改 | 缓存内容从 {token_name, employee_no} 改为 {username} |
| `cmd/audit-gateway/main.go` | 修改 | 初始化 NewAPILookup，注入 Resolver |
| `internal/gateway/proxy.go` | 检查 | 确认 resolveIdentity 调用不受影响 |

### 8. Key 格式兼容性

new-api token key 格式：
- 生成方式：`GenerateRandomCharsKey(48)`，字符集 `0-9a-zA-Z`，无连字符
- 存储：数据库中**不带** `sk-` 前缀
- 使用：客户端以 `sk-<key>` 格式传递

网关 canonicalize 处理后（去 `Bearer `、去 `sk-`、取首段），结果正好匹配数据库存储的 key。无需修改 canonicalize 逻辑。

### 9. 测试计划

- 单元测试：`NewAPILookup.LookupUsername` 的正常/空结果/错误场景
- 单元测试：Resolver 的缓存命中/未命中/降级场景
- 集成测试：完整的 key → canonicalize → lookup → cache 流程
- 确保现有 `make test` 和 `make smoke` 通过
