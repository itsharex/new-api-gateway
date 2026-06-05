# Analysis Runtime Sampling Copy Design

## Background

The analysis runtime page currently shows a right-aligned label like `83 点` above both:

- `Core 队列趋势`
- `Core 时延趋势`

This number is technically correct because it reflects the number of history samples returned for the chart, but the wording is unclear for product users.

`点` reads like a charting term. Users can easily misread it as:

- queue depth
- latency score
- request volume
- some internal rating

The current copy explains the rendering shape of the chart instead of the business meaning of the number.

## Problem Statement

The UI needs to express this number as a user-facing summary of sampling activity within the selected time range, not as a visualization detail.

Users should understand three things immediately:

- the number refers to sampling count
- the count belongs to the current time range
- the count is not request volume, queue depth, or latency

## Goals

- Replace the ambiguous `X 点` wording with product language
- Make the selected time range explicit in the summary copy
- Keep the chart header compact and easy to scan
- Provide a lightweight explanation for users who want to understand why the count changes
- Keep queue and latency charts semantically aligned

## Non-Goals

- Do not change runtime sampling behavior
- Do not change backend API payloads
- Do not expose sampling interval as always-visible UI copy
- Do not redesign the chart layout beyond the header copy and tooltip affordance
- Do not change snapshot cards or consumer table wording in this design

## Current Semantics

The displayed number is derived from the length of the runtime history array returned for the selected stage and time range.

The current ranges are:

- `15m`
- `1h`
- `24h`

Each history entry is one runtime sample. The sample count can vary because it depends on actual persisted samples within the selected window.

## Proposed Direction

Treat the header number as a time-range summary:

- from chart terminology: `83 点`
- to user language: `近 1 小时采样 83 次`

This reframes the same data in a way that answers the natural user question: "In this period, how many times did the system record runtime samples?"

## Copy Rules

### Range mapping

Map internal range values to human-readable copy:

- `15m` -> `近 15 分钟`
- `1h` -> `近 1 小时`
- `24h` -> `近 24 小时`

### Primary summary copy

When history contains one or more samples, render:

- `近 15 分钟采样 N 次`
- `近 1 小时采样 N 次`
- `近 24 小时采样 N 次`

`N` should continue to use the existing number formatting behavior.

### Empty-state summary copy

When history is empty, do not show `采样 0 次`.

Render:

- `近 15 分钟暂无采样`
- `近 1 小时暂无采样`
- `近 24 小时暂无采样`

This reads more naturally and avoids implying that a hard failure necessarily occurred.

## Tooltip Strategy

Do not place sampling frequency explanation directly in the always-visible header.

Instead, attach a tooltip or equivalent hover/focus affordance to the summary copy. The tooltip should explain:

- sampling count means runtime samples recorded in the current time range
- it does not mean request count, queue depth, or latency value
- counts may vary when workers restart, pause, or sampling interval changes

Suggested tooltip copy:

`采样次数表示当前时间范围内记录到的运行采样数量，不代表请求数、队列长度或时延值。采样通常按固定间隔生成；如果 worker 重启、暂停或采样间隔调整，次数可能不是固定值。`

## Information Architecture

Both runtime trend charts should use the same summary-copy rules because they are driven by the same history dataset.

This consistency matters because:

- users will compare the two cards side by side
- differing labels would imply differing data sources
- the meaning of the count should stay stable across queue and latency views

## Implementation Scope

In scope:

- replace `X 点` copy in analysis runtime chart headers
- add a small copy helper that maps range to user-facing text
- add empty-state summary wording
- add tooltip text for sampling-count explanation
- apply the same rule to queue and latency cards

Out of scope:

- API changes
- database changes
- worker sampling changes
- changing chart datasets, axes, or colors

## Error Handling

If the selected range is missing or unknown in the frontend state, the implementation should fall back to the existing default runtime range behavior and produce matching summary copy for that effective range.

If tooltip rendering is unavailable for any reason, the primary summary copy must still remain understandable on its own.

## Testing Strategy

Frontend coverage should verify:

- `15m`, `1h`, and `24h` map to the expected Chinese summary text
- non-empty history renders `近 X 采样 N 次`
- empty history renders `近 X 暂无采样`
- queue and latency cards stay consistent for the same history payload
- tooltip text is attached to both chart summaries

Manual verification should confirm:

- copy remains readable on desktop and narrow layouts
- no header overflow or awkward wrapping appears in the analysis runtime page
- hover or focus access to the tooltip works for mouse and keyboard users
