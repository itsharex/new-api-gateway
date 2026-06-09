# Analysis Runtime 采样文案实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将分析运行页两个趋势卡片右上角的 `X 点` 改成用户可理解的“近 X 采样 N 次 / 近 X 暂无采样”，并通过 tooltip 解释采样次数的含义。

**Architecture:** 把采样文案规则提取为一个纯 JavaScript helper，既给浏览器端 `app.js` 复用，也给 `node --test` 做轻量单测。Admin UI 继续使用现有静态资源打包方式，通过新增脚本、复用现有浮层样式和泛化现有 tooltip 事件处理来完成交互，不引入后端接口改动。

**Tech Stack:** JavaScript (vanilla browser + Node test runner), HTML, CSS, Go embed (`internal/adminui/static.go`)

---

### Task 1: 提取可测试的采样文案 helper

**Files:**
- Create: `internal/adminui/runtime_copy.js`
- Create: `internal/adminui/runtime_copy.test.js`

- [ ] **Step 1: 先写失败的 Node 单测，锁定范围映射、空态和 tooltip 文案**

创建 `internal/adminui/runtime_copy.test.js`：

```js
const test = require("node:test");
const assert = require("node:assert/strict");

const {
  runtimeSamplingRangeLabel,
  runtimeSamplingSummary,
  runtimeSamplingTooltip,
} = require("./runtime_copy.js");

test("runtimeSamplingRangeLabel maps supported ranges to Chinese labels", () => {
  assert.equal(runtimeSamplingRangeLabel("15m"), "近 15 分钟");
  assert.equal(runtimeSamplingRangeLabel("1h"), "近 1 小时");
  assert.equal(runtimeSamplingRangeLabel("24h"), "近 24 小时");
});

test("runtimeSamplingRangeLabel falls back to 1h label for unknown range", () => {
  assert.equal(runtimeSamplingRangeLabel("6h"), "近 1 小时");
  assert.equal(runtimeSamplingRangeLabel(""), "近 1 小时");
});

test("runtimeSamplingSummary renders count copy for non-empty history", () => {
  assert.equal(runtimeSamplingSummary("15m", 12), "近 15 分钟采样 12 次");
  assert.equal(runtimeSamplingSummary("1h", 83), "近 1 小时采样 83 次");
  assert.equal(runtimeSamplingSummary("24h", 1248), "近 24 小时采样 1,248 次");
});

test("runtimeSamplingSummary renders empty copy instead of zero count", () => {
  assert.equal(runtimeSamplingSummary("15m", 0), "近 15 分钟暂无采样");
  assert.equal(runtimeSamplingSummary("1h", -3), "近 1 小时暂无采样");
});

test("runtimeSamplingTooltip explains the metric semantics", () => {
  assert.match(runtimeSamplingTooltip(), /不代表请求数、队列长度或时延值/);
  assert.match(runtimeSamplingTooltip(), /worker 重启、暂停或采样间隔调整/);
});
```

- [ ] **Step 2: 运行单测，确认当前缺少 helper 文件而失败**

Run: `node --test internal/adminui/runtime_copy.test.js`

Expected: FAIL，错误包含 `Cannot find module './runtime_copy.js'`

- [ ] **Step 3: 新建纯函数 helper，兼容浏览器和 Node `require`**

创建 `internal/adminui/runtime_copy.js`：

```js
(function attachRuntimeCopy(globalObject) {
  const rangeLabels = {
    "15m": "近 15 分钟",
    "1h": "近 1 小时",
    "24h": "近 24 小时",
  };

  function runtimeSamplingRangeLabel(range) {
    const normalized = String(range || "").trim();
    return rangeLabels[normalized] || rangeLabels["1h"];
  }

  function runtimeSamplingSummary(range, sampleCount) {
    const label = runtimeSamplingRangeLabel(range);
    const count = Number(sampleCount);
    if (!Number.isFinite(count) || count <= 0) {
      return `${label}暂无采样`;
    }
    return `${label}采样 ${count.toLocaleString()} 次`;
  }

  function runtimeSamplingTooltip() {
    return "采样次数表示当前时间范围内记录到的运行采样数量，不代表请求数、队列长度或时延值。采样通常按固定间隔生成；如果 worker 重启、暂停或采样间隔调整，次数可能不是固定值。";
  }

  const api = {
    runtimeSamplingRangeLabel,
    runtimeSamplingSummary,
    runtimeSamplingTooltip,
  };

  globalObject.AdminRuntimeCopy = api;

  if (typeof module !== "undefined" && module.exports) {
    module.exports = api;
  }
})(typeof window !== "undefined" ? window : globalThis);
```

- [ ] **Step 4: 重跑 Node 单测，确认 helper 行为通过**

Run: `node --test internal/adminui/runtime_copy.test.js`

Expected: PASS，5 个测试全部通过

- [ ] **Step 5: Commit**

```bash
git add internal/adminui/runtime_copy.js internal/adminui/runtime_copy.test.js
git commit -m "test(adminui): add analysis runtime sampling copy helper"
```

---

### Task 2: 把 helper 接入 Admin UI 静态资源打包

**Files:**
- Modify: `internal/adminui/index.html:13-14`
- Modify: `internal/adminui/static.go:9`

- [ ] **Step 1: 先让浏览器加载新 helper 脚本，再加载 `app.js`**

把 `internal/adminui/index.html` 末尾脚本区从：

```html
    <script src="/admin/vendor/chartjs/chart.umd.min.js" defer></script>
    <script src="/admin/app.js" defer></script>
```

改成：

```html
    <script src="/admin/vendor/chartjs/chart.umd.min.js" defer></script>
    <script src="/admin/runtime_copy.js" defer></script>
    <script src="/admin/app.js" defer></script>
```

- [ ] **Step 2: 更新 embed 清单，确保 Go 二进制能打包新文件**

把 `internal/adminui/static.go` 的 `go:embed` 行从：

```go
//go:embed index.html app.css app.js vendor/chartjs/chart.umd.min.js vendor/chartjs/LICENSE.md
```

改成：

```go
//go:embed index.html app.css app.js runtime_copy.js vendor/chartjs/chart.umd.min.js vendor/chartjs/LICENSE.md
```

- [ ] **Step 3: 运行 Go 测试，确认新增静态资源不会破坏编译或 embed**

Run: `go test ./internal/admin ./internal/adminui ./cmd/audit-gateway -v`

Expected: PASS，`internal/adminui` 能正常编译，embed 不报缺文件错误

- [ ] **Step 4: Commit**

```bash
git add internal/adminui/index.html internal/adminui/static.go
git commit -m "feat(adminui): bundle runtime sampling copy helper"
```

---

### Task 3: 用新文案替换图表右上角点数，并把 tooltip 机制泛化到 summary

**Files:**
- Modify: `internal/adminui/app.js:1-40`
- Modify: `internal/adminui/app.js:453-476`
- Modify: `internal/adminui/app.js:691-733`
- Modify: `internal/adminui/app.css:304-320`
- Modify: `internal/adminui/app.css:599-616`
- Modify: `internal/adminui/app.css:648-651`

- [ ] **Step 1: 在 `app.js` 顶部接入 helper，并提供兜底实现**

在 `internal/adminui/app.js` 顶部 `const app = document.querySelector("#app");` 下方插入：

```js
const runtimeCopy = window.AdminRuntimeCopy || {
  runtimeSamplingRangeLabel(range) {
    const normalized = String(range || "").trim();
    if (normalized === "15m") return "近 15 分钟";
    if (normalized === "24h") return "近 24 小时";
    return "近 1 小时";
  },
  runtimeSamplingSummary(range, sampleCount) {
    const label = this.runtimeSamplingRangeLabel(range);
    const count = Number(sampleCount);
    if (!Number.isFinite(count) || count <= 0) return `${label}暂无采样`;
    return `${label}采样 ${count.toLocaleString()} 次`;
  },
  runtimeSamplingTooltip() {
    return "采样次数表示当前时间范围内记录到的运行采样数量，不代表请求数、队列长度或时延值。采样通常按固定间隔生成；如果 worker 重启、暂停或采样间隔调整，次数可能不是固定值。";
  },
};
```

- [ ] **Step 2: 在 `app.js` 里添加图表 summary HTML helper**

在 `runtimeQueueChart` 之前插入：

```js
function runtimeSamplingSummaryHTML(range, count) {
  const summary = runtimeCopy.runtimeSamplingSummary(range, count);
  const tooltip = runtimeCopy.runtimeSamplingTooltip();
  return `
    <strong
      class="chart-summary"
      tabindex="0"
      data-tooltip="${escapeHTML(tooltip)}"
      aria-label="${escapeHTML(`${summary}。查看采样次数说明`)}"
    >${escapeHTML(summary)}</strong>
  `;
}
```

- [ ] **Step 3: 把 `X 点` 改成新 summary 文案，空数组时也显示“暂无采样”**

把 `runtimeQueueChart` 和 `runtimeLatencyChart` 中的右侧 `<strong>` 分别从：

```js
        <strong>${formatNumber(items.length)} 点</strong>
```

改成：

```js
        ${runtimeSamplingSummaryHTML(state.analysisRuntime.range, items.length)}
```

这样两张图会统一输出：

- `近 15 分钟采样 N 次`
- `近 1 小时采样 N 次`
- `近 24 小时采样 N 次`
- `近 X 暂无采样`

- [ ] **Step 4: 把现有 `.cell-truncate` tooltip 逻辑泛化为“显式 data-tooltip + 截断文本”双模式**

把 `internal/adminui/app.js` 里第 453-476 行附近的 tooltip 事件块整体替换为：

```js
  let tooltip = null;
  const main = document.querySelector(".main");

  function removeTooltip() {
    if (tooltip) {
      tooltip.remove();
      tooltip = null;
    }
  }

  function tooltipTextForElement(el) {
    if (!el) return "";
    const explicit = el.getAttribute("data-tooltip");
    if (explicit) return explicit;
    if (el.matches(".cell-truncate") && el.scrollWidth > el.clientWidth) {
      return el.textContent;
    }
    return "";
  }

  function showTooltipForElement(el, text) {
    removeTooltip();
    tooltip = document.createElement("div");
    tooltip.className = "cell-tooltip";
    tooltip.textContent = text;
    document.body.appendChild(tooltip);
    const rect = el.getBoundingClientRect();
    const tipRect = tooltip.getBoundingClientRect();
    let top = rect.bottom + 6;
    let left = rect.left;
    if (top + tipRect.height > window.innerHeight - 8) top = rect.top - tipRect.height - 6;
    if (left + tipRect.width > window.innerWidth - 8) left = window.innerWidth - tipRect.width - 8;
    if (left < 8) left = 8;
    tooltip.style.top = top + "px";
    tooltip.style.left = left + "px";
  }

  main.addEventListener("pointerenter", (e) => {
    const el = e.target.closest("[data-tooltip], .cell-truncate");
    const text = tooltipTextForElement(el);
    if (!text) return;
    showTooltipForElement(el, text);
  }, true);

  main.addEventListener("pointerleave", (e) => {
    if (!e.target.closest("[data-tooltip], .cell-truncate")) return;
    removeTooltip();
  }, true);

  main.addEventListener("focusin", (e) => {
    const el = e.target.closest("[data-tooltip]");
    const text = tooltipTextForElement(el);
    if (!text) return;
    showTooltipForElement(el, text);
  }, true);

  main.addEventListener("focusout", (e) => {
    if (!e.target.closest("[data-tooltip]")) return;
    removeTooltip();
  }, true);
```

- [ ] **Step 5: 给 summary 增加可感知的 hover / focus 样式，同时照顾窄屏换行**

在 `internal/adminui/app.css` 中：

1. 保留 `.chart-meta` 结构不变。
2. 在 `.chart-meta strong` 之后新增：

```css
.chart-summary {
  display: inline-block;
  font-size: 20px;
  line-height: 1.2;
  white-space: nowrap;
  cursor: help;
  text-decoration: underline dotted;
  text-underline-offset: 3px;
}

.chart-summary:focus-visible {
  outline: 3px solid var(--focus);
  outline-offset: 2px;
  border-radius: 4px;
}
```

3. 在移动端媒体查询中的 `.chart-meta { display: grid; }` 后面追加：

```css
  .chart-summary {
    white-space: normal;
    justify-self: start;
  }
```

- [ ] **Step 6: 运行测试和编译检查**

Run: `node --test internal/adminui/runtime_copy.test.js && go test ./internal/admin ./internal/adminui ./cmd/audit-gateway -v`

Expected: PASS，Node 文案测试通过，Go 侧编译与现有测试通过

- [ ] **Step 7: Commit**

```bash
git add internal/adminui/app.js internal/adminui/app.css
git commit -m "feat(adminui): clarify analysis runtime sampling summaries"
```

---

### Task 4: 手工验证分析运行页交互，并确认无需更新文档

**Files:** 无代码改动

- [ ] **Step 1: 启动本地网关并打开 Admin UI 分析运行页**

Run: `make run`

Expected: 网关成功启动，后台可访问

验证项：

- `Core 队列趋势` 右上角显示 `近 1 小时采样 N 次` 或 `近 1 小时暂无采样`
- `Core 时延趋势` 使用同样口径
- 切换 `15m / 1h / 24h` 后，范围文案随之变化
- 当 history 为空时，不再出现 `0 次`

- [ ] **Step 2: 验证 hover 和键盘 focus 都能看到 tooltip**

在分析运行页分别检查：

- 鼠标悬停 `chart-summary` 时出现说明浮层
- `Tab` 聚焦到 summary 时同样出现说明浮层
- tooltip 文案说明“不是请求数、队列长度或时延值”
- 移出或失焦后 tooltip 消失

- [ ] **Step 3: 检查移动端窄布局不发生难看溢出**

Run: 使用浏览器响应式视图或窄窗口，将宽度压到 `820px` 以下

Expected:

- `chart-summary` 允许换行
- 文案不会把图表头部挤出卡片
- 左右信息仍能稳定对齐

- [ ] **Step 4: 检查仓库文档是否需要同步**

Run: `rg -n "83 点|采样 .* 次|分析运行" README.md ARCHITECTURE.md docs`

Expected: 若无文档直接描述旧 UI 文案，则记录“本次为纯前端展示调整，无需更新文档”；若搜到依赖旧文案的截图或说明，再单独补文档任务

- [ ] **Step 5: 最终回归**

Run: `make test`

Expected: PASS，确认这次前端静态资源改动没有引入仓库级回归
