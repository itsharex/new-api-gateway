# Admin Chart.js 图表升级 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the admin overview and employee usage hand-written SVG line charts with locally embedded Chart.js charts.

**Architecture:** Keep the current no-build admin UI. Vendor Chart.js 4.5.1 into `internal/adminui/vendor/chartjs/`, embed it with the existing Go static assets, and add a small chart registry/helper layer in `app.js` so overview and usage charts share formatting, lifecycle cleanup, and graceful fallback behavior.

**Tech Stack:** Go `embed`, plain HTML/CSS/JavaScript, Chart.js UMD 4.5.1, existing admin API JSON responses.

---

## File Map

- `internal/adminui/vendor/chartjs/chart.umd.min.js`: new vendored Chart.js UMD bundle, served locally by `/admin/vendor/chartjs/chart.umd.min.js`.
- `internal/adminui/vendor/chartjs/LICENSE.md`: new vendored license file from the Chart.js npm package.
- `internal/adminui/static.go`: update `//go:embed` so the vendor directory is included in the admin binary.
- `internal/adminui/index.html`: load Chart.js before `app.js`.
- `internal/adminui/app.js`: add chart lifecycle helpers, Chart.js option builders, canvas renderers, and replace the two SVG chart functions.
- `internal/adminui/app.css`: remove SVG-only chart styling that no longer applies, add canvas container/fallback styling, keep existing panel/tabs/legend styles.
- `docs/superpowers/specs/2026-05-29-admin-chartjs-upgrade-design.md`: read-only reference for scope and acceptance.

---

### Task 1: Vendor Chart.js And Embed Static Assets

**Files:**
- Create: `internal/adminui/vendor/chartjs/chart.umd.min.js`
- Create: `internal/adminui/vendor/chartjs/LICENSE.md`
- Modify: `internal/adminui/static.go`
- Modify: `internal/adminui/index.html`

- [ ] **Step 1: Fetch Chart.js 4.5.1 into the vendor directory**

Run:

```bash
mkdir -p /tmp/chartjs-4.5.1 internal/adminui/vendor/chartjs
npm pack chart.js@4.5.1 --pack-destination /tmp/chartjs-4.5.1
tar -xzf /tmp/chartjs-4.5.1/chart.js-4.5.1.tgz -C /tmp/chartjs-4.5.1
cp /tmp/chartjs-4.5.1/package/dist/chart.umd.min.js internal/adminui/vendor/chartjs/chart.umd.min.js
cp /tmp/chartjs-4.5.1/package/LICENSE.md internal/adminui/vendor/chartjs/LICENSE.md
```

Expected:

```text
chart.js-4.5.1.tgz
```

- [ ] **Step 2: Verify vendored files exist and are non-empty**

Run:

```bash
test -s internal/adminui/vendor/chartjs/chart.umd.min.js
test -s internal/adminui/vendor/chartjs/LICENSE.md
wc -c internal/adminui/vendor/chartjs/chart.umd.min.js internal/adminui/vendor/chartjs/LICENSE.md
```

Expected: both `test` commands exit 0, and `wc -c` prints positive byte counts.

- [ ] **Step 3: Embed vendor files in `internal/adminui/static.go`**

Replace:

```go
//go:embed index.html app.css app.js
var assets embed.FS
```

With:

```go
//go:embed index.html app.css app.js vendor/chartjs/chart.umd.min.js vendor/chartjs/LICENSE.md
var assets embed.FS
```

- [ ] **Step 4: Load Chart.js before `app.js` in `internal/adminui/index.html`**

Replace:

```html
    <script src="/admin/app.js" defer></script>
```

With:

```html
    <script src="/admin/vendor/chartjs/chart.umd.min.js" defer></script>
    <script src="/admin/app.js" defer></script>
```

- [ ] **Step 5: Run embed-focused Go tests**

Run:

```bash
go test ./internal/adminui ./internal/admin
```

Expected: `ok` for both packages.

- [ ] **Step 6: Commit vendored asset wiring**

Run:

```bash
git add internal/adminui/static.go internal/adminui/index.html internal/adminui/vendor/chartjs/chart.umd.min.js internal/adminui/vendor/chartjs/LICENSE.md
git commit -m "feat(adminui): vendor chartjs"
```

Expected: commit succeeds.

---

### Task 2: Add Shared Chart Lifecycle And Option Helpers

**Files:**
- Modify: `internal/adminui/app.js`

- [ ] **Step 1: Add chart registry and constants near the top of `app.js`**

Insert this after `let usageRequestSeq = 0;`:

```javascript
const activeCharts = [];

const chartColors = {
  total: "#2563eb",
  totalFill: "rgba(37, 99, 235, 0.12)",
  input: "#16a34a",
  output: "#f97316",
  cache: "#7c3aed",
  grid: "#e4e9f2",
  muted: "#667085",
  ink: "#172033",
};
```

- [ ] **Step 2: Add chart helper functions after `compactNumber()`**

Insert:

```javascript
function destroyCharts() {
  while (activeCharts.length) {
    const chart = activeCharts.pop();
    try {
      chart.destroy();
    } catch (_) {
      // Ignore stale canvas cleanup errors after view swaps.
    }
  }
}

function chartAvailable() {
  return typeof window !== "undefined" && typeof window.Chart === "function";
}

function chartErrorHTML(message = "图表组件加载失败") {
  return `<div class="chart-fallback">${escapeHTML(message)}</div>`;
}

function hasPositiveValue(items, keys) {
  return items.some((item) => keys.some((key) => finiteNumber(item[key]) > 0));
}

function registerChart(canvasID, config) {
  const canvas = document.getElementById(canvasID);
  if (!canvas) return null;
  const frame = canvas.closest(".chart-frame, .chart-wrap");
  if (!chartAvailable()) {
    if (frame) frame.innerHTML = chartErrorHTML();
    return null;
  }
  try {
    const chart = new window.Chart(canvas, config);
    activeCharts.push(chart);
    return chart;
  } catch (_) {
    if (frame) frame.innerHTML = chartErrorHTML();
    return null;
  }
}

function chartAxisTicks(value) {
  return compactNumber(value);
}

function chartTooltipLabel(context) {
  const label = context.dataset.label || "";
  const value = context.parsed?.y ?? 0;
  return `${label}: ${formatNumber(value)}`;
}

function chartBaseOptions() {
  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: {
      duration: 450,
      easing: "easeOutQuart",
    },
    interaction: {
      mode: "index",
      intersect: false,
    },
    plugins: {
      legend: {
        display: false,
      },
      tooltip: {
        backgroundColor: "rgba(17, 24, 39, 0.94)",
        titleColor: "#ffffff",
        bodyColor: "#ffffff",
        borderColor: "rgba(255, 255, 255, 0.12)",
        borderWidth: 1,
        displayColors: true,
        padding: 10,
        callbacks: {
          label: chartTooltipLabel,
        },
      },
    },
    scales: {
      x: {
        grid: {
          display: false,
        },
        border: {
          color: chartColors.grid,
        },
        ticks: {
          color: chartColors.muted,
          maxRotation: 0,
          autoSkip: true,
          maxTicksLimit: 6,
        },
      },
      y: {
        beginAtZero: true,
        grid: {
          color: chartColors.grid,
          drawBorder: false,
        },
        border: {
          display: false,
        },
        ticks: {
          color: chartColors.muted,
          callback: chartAxisTicks,
        },
      },
    },
  };
}

function lineDataset({ label, data, color, backgroundColor, fill = false }) {
  return {
    label,
    data,
    borderColor: color,
    backgroundColor: backgroundColor || color,
    fill,
    borderWidth: 2.5,
    pointRadius: 0,
    pointHoverRadius: 4,
    pointHitRadius: 12,
    pointBackgroundColor: "#ffffff",
    pointBorderColor: color,
    pointBorderWidth: 2,
    tension: 0.35,
  };
}
```

- [ ] **Step 3: Call `destroyCharts()` before shell DOM replacement**

At the start of `renderShell(content)`, change:

```javascript
function renderShell(content) {
  const user = state.user || {};
```

To:

```javascript
function renderShell(content) {
  destroyCharts();
  const user = state.user || {};
```

- [ ] **Step 4: Smoke-check syntax**

Run:

```bash
node --check internal/adminui/app.js
```

Expected:

`node --check` exits 0 and prints no output.

- [ ] **Step 5: Commit shared chart helpers**

Run:

```bash
git add internal/adminui/app.js
git commit -m "feat(adminui): add chart helpers"
```

Expected: commit succeeds.

---

### Task 3: Replace Overview And Usage SVG Renderers With Chart.js

**Files:**
- Modify: `internal/adminui/app.js`
- Modify: `internal/adminui/app.css`

- [ ] **Step 1: Replace `tokenUsageChart(points)` with a canvas renderer**

Replace the whole existing `tokenUsageChart(points)` function with:

```javascript
function tokenUsageChart(points) {
  const items = arrayValue(points).map((item) => ({
    label: String(item.date || ""),
    total: finiteNumber(item.total_tokens),
  }));
  const maxTokens = Math.max(0, ...items.map((item) => item.total));
  const hasData = items.length > 0 && maxTokens > 0;

  return `
    <section class="panel usage-chart">
      <div class="chart-meta">
        <div>
          <h2>最近 30 天 Token 使用趋势</h2>
          <div class="muted">按天汇总 Total Token</div>
        </div>
        <strong>${formatNumber(maxTokens)}</strong>
      </div>
      <div class="chart-frame chart-frame-overview">
        ${hasData ? `<canvas id="overview-token-chart" aria-label="最近 30 天 Token 使用趋势" role="img"></canvas>` : `<div class="chart-empty">暂无 token 使用数据</div>`}
      </div>
    </section>
  `;
}
```

- [ ] **Step 2: Add `renderOverviewChart(points)` after `tokenUsageChart(points)`**

Insert:

```javascript
function renderOverviewChart(points) {
  const items = arrayValue(points).map((item) => ({
    label: String(item.date || ""),
    total: finiteNumber(item.total_tokens),
  }));
  if (!items.length || !hasPositiveValue(items, ["total"])) return;

  registerChart("overview-token-chart", {
    type: "line",
    data: {
      labels: items.map((item) => item.label),
      datasets: [
        lineDataset({
          label: "Total",
          data: items.map((item) => item.total),
          color: chartColors.total,
          backgroundColor: chartColors.totalFill,
          fill: true,
        }),
      ],
    },
    options: chartBaseOptions(),
  });
}
```

- [ ] **Step 3: Initialize the overview chart after rendering**

In `renderOverview(body)`, after:

```javascript
  renderShell(page("概览", `
    <div class="overview-layout">
      <section class="cards">${cards}</section>
      ${tokenUsageChart(overview.token_usage_daily)}
    </div>
  `));
```

Add:

```javascript
  renderOverviewChart(overview.token_usage_daily);
```

- [ ] **Step 4: Replace `usageChart(points)` with a canvas renderer**

Replace the whole existing `usageChart(points)` function with:

```javascript
function usageChart(points) {
  const items = arrayValue(points).map((item) => ({
    label: String(item.date || item.bucket_start || ""),
    total: finiteNumber(item.total_tokens),
    input: finiteNumber(item.prompt_tokens),
    output: finiteNumber(item.completion_tokens),
    cache: finiteNumber(item.cached_tokens),
  }));
  if (!items.length || !hasPositiveValue(items, ["total", "input", "output", "cache"])) {
    return `<div class="chart-wrap"><div class="empty-chart">暂无 token 使用数据</div></div>`;
  }

  return `
    <div class="legend" aria-label="Token 类型图例">
      <span><i class="swatch"></i>Total</span>
      <span><i class="swatch input"></i>Input</span>
      <span><i class="swatch output"></i>Output</span>
      <span><i class="swatch cache"></i>Cache</span>
    </div>
    <div class="chart-wrap">
      <canvas id="employee-usage-chart" aria-label="员工 token 使用趋势" role="img"></canvas>
    </div>
  `;
}
```

- [ ] **Step 5: Add `renderEmployeeUsageChart(points)` after `usageChart(points)`**

Insert:

```javascript
function renderEmployeeUsageChart(points) {
  const items = arrayValue(points).map((item) => ({
    label: String(item.date || item.bucket_start || ""),
    total: finiteNumber(item.total_tokens),
    input: finiteNumber(item.prompt_tokens),
    output: finiteNumber(item.completion_tokens),
    cache: finiteNumber(item.cached_tokens),
  }));
  if (!items.length || !hasPositiveValue(items, ["total", "input", "output", "cache"])) return;

  registerChart("employee-usage-chart", {
    type: "line",
    data: {
      labels: items.map((item) => item.label),
      datasets: [
        lineDataset({ label: "Total", data: items.map((item) => item.total), color: chartColors.total }),
        lineDataset({ label: "Input", data: items.map((item) => item.input), color: chartColors.input }),
        lineDataset({ label: "Output", data: items.map((item) => item.output), color: chartColors.output }),
        lineDataset({ label: "Cache", data: items.map((item) => item.cache), color: chartColors.cache }),
      ],
    },
    options: chartBaseOptions(),
  });
}
```

- [ ] **Step 6: Initialize the usage chart after rendering and binding controls**

In `renderUsage(body = {})`, after:

```javascript
  bindUsageSearch();
  bindUsageControls();
```

Add:

```javascript
  renderEmployeeUsageChart(daily);
```

- [ ] **Step 7: Update chart CSS containers in `internal/adminui/app.css`**

Replace these SVG-specific blocks:

```css
.chart-frame svg {
  display: block;
  width: 100%;
  height: auto;
  min-height: 220px;
}

.chart-grid {
  stroke: #d7dde8;
  stroke-width: 1;
}

.chart-area {
  fill: rgba(37, 99, 235, 0.1);
}

.chart-line {
  fill: none;
  stroke: var(--accent);
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 3;
}

.chart-point {
  fill: #ffffff;
  stroke: var(--accent-strong);
  stroke-width: 2;
}

.chart-axis {
  fill: var(--muted);
  font-size: 12px;
}

.chart-axis-end {
  text-anchor: end;
}
```

With:

```css
.chart-frame canvas,
.chart-wrap canvas {
  display: block;
  width: 100%;
  height: 100%;
}

.chart-frame-overview {
  height: 260px;
}

.chart-fallback {
  min-height: 180px;
  display: grid;
  place-items: center;
  color: var(--danger);
  font-weight: 650;
  text-align: center;
  padding: 20px;
}
```

- [ ] **Step 8: Remove obsolete usage SVG styles**

Delete these CSS blocks from `internal/adminui/app.css`:

```css
.chart-wrap svg {
  display: block;
  width: 100%;
  height: auto;
  min-height: 260px;
}

.usage-chart-axis,
.usage-chart-grid {
  stroke: #d7dde8;
  stroke-width: 1;
}

.usage-chart-grid {
  stroke-dasharray: 4 4;
}

.series-total,
.series-input,
.series-output,
.series-cache {
  fill: none;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.5;
}

.series-total {
  stroke: #2563eb;
}

.series-input {
  stroke: #16a34a;
  stroke-dasharray: 8 6;
}

.series-output {
  stroke: #f97316;
  stroke-dasharray: 8 6;
}

.series-cache {
  stroke: #7c3aed;
  stroke-dasharray: 8 6;
}

.chart-dot {
  fill: #ffffff;
  stroke-width: 2;
}

.chart-dot.total {
  stroke: #2563eb;
}

.chart-dot.input {
  stroke: #16a34a;
}

.chart-dot.output {
  stroke: #f97316;
}

.chart-dot.cache {
  stroke: #7c3aed;
}
```

- [ ] **Step 9: Keep `chart-wrap` dimensions stable**

Ensure the existing `.chart-wrap` block is:

```css
.chart-wrap {
  position: relative;
  height: 300px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: #ffffff;
  overflow: hidden;
}
```

- [ ] **Step 10: Add mobile chart height overrides**

Inside the existing `@media (max-width: 820px)` block, add:

```css
  .chart-frame-overview,
  .chart-wrap {
    height: 240px;
  }
```

- [ ] **Step 11: Run syntax and admin tests**

Run:

```bash
node --check internal/adminui/app.js
go test ./internal/adminui ./internal/admin
```

Expected: `node --check` exits 0 with no syntax errors; Go tests pass.

- [ ] **Step 12: Commit chart replacement**

Run:

```bash
git add internal/adminui/app.js internal/adminui/app.css
git commit -m "feat(adminui): render usage charts with chartjs"
```

Expected: commit succeeds.

---

### Task 4: Browser Verification And Final Documentation Check

**Files:**
- Inspect: `README.md`
- Inspect: `ARCHITECTURE.md`
- Inspect: `CLAUDE.md` if present
- Inspect: `internal/adminui/app.js`
- Inspect: `internal/adminui/app.css`

- [ ] **Step 1: Check whether documentation needs updates**

Run:

```bash
test -f CLAUDE.md && sed -n '1,220p' CLAUDE.md || true
rg -n "admin UI|adminui|管理 Web UI|管理后台|Chart.js|chart" README.md ARCHITECTURE.md CLAUDE.md 2>/dev/null || true
```

Expected: no deployment command, architecture contract, environment variable, or service dependency docs require changes because Chart.js is embedded static admin UI code. If a doc explicitly lists admin UI assets, update that line to mention vendored Chart.js; otherwise make no docs commit.

- [ ] **Step 2: Start the gateway for local browser verification**

Run:

```bash
make run
```

Expected: gateway starts and serves `/admin`. Keep this process running while performing browser checks.

- [ ] **Step 3: Verify local Chart.js asset is served**

Open:

```text
http://localhost:8080/admin/vendor/chartjs/chart.umd.min.js
```

Expected: HTTP 200 and JavaScript content beginning with Chart.js license/banner text or minified code.

- [ ] **Step 4: Verify overview chart renders**

Open:

```text
http://localhost:8080/admin/
```

Log in with the local admin credentials configured for the environment. Navigate to `概览`.

Expected:

- The page shows the existing metric cards.
- The chart area contains a canvas rather than SVG.
- Hovering the chart shows a Chart.js tooltip.
- Browser console has no chart initialization errors.

- [ ] **Step 5: Verify usage chart renders and refreshes**

Navigate to `用量`, enter a username that has usage data, and submit the search.

Expected:

- Summary cards render as before.
- The chart area contains a canvas rather than SVG.
- Legend labels are `Total`, `Input`, `Output`, `Cache`.
- Hovering the chart shows values for the same date.
- Clicking `1d`, `7d`, and `30d` refreshes without console errors.
- Clicking model filter buttons refreshes without console errors.
- `Model 汇总` remains a table.

- [ ] **Step 6: Verify empty data state**

In `用量`, search for a username that has no usage data.

Expected:

- Empty chart message remains Chinese: `暂无 token 使用数据`.
- No blank or flat zero-value Chart.js canvas is created.
- Browser console has no errors.

- [ ] **Step 7: Verify Chart.js missing fallback manually**

Temporarily edit `internal/adminui/index.html` in the working tree only:

```html
    <script src="/admin/vendor/chartjs/chart.umd.min.js.disabled" defer></script>
```

Reload `概览` and `用量`.

Expected:

- Chart areas show `图表组件加载失败`.
- Metric cards, search controls, range/model buttons, and `Model 汇总` still work.
- Browser console may show the failed script request, but `app.js` must not throw an uncaught exception.

Restore the original script path before continuing:

```html
    <script src="/admin/vendor/chartjs/chart.umd.min.js" defer></script>
```

- [ ] **Step 8: Run final automated checks**

Run:

```bash
node --check internal/adminui/app.js
go test ./internal/adminui ./internal/admin
git status --short
```

Expected:

- JavaScript syntax check passes.
- Go tests pass.
- `git status --short` shows only intentional changes, or is clean if all task commits are complete.

- [ ] **Step 9: Commit any final docs or fallback fixes**

If Step 1 required documentation edits or browser verification found a small fix, commit those intentional changes:

```bash
git add README.md ARCHITECTURE.md CLAUDE.md internal/adminui/index.html internal/adminui/app.js internal/adminui/app.css
git commit -m "chore(adminui): finalize chartjs upgrade"
```

Expected: commit succeeds only when there are actual final changes. If there are no changes, skip this commit.

---

## Self-Review Notes

- Spec coverage: vendored local Chart.js, Go embed, overview chart, usage chart, lifecycle cleanup, empty data, missing-library fallback, no API/schema changes, no Model summary chart, and browser verification are all covered.
- Placeholder scan: no unfinished implementation steps or vague test instructions remain.
- Type consistency: helper names used by renderers are defined in Task 2 before Task 3 uses them: `destroyCharts`, `registerChart`, `chartBaseOptions`, `lineDataset`, `hasPositiveValue`, and `finiteNumber`.
