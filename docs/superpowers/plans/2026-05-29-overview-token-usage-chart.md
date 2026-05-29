# Overview Token Usage Chart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a recent 30-day token usage trend chart at the bottom of the admin overview page.

**Architecture:** Extend the existing overview repository method to attach daily token totals from `usage_aggregates`. Keep the UI dependency-free by rendering an SVG chart in `internal/adminui/app.js` with focused CSS in `internal/adminui/app.css`.

**Tech Stack:** Go `net/http` admin API, PostgreSQL via pgx, embedded vanilla JavaScript admin UI, SVG.

---

### Task 1: Backend Overview Data

**Files:**
- Modify: `internal/admin/models.go`
- Modify: `internal/admin/repository.go`
- Test: `internal/admin/repository_test.go`
- Test: `internal/admin/handlers_test.go`

- [ ] Add `TokenUsageDay` with JSON fields `date` and `total_tokens`.
- [ ] Add `TokenUsageDaily []TokenUsageDay` to `OverviewSummary`.
- [ ] Write a failing repository test that seeds rows for two days and expects exactly 30 output points with missing days set to 0.
- [ ] Implement `loadDailyTokenUsage` to query `usage_aggregates` for `bucket_size = 'day'`, group by `bucket_start::date`, and fill the 30-day window.
- [ ] Write a failing handler test that verifies `/admin/api/overview` includes `token_usage_daily`.
- [ ] Update the handler fake DB to return representative daily usage.
- [ ] Run `go test ./internal/admin/`.

### Task 2: Frontend Chart

**Files:**
- Modify: `internal/adminui/app.js`
- Modify: `internal/adminui/app.css`

- [ ] Update `renderOverview` to pass `overview.token_usage_daily` into a new chart renderer.
- [ ] Add `tokenUsageChart(points)` that renders an SVG line chart, empty state, axis labels, and max token label.
- [ ] Add CSS classes for `.overview-layout`, `.usage-chart`, `.chart-frame`, `.chart-empty`, `.chart-axis`, and `.chart-meta`.
- [ ] Verify text remains in Chinese and matches the existing admin UI style.

### Task 3: Verification and Docs

**Files:**
- Check: `README.md`
- Check: `ARCHITECTURE.md`
- Check: `CLAUDE.md`

- [ ] Run `go test ./internal/admin/`.
- [ ] Run `make test` if time permits.
- [ ] Confirm no README, ARCHITECTURE, or CLAUDE updates are required because the feature only extends an existing admin API/UI surface and does not change deployment, service dependencies, schema, or Go/Python contracts.

