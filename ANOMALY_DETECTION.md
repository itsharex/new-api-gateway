# 异常识别说明

## 1. 文档目标

本文档用于说明当前项目中的异常识别规则，帮助使用者、管理员和技术同学理解：

- 哪些 trace 会进入异常菜单
- 哪些情况只会进入 `needs_review`，不会进入异常菜单
- 当前异常判断使用的 token 口径是什么
- 管理端为什么会同时看到 `reason` 和 `display_reason`

本文档描述的是**当前 worker 新写入/收敛后的规则**。历史数据中如果已经存在旧类型 anomaly，仍可能在数据库中保留，但不代表当前规则还会继续生成这些类型。

---

## 2. 快速理解

### 2.1 当前异常菜单只关注什么

当前异常菜单只保留 4 类明确、可信、可解释的异常信号：

1. `non_work_use`
2. `high_trace_tokens`
3. `long_output_anomaly`
4. `off_hours_high_usage`

设计原则是：

- 只保留明确非工作内容
- 只保留明确高消耗或明确异常输出长度
- 不再把噪音较大的身份链路、聚合波动、unknown 高成本等信号直接塞进异常菜单

### 2.2 什么不会进入异常菜单

有一些信号仍然保留分析意义，但**不会**进入异常菜单：

- `work_nonwork_conflict`
  - 表示内容里同时存在工作和非工作信号
  - 当前只保留在 `analysis_results` / `needs_review`
  - 不再落库为 anomaly

- `unknown`
  - 当前统一走 `record_only`
  - 不再生成 `unknown_high_cost`

### 2.3 为什么有些 trace 有 review，但没有异常

这是当前设计的有意区分：

- **异常菜单**：用于展示已经足够明确、可直接归类的异常
- **`needs_review`**：用于展示需要人工判断的 trace

因此：

- 明确非工作内容会直接进入异常菜单
- 工作/非工作冲突只会打上 `needs_review`
- unknown 只保留分析结果，不直接报警

### 2.4 为什么界面上看到中文原因，但底层还是英文原因

管理端会同时返回两个字段：

- `reason`
  - worker 原始原因
  - 主要用于保留底层分析信息

- `display_reason`
  - 管理端展示层生成的说明文案
  - 对当前支持的 anomaly type 生成中文展示文案
  - 如果是未知或历史 anomaly type，则回退为原始 `reason`

因此，`display_reason` 适合界面展示，`reason` 适合保留原始分析语义。

---

## 3. 异常识别总流程

当前异常识别大致分为 4 个阶段：

1. 网关采集 trace、用量和证据
2. analysis worker 做协议归一化与工作相关性识别
3. worker 根据规则决定是否生成 anomaly
4. admin API 在返回 anomaly 时补充中文 `display_reason`

从职责上看：

- `work_relevance.py`
  - 负责判断内容更像工作、非工作、冲突还是 unknown
  - 输出 `decision`、`recommended_action`、`needs_review`
  - 不直接决定最终展示文案

- `rules.py`
  - 负责把明确可落库的规则转换成 anomaly
  - 当前只会新写入/收敛为 4 类 anomaly

- `internal/admin/anomaly_reason.go`
  - 负责把 anomaly 转换成管理端展示用的中文说明

---

## 4. 核心术语

### 4.1 `effective_tokens`

当前成本类异常统一使用：

```text
effective_tokens = max(prompt_tokens - cached_tokens, 0) + completion_tokens
```

对应到 worker 字段：

```text
effective_tokens = max(usage_prompt_tokens - usage_cached_tokens, 0) + usage_completion_tokens
```

这样做的目的，是避免缓存命中的 prompt token 被按高成本重复放大。

### 4.2 baseline

当前规则里仍会使用两类 baseline：

- `trace_effective_tokens_p95`
  - 某个 token 指纹历史 trace 的 `effective_tokens` P95

- `completion_tokens_p95`
  - 某个 token 指纹历史 completion token 的 P95

它们用于判断某次请求是否显著偏离该 token 的历史水平。

### 4.3 `needs_review`

`needs_review` 表示：

- 当前系统认为该 trace 有人工复核价值
- 但还不足以直接进入异常菜单

当前最典型的场景是：

- `work_nonwork_conflict`

---

## 5. 当前会进入异常菜单的 4 类异常

## 5.1 `non_work_use`

### 含义

表示该 trace 被明确判定为非工作用途。

### 触发条件

当工作相关性分类结果满足：

```text
recommended_action = alert_non_work
```

时，worker 会生成：

```text
anomaly_type = non_work_use
```

### 特点

- 这是当前唯一保留的非工作类 anomaly
- 之前更细的分类已被收敛，不再区分：
  - `non_work_high_risk`
  - `non_work_job_search`
  - `non_work_side_business`
  - `non_work_personal_use`

### 管理端展示

`display_reason` 默认展示为：

```text
检测到明确非工作用途内容。
```

---

## 5.2 `high_trace_tokens`

### 含义

表示单次 trace 的**有效 token 消耗**明显过高。

### 使用指标

```text
effective_tokens
```

### 触发阈值

如果没有 baseline：

```text
effective_tokens >= 40,000
```

如果存在 baseline：

```text
effective_tokens >= max(trace_effective_tokens_p95 * 1.5, 40,000)
```

### 解释

也就是说，这条规则会同时考虑：

- 一个固定底线：40,000
- 该 token 历史自己的高位区间：`trace_effective_tokens_p95`

只有当本次请求超过“历史 P95 的 1.5 倍”和“40,000 底线”中的较大者时，才会命中。

### 管理端展示

示例：

```text
本次请求有效 token 消耗 48,200，超过阈值 40,000。
```

---

## 5.3 `long_output_anomaly`

### 含义

表示本次输出长度异常偏长。

### 使用指标

```text
usage_completion_tokens
```

注意，这条规则只看**输出 token**，不看总 token，也不看缓存。

### 触发阈值

如果没有 baseline：

```text
completion_tokens >= 16,000
```

如果存在 baseline：

```text
completion_tokens >= max(completion_tokens_p95 * 1.5, 16,000)
```

### 解释

这条规则更关注“模型这次吐出的内容是否异常长”，而不是整次请求的总成本。

因此一个 trace 可能：

- 没有命中 `high_trace_tokens`
- 但仍命中 `long_output_anomaly`

### 管理端展示

示例：

```text
本次输出 token 为 18,300，超过阈值 16,000。
```

---

## 5.4 `off_hours_high_usage`

### 含义

表示夜间时段发生了高有效 token 消耗请求。

### 使用指标

```text
effective_tokens
```

### 夜间时间窗

使用本地时间判断：

```text
23:00 <= local_time < 24:00
或
00:00 <= local_time < 07:00
```

也就是：

```text
23:00 - 07:00
```

### 触发阈值

```text
effective_tokens >= 20,000
```

### 解释

这条规则不再依赖：

- `hourly_tokens_baseline`
- client 维度基线

也就是说，它是一个明确、简单、固定的夜间高消耗判断规则。

### 管理端展示

示例：

```text
夜间时段（23:00-07:00）本次有效 token 消耗 22,500，超过阈值 20,000。
```

---

## 6. 当前不会再新写入异常菜单的类型

以下类型已从当前 worker 新写入规则中移除，不会再继续新生成：

- `missing_username`
- `identity_unresolved_success`
- `invalid_username`
- `retry_storm_trace`
- `raw_only_large_response`
- `unknown_high_cost`
- `daily_token_limit_exceeded`
- `short_window_token_spike`
- `expensive_model_overuse`
- `possible_token_leak`

这些类型之所以移除，主要是因为它们要么噪音偏大，要么解释成本高，要么与当前的异常菜单目标不一致。

需要注意：

- **历史数据**中仍可能保留这些 anomaly type
- 但这不代表当前 worker 还会继续生成它们

---

## 7. `work_nonwork_conflict` 的当前处理方式

### 7.1 它是什么

`work_nonwork_conflict` 表示：

- 内容里同时有工作相关信号
- 也有明显的非工作信号

典型场景是：

- 在项目上下文里写简历
- 在工作仓库语境里讨论求职内容

### 7.2 当前为什么不进入异常菜单

因为这类 trace 本质上需要人工判断：

- 不能简单算作明确非工作
- 也不适合直接按正常工作内容放过

所以当前策略是：

- 保留 `needs_review`
- 保留分析结果
- 不再写 anomaly

### 7.3 对用户的影响

如果一个 trace：

- 在 trace 列表上显示 `needs_review`
- 但异常菜单里没有对应记录

这通常不是漏报，而是 `work_nonwork_conflict` 这类 review-only 信号。

---

## 8. unknown 的当前处理方式

### 8.1 当前语义

当系统无法明确判断是工作还是非工作时，当前走：

```text
recommended_action = record_only
```

### 8.2 与旧规则的区别

旧逻辑里，unknown + 高成本可能进入：

```text
unknown_high_cost
```

当前这条规则已经取消。

### 8.3 现在会发生什么

unknown 场景下：

- 会保留分析结果
- 不会进入异常菜单
- 不会因为高成本自动生成异常

---

## 9. 管理端字段说明

## 9.1 `reason`

原始字段，来自 worker 的异常原因文本。

特点：

- 保留原始分析语义
- 可能是英文
- 对历史 anomaly 也适用

## 9.2 `display_reason`

展示字段，由 admin 端按 anomaly type 生成。

特点：

- 对当前支持的 anomaly type 生成中文说明
- 用于前端列表和详情页直接展示
- 对未知或历史 anomaly type 回退为原始 `reason`

因此：

- 如果是当前 4 类 anomaly，通常能看到标准中文说明
- 如果是历史 anomaly 或未知类型，`display_reason` 不保证一定是中文

---

## 10. 典型示例

## 10.1 明确非工作内容

输入特征：

- 内容明确是简历、求职、个人副业、非工作用途

结果：

- 进入异常菜单
- anomaly type 为 `non_work_use`

## 10.2 工作与非工作混合

输入特征：

- 在项目上下文里写求职材料
- 同时出现 repo/work context 和明显非工作意图

结果：

- trace 可能进入 `needs_review`
- 不进入异常菜单

## 10.3 总 token 很高，但缓存命中也很高

输入特征：

- `usage_total_tokens` 很大
- 但其中大量 prompt token 来自 cache

结果：

- 不一定触发 `high_trace_tokens`
- 因为判断看的是 `effective_tokens`，不是 `usage_total_tokens`

## 10.4 输出特别长，但总成本未必最高

输入特征：

- completion token 很高

结果：

- 可能触发 `long_output_anomaly`
- 即使没有触发 `high_trace_tokens`

## 10.5 夜间高消耗请求

输入特征：

- 本地时间处于 23:00-07:00
- `effective_tokens >= 20,000`

结果：

- 触发 `off_hours_high_usage`

---

## 11. 当前规则一览表

| 类型 | 是否进入异常菜单 | 判断依据 | 当前状态 |
|------|------------------|----------|----------|
| `non_work_use` | 是 | `recommended_action=alert_non_work` | 保留 |
| `high_trace_tokens` | 是 | `effective_tokens` 超阈值 | 保留 |
| `long_output_anomaly` | 是 | `completion_tokens` 超阈值 | 保留 |
| `off_hours_high_usage` | 是 | 夜间 + 高 `effective_tokens` | 保留 |
| `work_nonwork_conflict` | 否 | 冲突内容 | 仅 review-only |
| `unknown_high_cost` | 否 | 已取消 | 不再新写入 |
| 身份链路类异常 | 否 | 已取消 | 不再新写入 |
| 聚合波动类异常 | 否 | 已取消 | 不再新写入 |
| token leak / model overuse 类 | 否 | 已取消 | 不再新写入 |

---

## 12. 结论

当前异常识别规则的核心思路是：

- 异常菜单只保留少数明确、可信、可解释的信号
- 需要人工判断的内容进入 `needs_review`，不直接变成 anomaly
- 成本类判断统一使用 `effective_tokens`
- 管理端提供中文 `display_reason`，但仍保留原始 `reason`

如果后续异常菜单再次调整，建议同步更新本文档、`README.md` 与 `ARCHITECTURE.md`，避免用户理解与实际规则脱节。
