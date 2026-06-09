# Trace Analysis Results Presentation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the generic trace-detail analysis-results table with analyzer-specific cards that clearly distinguish factual usage data from work-relevance classification output.

**Architecture:** Keep the existing backend contract unchanged and move the presentation logic into a small, testable browser helper dedicated to trace analysis result formatting. The bundled admin UI will load that helper before `app.js`, use it to render analyzer cards in trace detail, and fall back to the old table only if the helper is unexpectedly unavailable.

**Tech Stack:** Vanilla browser JavaScript, embedded admin UI assets via Go `embed`, Node built-in `node:test`, Go unit compile checks with `go test`.

---

## File Structure

- Create: `internal/adminui/analysis_result_cards.js`
  Purpose: pure formatting and HTML rendering for `usage_extraction`, `work_relevance`, and fallback analyzer cards; usable from both browser and Node tests.
- Create: `internal/adminui/analysis_result_cards.test.js`
  Purpose: Node-based unit coverage for card view-model generation and rendered HTML fragments.
- Modify: `internal/adminui/index.html`
  Purpose: load the new helper before `app.js`.
- Modify: `internal/adminui/static.go`
  Purpose: embed the new helper into the Go binary so `/admin/analysis_result_cards.js` is served.
- Modify: `internal/adminui/app.js`
  Purpose: replace the trace-detail analysis table with helper-based card rendering while keeping a defensive fallback path.
- Modify: `internal/adminui/app.css`
  Purpose: style the new card stack, badge placement, metadata rows, and the collapsed raw JSON `<details>` section.
- Review only: `README.md`, `ARCHITECTURE.md`, `CLAUDE.md`
  Purpose: confirm no documentation promises the old shared-score table semantics; only update if a stale UI description exists.

## Task 1: Lock the Formatting Contract with Failing Node Tests

**Files:**
- Create: `internal/adminui/analysis_result_cards.test.js`
- Test: `internal/adminui/analysis_result_cards.test.js`

- [ ] **Step 1: Write the failing card-formatting tests**

Create `internal/adminui/analysis_result_cards.test.js` with:

```js
const test = require("node:test");
const assert = require("node:assert/strict");

const {
  buildAnalysisResultCardModel,
  renderAnalysisResultCards,
} = require("./analysis_result_cards.js");

test("buildAnalysisResultCardModel formats work_relevance as conclusion-first", () => {
  const model = buildAnalysisResultCardModel({
    analyzer_name: "work_relevance",
    category: "work_relevance",
    label: "debugging",
    score: "0.92",
    confidence: "0.95",
    severity: "review",
    created_at: "2026-06-05 09:15:00+00",
    result_json: JSON.stringify({
      task_category: "debugging",
      decision: "work_related",
      recommended_action: "allow",
      needs_review: true,
      work_related_score: 0.92,
      personal_use_score: 0.02,
      score_breakdown: {
        work: 0.92,
        non_work: 0.02,
        risk: 0.0,
      },
    }),
  });

  assert.equal(model.variant, "work_relevance");
  assert.equal(model.title, "work_related · debugging");
  assert.deepEqual(model.summaryItems, [
    "work 0.92",
    "non-work 0.02",
    "risk 0.00",
  ]);
  assert.equal(model.badge, "review");
  assert.match(model.meta.join(" | "), /confidence 0.95/);
  assert.match(model.meta.join(" | "), /allow/);
  assert.match(model.detailsJSON, /"recommended_action": "allow"/);
});

test("buildAnalysisResultCardModel formats usage_extraction as token facts", () => {
  const model = buildAnalysisResultCardModel({
    analyzer_name: "usage_extraction",
    category: "usage_extraction",
    label: "usage_from_gateway_job",
    score: "18420",
    confidence: "1",
    severity: "",
    created_at: "2026-06-05 09:15:00+00",
    result_json: JSON.stringify({
      prompt_tokens: 8200,
      completion_tokens: 10000,
      cached_tokens: 220,
      reasoning_tokens: 18,
      total_tokens: 18420,
    }),
  });

  assert.equal(model.variant, "usage_extraction");
  assert.equal(model.title, "18,420 total tokens");
  assert.deepEqual(model.summaryItems, [
    "input 8,200",
    "output 10,000",
    "cache 220",
    "reasoning 18",
  ]);
  assert.match(model.meta.join(" | "), /usage available/);
});

test("buildAnalysisResultCardModel falls back for unknown analyzers", () => {
  const model = buildAnalysisResultCardModel({
    analyzer_name: "custom_rule",
    category: "custom_rule",
    label: "odd_shape",
    score: "7",
    confidence: "0.4",
    severity: "low",
    created_at: "2026-06-05 09:15:00+00",
    result_json: "{\"ok\":true}",
  });

  assert.equal(model.variant, "generic");
  assert.equal(model.title, "custom_rule");
  assert.deepEqual(model.summaryItems, ["label odd_shape", "score 7", "confidence 0.4"]);
  assert.equal(model.badge, "low");
});

test("renderAnalysisResultCards renders details blocks and empty state", () => {
  const html = renderAnalysisResultCards([
    {
      analyzer_name: "usage_extraction",
      category: "usage_extraction",
      label: "usage_not_available",
      score: "0",
      confidence: "0",
      severity: "",
      created_at: "2026-06-05 09:15:00+00",
      result_json: "{\"total_tokens\":0}",
    },
  ]);
  assert.match(html, /analysis-result-grid/);
  assert.match(html, /查看原始 JSON/);
  assert.match(renderAnalysisResultCards([]), /暂无分析结果/);
});
```

- [ ] **Step 2: Run the new test file and verify it fails**

Run:

```bash
node --test internal/adminui/analysis_result_cards.test.js
```

Expected: FAIL because `internal/adminui/analysis_result_cards.js` does not exist yet.

## Task 2: Implement a Shared Card-Rendering Helper and Asset Wiring

**Files:**
- Create: `internal/adminui/analysis_result_cards.js`
- Modify: `internal/adminui/index.html`
- Modify: `internal/adminui/static.go`
- Test: `internal/adminui/analysis_result_cards.test.js`

- [ ] **Step 1: Create the helper module used by both the browser and Node tests**

Create `internal/adminui/analysis_result_cards.js` with:

```js
function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function finiteNumber(value) {
  const number = Number(value ?? 0);
  return Number.isFinite(number) ? number : 0;
}

function formatCount(value) {
  return finiteNumber(value).toLocaleString();
}

function formatScore(value) {
  return finiteNumber(value).toFixed(2);
}

function formatTime(value) {
  return String(value ?? "").replace(/(\d{2}:\d{2}:\d{2})\.\d+/, "$1");
}

function parseResultJSON(value) {
  if (value && typeof value === "object") return value;
  if (!value) return {};
  try {
    const parsed = JSON.parse(String(value));
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch (_) {
    return {};
  }
}

function buildWorkRelevanceCard(item, payload) {
  const breakdown = payload.score_breakdown || {};
  const category = payload.task_category || item.label || "unknown";
  const decision = payload.decision || "unknown";
  return {
    variant: "work_relevance",
    analyzerName: item.analyzer_name || "work_relevance",
    badge: payload.needs_review || String(item.severity || "").toLowerCase() === "review" ? "review" : "",
    title: `${decision} · ${category}`,
    summaryItems: [
      `work ${formatScore(breakdown.work ?? payload.work_related_score ?? item.score)}`,
      `non-work ${formatScore(breakdown.non_work ?? payload.personal_use_score)}`,
      `risk ${formatScore(breakdown.risk ?? 0)}`,
    ],
    meta: [
      `confidence ${formatScore(payload.confidence ?? item.confidence)}`,
      `action ${payload.recommended_action || "record_only"}`,
      formatTime(item.created_at),
    ],
    detailsJSON: JSON.stringify(payload, null, 2),
  };
}

function buildUsageExtractionCard(item, payload) {
  const totalTokens = payload.total_tokens ?? item.score ?? 0;
  const available = finiteNumber(payload.total_tokens ?? item.score) > 0 && finiteNumber(item.confidence) > 0;
  return {
    variant: "usage_extraction",
    analyzerName: item.analyzer_name || "usage_extraction",
    badge: String(item.severity || "").toLowerCase(),
    title: `${formatCount(totalTokens)} total tokens`,
    summaryItems: [
      `input ${formatCount(payload.prompt_tokens)}`,
      `output ${formatCount(payload.completion_tokens)}`,
      `cache ${formatCount(payload.cached_tokens)}`,
      `reasoning ${formatCount(payload.reasoning_tokens)}`,
    ],
    meta: [
      available ? "usage available" : "usage missing",
      formatTime(item.created_at),
    ],
    detailsJSON: JSON.stringify(payload, null, 2),
  };
}

function buildGenericCard(item, payload) {
  return {
    variant: "generic",
    analyzerName: item.analyzer_name || "unknown",
    badge: String(item.severity || "").toLowerCase(),
    title: item.analyzer_name || item.category || "unknown",
    summaryItems: [
      `label ${item.label || "unknown"}`,
      `score ${item.score || "0"}`,
      `confidence ${item.confidence || "0"}`,
    ],
    meta: [
      item.category || "unknown",
      formatTime(item.created_at),
    ],
    detailsJSON: JSON.stringify(payload, null, 2),
  };
}

function buildAnalysisResultCardModel(item) {
  const payload = parseResultJSON(item?.result_json);
  if ((item?.analyzer_name || "") === "work_relevance") {
    return buildWorkRelevanceCard(item, payload);
  }
  if ((item?.analyzer_name || "") === "usage_extraction") {
    return buildUsageExtractionCard(item, payload);
  }
  return buildGenericCard(item || {}, payload);
}

function renderBadge(value) {
  if (!value) return "";
  return `<span class="badge ${escapeHTML(String(value).toLowerCase())}">${escapeHTML(value)}</span>`;
}

function renderAnalysisResultCard(item) {
  const model = buildAnalysisResultCardModel(item);
  const summary = model.summaryItems.map((entry) => `<span>${escapeHTML(entry)}</span>`).join("");
  const meta = model.meta.map((entry) => `<span>${escapeHTML(entry)}</span>`).join("");
  return `
    <article class="analysis-card analysis-card-${escapeHTML(model.variant)}">
      <div class="analysis-card-head">
        <div>
          <div class="analysis-card-kicker">${escapeHTML(model.analyzerName)}</div>
          <div class="analysis-card-title">${escapeHTML(model.title)}</div>
        </div>
        ${renderBadge(model.badge)}
      </div>
      <div class="analysis-card-summary">${summary}</div>
      <div class="analysis-card-meta">${meta}</div>
      <details class="analysis-card-details">
        <summary>查看原始 JSON</summary>
        <pre>${escapeHTML(model.detailsJSON)}</pre>
      </details>
    </article>
  `;
}

function renderAnalysisResultCards(items) {
  const list = Array.isArray(items) ? items : [];
  if (!list.length) {
    return `<div class="muted">暂无分析结果。</div>`;
  }
  return `<div class="analysis-result-grid">${list.map(renderAnalysisResultCard).join("")}</div>`;
}

const AdminAnalysisResultCards = {
  buildAnalysisResultCardModel,
  renderAnalysisResultCards,
};

if (typeof module !== "undefined" && module.exports) {
  module.exports = AdminAnalysisResultCards;
}

if (typeof window !== "undefined") {
  window.AdminAnalysisResultCards = AdminAnalysisResultCards;
}
```

- [ ] **Step 2: Load the helper before `app.js` and embed it into the Go binary**

In `internal/adminui/index.html`, replace the closing script block with:

```html
    <script src="/admin/vendor/chartjs/chart.umd.min.js" defer></script>
    <script src="/admin/analysis_result_cards.js" defer></script>
    <script src="/admin/app.js" defer></script>
```

In `internal/adminui/static.go`, update the embed directive to:

```go
//go:embed index.html app.css app.js analysis_result_cards.js vendor/chartjs/chart.umd.min.js vendor/chartjs/LICENSE.md
var assets embed.FS
```

- [ ] **Step 3: Run helper tests and syntax checks**

Run:

```bash
node --test internal/adminui/analysis_result_cards.test.js
node --check internal/adminui/analysis_result_cards.js
go test ./internal/adminui
```

Expected: all commands PASS.

- [ ] **Step 4: Commit the helper layer**

Run:

```bash
git add internal/adminui/analysis_result_cards.js internal/adminui/analysis_result_cards.test.js internal/adminui/index.html internal/adminui/static.go
git commit -m "test(adminui): add trace analysis card formatter coverage"
```

Expected: commit succeeds.

## Task 3: Switch Trace Detail to Cards and Style the New Presentation

**Files:**
- Modify: `internal/adminui/app.js`
- Modify: `internal/adminui/app.css`
- Test: `internal/adminui/analysis_result_cards.test.js`

- [ ] **Step 1: Add a dedicated trace-detail rendering wrapper in `app.js`**

In `internal/adminui/app.js`, add this helper near `renderTraceDetail`:

```js
function renderTraceAnalysisResults(items) {
  const helper = window.AdminAnalysisResultCards;
  if (helper && typeof helper.renderAnalysisResultCards === "function") {
    return helper.renderAnalysisResultCards(arrayValue(items));
  }
  const rows = arrayValue(items).map((item) => [
    item.analyzer_name,
    item.category,
    item.label,
    item.score,
    item.confidence,
    badge(item.severity),
    formatTime(item.created_at),
  ]);
  return table(["分析器", "分类", "标签", "分数", "置信度", "Severity", "时间 (UTC+8)"], rows);
}
```

- [ ] **Step 2: Replace the trace-detail analysis table with the helper output**

In `renderTraceDetail`, delete:

```js
  const analysis = arrayValue(trace.analysis_results).map((item) => [
    item.analyzer_name,
    item.category,
    item.label,
    item.score,
    item.confidence,
    badge(item.severity),
    formatTime(item.created_at),
  ]);
```

Then replace:

```js
        <section class="panel"><h2>分析结果</h2>${table(["分析器", "分类", "标签", "分数", "置信度", "Severity", "时间 (UTC+8)"], analysis)}</section>
```

with:

```js
        <section class="panel"><h2>分析结果</h2>${renderTraceAnalysisResults(trace.analysis_results)}</section>
```

- [ ] **Step 3: Add card styles without disturbing the rest of the admin UI**

In `internal/adminui/app.css`, add the following block after `.meta-item span`:

```css
.analysis-result-grid {
  display: grid;
  gap: 12px;
}

.analysis-card {
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 14px;
  background: #ffffff;
}

.analysis-card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.analysis-card-kicker {
  color: var(--muted);
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
}

.analysis-card-title {
  margin-top: 4px;
  font-size: 18px;
  font-weight: 750;
  line-height: 1.3;
}

.analysis-card-summary,
.analysis-card-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 10px;
}

.analysis-card-summary span,
.analysis-card-meta span {
  display: inline-flex;
  align-items: center;
  border-radius: 999px;
  padding: 4px 10px;
  background: #f8fafc;
  border: 1px solid var(--line);
}

.analysis-card-details {
  margin-top: 12px;
}

.analysis-card-details summary {
  cursor: pointer;
  color: var(--accent-strong);
  font-weight: 650;
}

.analysis-card-details pre {
  margin: 10px 0 0;
  padding: 12px;
  border-radius: 8px;
  background: #111827;
  color: #f9fafb;
  overflow: auto;
  font-size: 12px;
  line-height: 1.5;
}
```

- [ ] **Step 4: Run focused checks after wiring the UI**

Run:

```bash
node --test internal/adminui/analysis_result_cards.test.js
node --check internal/adminui/analysis_result_cards.js
node --check internal/adminui/app.js
go test ./internal/admin ./internal/adminui
```

Expected: all commands PASS.

- [ ] **Step 5: Commit the trace-detail presentation changes**

Run:

```bash
git add internal/adminui/app.js internal/adminui/app.css
git commit -m "feat(adminui): present trace analysis results as cards"
```

Expected: commit succeeds.

## Task 4: Manual Verification and Documentation Audit

**Files:**
- Review: `README.md`
- Review: `ARCHITECTURE.md`
- Review: `CLAUDE.md`

- [ ] **Step 1: Run a focused documentation audit**

Run:

```bash
rg -n "分析结果|Trace 详情|analysis_results|分数" README.md ARCHITECTURE.md CLAUDE.md
```

Expected: inspect the matches and confirm no user-facing documentation promises the old generic trace-detail analysis table. If a stale sentence explicitly describes the old table semantics, update that sentence before continuing; otherwise make no documentation edits.

- [ ] **Step 2: Start the app and manually inspect the trace-detail page**

Run:

```bash
make run
```

Then open `/admin`, authenticate, open a trace that contains both `usage_extraction` and `work_relevance`, and verify:

- `work_relevance` shows `decision + category` as the card title
- `work / non-work / risk` appears directly below the title
- `review` badge appears when `needs_review` or severity requires it
- `usage_extraction` shows token facts instead of a generic score row
- expanding `查看原始 JSON` reveals raw structured detail without breaking layout

- [ ] **Step 3: Re-run the final command set before handoff**

Run:

```bash
node --test internal/adminui/analysis_result_cards.test.js
node --check internal/adminui/analysis_result_cards.js
node --check internal/adminui/app.js
go test ./internal/admin ./internal/adminui
```

Expected: all commands PASS again after manual validation and any doc touch-up.

- [ ] **Step 4: Summarize the doc decision in the handoff**

Record one of these outcomes in the implementation handoff or PR summary:

```text
Docs review complete: no README/ARCHITECTURE/CLAUDE changes required because this is a trace-detail presentation-only change.
```

or

```text
Docs review complete: updated stale trace-detail wording to match analyzer-card presentation.
```
