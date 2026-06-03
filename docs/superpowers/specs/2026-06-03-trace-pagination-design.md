# Trace 页面分页设计

Date: 2026-06-03

## Goal

为管理后台 Trace 列表增加稳定的历史翻页能力，支持传统页码分页，并保证从列表进入 trace 详情后返回时仍停留在原页。

本设计优先服务“稳定翻历史”，不追求信息流式刷新体验，也不把分页状态写入 URL。

## Current State

当前管理后台 Trace 页面有这些特征：

- 前端位于 `internal/adminui/app.js`
- 后端列表接口为 `GET /admin/api/traces`
- 列表接口当前只返回 `{"traces":[...]}`，没有分页元信息
- 前端进入 Trace 视图时一次性请求 `/traces`
- 后端 `ListTraces` 通过 `LIMIT` 返回最多 100 条记录
- Trace 详情页和列表页之间没有独立的分页状态管理

这导致当前只能查看最近一批 trace，无法稳定浏览更早的历史记录。

## User-Confirmed Requirements

- 分页方式使用传统页码分页，不使用“加载更多”或下拉刷新
- 每页固定 50 条，不提供前端切换 page size 的控件
- 从列表进入 trace 详情后，再返回列表时要保留原页
- 分页状态只保留在当前前端会话内，不写入 URL
- 设计重点是稳定翻历史，而不是快速追最新数据

## Approaches Considered

### 1. Recommended: 偏移量分页 + 传统页码

后端使用 `COUNT(*)` 统计总数，再使用 `LIMIT 50 OFFSET ...` 查询当前页；前端渲染传统页码栏，并在本地 state 中保存当前页。

优点：

- 最符合管理后台“查历史”的使用习惯
- 支持首页、上一页、指定页、下一页、末页
- 前端返回原页的实现简单直观
- 只需在现有 `/admin/api/traces` 基础上扩展，不必新增接口

缺点：

- 深页码查询会随着 `OFFSET` 增大而变慢
- 新 trace 持续写入时，不同页之间仍可能出现轻微边界漂移

### 2. Keyset 分页伪装为页码

后端使用 cursor / anchor 查询，前端自己维护“第 N 页对应哪个 anchor”。

优点：

- 深分页性能更稳定

缺点：

- 实现明显更复杂
- 不适合“末页”和任意页跳转
- 与传统页码心智模型不一致

### 3. 前端缓存更多数据后本地分页

一次拉取几百条 trace，前端在本地切页。

优点：

- 后端改动最小

缺点：

- 不能提供真实总页数
- 数据量变大后仍会卡顿
- 不适合持续增长的审计列表

## Decision

采用方案 1：在现有 Trace 列表接口上增加偏移量分页和分页元信息，前端使用传统页码控件，并在本地 state 中保存当前页。

## API Design

继续使用现有接口：

`GET /admin/api/traces`

### Request Parameters

保留现有筛选参数：

- `trace_id`
- `username`
- `token_fingerprint`
- `route_pattern`
- `model`

新增分页参数：

- `page`
  - 可选
  - 默认值为 `1`
  - 非数字、空值或小于 `1` 时按 `1` 处理

不新增前端可配置的 `page_size` 参数。后端固定每页返回 50 条。

### Response Shape

从当前的：

```json
{
  "traces": []
}
```

扩展为：

```json
{
  "traces": [],
  "pagination": {
    "page": 1,
    "page_size": 50,
    "total_items": 0,
    "total_pages": 0,
    "has_prev": false,
    "has_next": false
  }
}
```

字段语义：

- `page`: 当前实际返回页码
- `page_size`: 固定为 `50`
- `total_items`: 当前筛选条件下的总记录数
- `total_pages`: 总页数；当 `total_items = 0` 时为 `0`
- `has_prev`: 是否存在上一页
- `has_next`: 是否存在下一页

## Backend Design

### Admin Models

在 `internal/admin/models.go` 中为 Trace 列表返回值增加分页结构，明确引入：

- `TracePagination`
- `TraceListResult`

`TraceFilter` 增加：

- `Page int`

`TraceFilter.Limit` 继续保留，并在本次实现中明确承担“每页条数”的角色；handler 固定传入 `50`。

### Handler

在 `internal/admin/handlers.go` 的 `listTraces()` 中：

1. 解析 `page`
2. 非法值回退到 `1`
3. 构造包含 `Page=page`、固定每页 `50` 的 filter
4. 返回 `traces + pagination` 的 JSON

错误处理保持现状：

- repository 查询失败时返回 `500 failed to list traces`

### Repository

在 `internal/admin/repository.go` 的 `ListTraces()` 中把当前“单条查询列表”改成“统计 + 当前页数据”两步：

1. 基于同一组筛选条件执行 `COUNT(*)`
2. 计算：
   - `pageSize = 50`
   - `totalPages = ceil(totalItems / pageSize)`
   - `page = clamp(page, 1, totalPages)`；当 `totalItems = 0` 时返回 `page = 1`
   - `offset = (page - 1) * pageSize`
3. 执行当前页列表查询

排序从：

```sql
ORDER BY t.created_at DESC
```

改为：

```sql
ORDER BY t.created_at DESC, t.trace_id DESC
```

这样可避免相同 `created_at` 时分页顺序不稳定，减少跨页重复或遗漏。

### Empty / Out-of-Range Behavior

- 没有数据时：
  - `traces = []`
  - `pagination.page = 1`
  - `pagination.total_items = 0`
  - `pagination.total_pages = 0`
  - `has_prev = false`
  - `has_next = false`

- 请求页码超过最后一页时：
  - 自动夹到最后一页返回
  - 不返回 404 或空白异常页

## Frontend Design

### State

在 `internal/adminui/app.js` 的 `state` 中新增 trace 专用分页状态，例如：

```js
traces: {
  page: 1,
  pageSize: 50
}
```

这部分状态只保留在当前前端会话内，不同步到 URL。

### Loading Flow

- 进入 Trace 视图时，请求 `state.traces.page`
- 点击分页控件时：
  - 更新 `state.traces.page`
  - 重新请求 `/admin/api/traces?page=N`
- 点击 trace 进入详情时：
  - 不清空 `state.traces.page`
- 在详情页点击“返回”时：
  - 回到 Trace 视图
  - 使用保留的页码重新加载列表

这样可满足“从第 3 页进入详情，再返回仍在第 3 页”的需求。

### Pagination UI

在 Trace 表格下方新增分页栏，包含：

- `首页`
- `上一页`
- 页码按钮
- `下一页`
- `末页`

页码呈现策略：

- 总页数较少时展示全部页码
- 总页数较多时展示“首尾 + 当前页附近页码 + 省略号”

例如第 `8 / 20` 页可显示为：

`1 ... 6 7 8 9 10 ... 20`

### Button States

- 第 1 页时禁用：
  - `首页`
  - `上一页`

- 最后一页时禁用：
  - `下一页`
  - `末页`

- 没有数据时：
  - 表格继续显示现有“暂无数据”
  - 分页按钮隐藏
  - 保留一行简短统计文案：`共 0 条`

### Why Not Browser-Side Page Cache

本次不引入“缓存已翻过页的数据”作为默认策略：

- 用户更在意稳定翻历史，不是翻页秒开
- 后端分页已经满足性能和复杂度的平衡
- 多页缓存会增加状态失效和返回逻辑复杂度

## Stability Considerations

偏移量分页仍有两个固有权衡：

1. 深页码会更慢
2. 当新 trace 持续插入时，页边界可能轻微漂移

对当前管理后台，这两个问题是可接受的，因为：

- 使用场景以人工审计为主
- 每页仅 50 条
- 用户通常关注前几页
- 传统页码带来的可定位性比信息流式交互更重要

## Testing Plan

### Repository Tests

补充或调整 `internal/admin/repository_test.go`，验证：

- 会执行总数查询和当前页数据查询
- 每页固定 50 条
- `OFFSET` 按页码正确计算
- 排序包含 `created_at DESC, trace_id DESC`
- 超出范围页码被夹到最后一页
- 空结果时 pagination 元信息正确

### Handler Tests

补充 `internal/admin/handlers_test.go`，验证：

- `page` 参数解析正确
- 非法 `page` 回退为 `1`
- 返回 JSON 包含 `pagination`
- 详情接口不受影响

### Manual Verification

由于当前 admin UI 是原生 `app.js`，没有现成的前端单测框架，本次保留手工验证：

1. 进入 Trace 页面，确认默认第 1 页
2. 翻到中间页与末页，确认按钮禁用状态正确
3. 从非第一页进入某条 trace 详情，再点击返回，确认停留在原页
4. 无数据筛选场景下，确认表格空态与分页栏表现正常
5. 快速连续翻页时，确认界面不会回跳到第 1 页

## Documentation Impact

本次不涉及：

- 数据库 schema
- Go 与 Python worker 契约
- 网关主请求链路

因此：

- `README.md` 本次不更新
- `ARCHITECTURE.md` 在涉及管理端 Trace 列表描述的章节补一句：Trace 列表支持分页浏览

实现完成后仍应按仓库约定检查这两个文档是否需要同步。

## Non-Goals

- 不实现下拉刷新或无限滚动
- 不把分页状态写入 URL
- 不提供 page size 切换控件
- 不为 Trace 列表新增复杂筛选器
- 不引入前端多页缓存
- 不改 Trace 详情接口

## Acceptance Criteria

- Trace 页面支持传统页码分页
- 每页固定展示 50 条 trace
- 点进 trace 详情再返回时，列表保留原页
- 页码状态仅保留在当前前端会话内，不写入 URL
- 后端返回 `traces` 和 `pagination` 元信息
- 排序具备稳定的分页边界
- 相关 Go 测试更新并通过
- 文档在必要时同步更新
