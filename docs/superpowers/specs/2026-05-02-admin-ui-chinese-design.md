# Admin UI 中文本地化设计

## 目标

将 admin UI 的用户可见文本从英文改为中文，技术术语保留英文，遵循国内同类产品的常见翻译习惯。

## 范围

仅涉及 `internal/adminui/app.js` 和 `internal/adminui/index.html`，不涉及后端 API 响应或数据模型。

## 翻译原则

- **保留英文**：Token, Model, Route, Trace, Fingerprint, Severity, API Key, Status, ID, Audit Log 等在国内运维/开发工具中通常不翻译的术语
- **翻译为中文**：所有面向用户的描述性文本、按钮、标签、提示信息

## 翻译对照表

### 导航标签

| 英文 | 中文 |
|------|------|
| Overview | 概览 |
| Usage | 用量 |
| Traces | Trace |
| Employee Directory | 员工目录 |
| Anomalies | 异常 |
| Coverage | 覆盖 |
| API Key Lookup | API Key 查询 |
| Context Catalog | Context 目录 |
| Review Decisions | 审核记录 |
| System Settings | 系统设置 |
| Audit Logs | 审计日志 |

### 通用文本

| 英文 | 中文 |
|------|------|
| Audit Gateway Admin | 审计网关管理后台 |
| Sign in | 登录 |
| Logout | 退出登录 |
| Username | 用户名 |
| Password | 密码 |
| No rows found. | 暂无数据。 |
| Loading... | 正在加载... |
| Back | 返回 |
| Create | 创建 |
| Lookup Result | 查询结果 |
| Login failed. | 登录失败。 |
| Sign in to review gateway activity. | 登录以查看网关活动。 |

### 概览指标

| 英文 | 中文 |
|------|------|
| Requests 24h | 24h 请求数 |
| Tokens 24h | 24h Token 数 |
| Errors 24h | 24h 错误数 |
| Open Anomalies | 未处理异常 |
| Open Coverage | 未处理覆盖 |
| Raw Only 24h | 24h 仅原始数据 |

### 表头

| 英文 | 中文 |
|------|------|
| Employee | 员工 |
| Name | 名称 |
| Department | 部门 |
| Bucket | 时间段 |
| Cost | 费用 |
| Setting | 设置项 |
| Value | 值 |
| Time | 时间 |
| Actor | 操作人 |
| Action | 操作 |
| Target Type | 目标类型 |
| Target | 目标 |
| Note | 备注 |
| Reviewer | 审核人 |
| Decision | 决定 |
| Observed | 观测值 |
| Reason | 原因 |
| Count | 数量 |

### Trace 详情

| 英文 | 中文 |
|------|------|
| Trace Detail | Trace 详情 |
| Normalized Messages | 归一化消息 |
| Analysis | 分析结果 |
| Raw Evidence | 原始证据 |
| Index | 序号 |
| Direction | 方向 |
| Role | 角色 |
| Modality | 模态 |
| Type | 类型 |
| Content | 内容 |
| Analyzer | 分析器 |
| Category | 分类 |
| Label | 标签 |
| Score | 分数 |
| Confidence | 置信度 |

### Context 创建表单

| 英文 | 中文 |
|------|------|
| Create Context | 创建 Context |
| Type | 类型 |
| Owner | 负责人 |
| Usage Level | 使用级别 |
| Keywords | 关键词 |
| Description | 描述 |

### Lookup 结果

| 英文 | 中文 |
|------|------|
| Fingerprint | Fingerprint |
| Open Anomalies | 未处理异常 |

### Settings

| 英文 | 中文 |
|------|------|
| Employee Pattern | 员工匹配规则 |
| Metrics Enabled | 指标已启用 |
| API Key Lookup Limit | API Key 查询限额 |
| Raw Evidence Limit | 原始证据访问限额 |

## 方案

直接替换字符串，不引入 i18n 框架。同时将 `index.html` 的 `lang` 属性从 `en` 改为 `zh-CN`。
