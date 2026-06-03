const app = document.querySelector("#app");

const state = {
  user: null,
  view: "overview",
  error: "",
  usage: {
    username: "",
    range: "30d",
    model: "",
  },
  password: {
    error: "",
    success: "",
  },
};

let usageRequestSeq = 0;

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

const views = [
  { id: "overview", label: "概览" },
  { id: "usage", label: "用量" },
  { id: "traces", label: "Trace" },
  { id: "identities", label: "员工目录" },
  { id: "anomalies", label: "异常" },
  { id: "coverage", label: "覆盖" },
  { id: "lookup", label: "API Key 查询" },
  { id: "context", label: "Context 目录" },
  { id: "reviews", label: "审核记录" },
  { id: "settings", label: "系统设置" },
  { id: "audit", label: "审计日志" },
];

function resetPasswordState() {
  state.password.error = "";
  state.password.success = "";
}

function passwordLength(value) {
  return Array.from(value).length;
}

function csrfToken() {
  const match = document.cookie.split("; ").find((part) => part.startsWith("audit_admin_csrf="));
  return match ? decodeURIComponent(match.split("=")[1] || "") : "";
}

async function api(path, options = {}) {
  const method = String(options.method || "GET").toUpperCase();
  const csrf = method === "GET" ? "" : csrfToken();
  const response = await fetch(`/admin/api${path}`, {
    ...options,
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(csrf ? { "X-CSRF-Token": csrf } : {}),
      ...(options.headers || {}),
    },
  });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || `Request failed: ${response.status}`);
  }
  if (response.status === 204) {
    return null;
  }
  return response.json();
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function table(headers, rows) {
  const safeHeaders = headers.map((header) => `<th>${escapeHTML(header)}</th>`).join("");
  const cell = (value) => {
    if (value && typeof value === "object" && value.safeHTML === true) {
      return value.html;
    }
    return escapeHTML(value);
  };
  const bodyRows = (rows || []).length
    ? rows
        .map((row) => `<tr>${row.map((value) => `<td>${cell(value)}</td>`).join("")}</tr>`)
        .join("")
    : `<tr><td colspan="${headers.length}" class="muted">暂无数据。</td></tr>`;
  return `<table><thead><tr>${safeHeaders}</tr></thead><tbody>${bodyRows}</tbody></table>`;
}

function formatNumber(value) {
  return Number(value || 0).toLocaleString();
}

function compactNumber(value) {
  const number = Number(value || 0);
  if (number >= 1_000_000) return `${(number / 1_000_000).toFixed(1)}M`;
  if (number >= 1_000) return `${(number / 1_000).toFixed(1)}K`;
  return formatNumber(number);
}

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
  } catch (error) {
    console.warn("failed to render admin chart", error);
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

function queryString(params) {
  const query = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && String(value).trim() !== "") {
      query.set(key, value);
    }
  });
  return query.toString();
}

function formatTime(value) {
  return String(value ?? "").replace(/(\d{2}:\d{2}:\d{2})\.\d+/, "$1");
}

function money(value) {
  if (value === null || value === undefined || value === "") {
    return "";
  }
  return `$${value}`;
}

function arrayValue(value) {
  return Array.isArray(value) ? value : [];
}

function badge(value) {
  const text = escapeHTML(value || "unknown");
  const severity = String(value || "unknown").toLowerCase();
  return safeHTML(`<span class="badge ${escapeHTML(severity)}">${text}</span>`);
}

function safeHTML(value) {
  return { safeHTML: true, html: value };
}

function truncate(value) {
  const text = String(value ?? "");
  return safeHTML(`<div class="cell-truncate">${escapeHTML(text)}</div>`);
}

function traceButton(traceID) {
  return safeHTML(`<button type="button" data-trace-id="${escapeHTML(traceID)}">${escapeHTML(traceID)}</button>`);
}

function rawEvidenceLink(traceID, objectType) {
  const safeTraceID = encodeURIComponent(traceID || "");
  const safeObjectType = encodeURIComponent(objectType || "");
  const href = `/admin/api/raw-evidence/${safeTraceID}/${safeObjectType}`;
  return `<a href="${href}" target="_blank" rel="noreferrer">${escapeHTML(objectType)}</a>`;
}

function page(title, content, action = "") {
  return `
    <div class="toolbar">
      <div>
        <h1>${escapeHTML(title)}</h1>
        <div class="muted">审计网关管理后台</div>
      </div>
      <div class="actions">${action}</div>
    </div>
    ${content}
  `;
}

function renderLogin() {
  destroyCharts();
  app.innerHTML = `
    <section class="login">
      <form id="login-form">
        <h1>审计网关管理后台</h1>
        <p class="muted">登录以查看网关活动。</p>
        <div class="field">
          <label for="username">用户名</label>
          <input id="username" name="username" autocomplete="username" required>
        </div>
        <div class="field">
          <label for="password">密码</label>
          <input id="password" name="password" type="password" autocomplete="current-password" required>
        </div>
        ${state.error ? `<div class="error">${escapeHTML(state.error)}</div>` : ""}
        <div class="field">
          <button class="primary" type="submit">登录</button>
        </div>
      </form>
    </section>
  `;

  document.querySelector("#login-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    state.error = "";
    const form = new FormData(event.currentTarget);
    try {
      const body = await api("/login", {
        method: "POST",
        body: JSON.stringify({
          username: form.get("username"),
          password: form.get("password"),
        }),
      });
      state.user = body.user;
      state.view = "overview";
      resetPasswordState();
      renderShell(`<section class="loading-panel">正在加载${escapeHTML(currentView().label)}...</section>`);
      await loadView();
    } catch (error) {
      state.error = error.message || "登录失败。";
      renderLogin();
    }
  });
}

function currentView() {
  if (state.view === "password") {
    return { id: "password", label: "修改密码" };
  }
  return views.find((view) => view.id === state.view) || views[0];
}

function renderShell(content) {
  destroyCharts();
  const user = state.user || {};
  app.innerHTML = `
    <div class="app-shell">
      <aside class="sidebar">
        <div class="brand">审计网关管理后台</div>
        <nav class="nav" aria-label="管理视图">
          ${views
            .map(
              (view) => `
                <button type="button" data-view="${escapeHTML(view.id)}" class="${view.id === state.view ? "active" : ""}">
                  ${escapeHTML(view.label)}
                </button>
              `,
            )
            .join("")}
        </nav>
        <div class="user-panel">
          <div>
            <strong>${escapeHTML(user.username || "admin")}</strong>
            <div class="muted">${escapeHTML(user.role || "unknown")}</div>
          </div>
          <div class="user-actions">
            <button type="button" id="change-password-button">修改密码</button>
            <button type="button" id="logout-button">退出登录</button>
          </div>
        </div>
      </aside>
      <section class="main">${content}</section>
    </div>
  `;

  document.querySelectorAll("[data-view]").forEach((button) => {
    button.addEventListener("click", async () => {
      state.view = button.dataset.view;
      state.error = "";
      resetPasswordState();
      renderShell(`<section class="loading-panel">正在加载${escapeHTML(currentView().label)}...</section>`);
      await loadView();
    });
  });

  document.querySelector("#change-password-button").addEventListener("click", () => {
    state.view = "password";
    state.error = "";
    resetPasswordState();
    renderPasswordChange();
  });

  document.querySelector("#logout-button").addEventListener("click", async () => {
    try {
      await api("/logout", { method: "POST" });
    } finally {
      state.user = null;
      state.error = "";
      state.view = "overview";
      resetPasswordState();
      renderLogin();
    }
  });

  let tooltip = null;
  const main = document.querySelector(".main");
  main.addEventListener("pointerenter", (e) => {
    const el = e.target.closest(".cell-truncate");
    if (!el) return;
    if (el.scrollWidth <= el.clientWidth) return;
    tooltip = document.createElement("div");
    tooltip.className = "cell-tooltip";
    tooltip.textContent = el.textContent;
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
  }, true);
  main.addEventListener("pointerleave", (e) => {
    if (!e.target.closest(".cell-truncate")) return;
    if (tooltip) { tooltip.remove(); tooltip = null; }
  }, true);
}

async function loadView() {
  try {
    if (state.view === "overview") {
      const body = await api("/overview");
      renderOverview(body);
    } else if (state.view === "usage") {
      await loadUsage();
    } else if (state.view === "traces") {
      const body = await api("/traces");
      renderTraces(body);
    } else if (state.view === "identities") {
      const body = await api("/token-identities");
      renderIdentities(body);
    } else if (state.view === "anomalies") {
      const body = await api("/anomalies");
      renderAnomalies(body);
    } else if (state.view === "coverage") {
      const body = await api("/coverage-alerts");
      renderCoverage(body);
    } else if (state.view === "lookup") {
      renderLookup();
    } else if (state.view === "context") {
      const body = await api("/context-catalog");
      renderContext(body);
    } else if (state.view === "reviews") {
      const body = await api("/review-decisions");
      renderReviews(body);
    } else if (state.view === "settings") {
      const body = await api("/settings");
      renderSettings(body);
    } else if (state.view === "password") {
      renderPasswordChange();
    } else if (state.view === "audit") {
      const body = await api("/audit-logs");
      renderAudit(body);
    }
  } catch (error) {
    renderShell(page(currentView().label, `<section class="panel error">${escapeHTML(error.message)}</section>`));
  }
}

async function loadUsage() {
  const requestSeq = ++usageRequestSeq;
  const username = String(state.usage.username || "").trim();
  if (!username) {
    if (requestSeq === usageRequestSeq) renderUsage({});
    return;
  }
  const params = queryString({
    username,
    range: state.usage.range || "30d",
    model: state.usage.model || "",
    bucket_size: "day",
  });
  let body;
  try {
    body = await api(`/usage?${params}`);
  } catch (error) {
    if (requestSeq !== usageRequestSeq) return;
    throw error;
  }
  if (requestSeq !== usageRequestSeq) return;
  renderUsage(body);
}

async function reloadUsageView() {
  renderShell(`<section class="loading-panel">正在加载用量...</section>`);
  try {
    await loadUsage();
  } catch (error) {
    renderShell(page("用量", `<section class="panel error">${escapeHTML(error.message || "加载用量失败。")}</section>`));
  }
}

function renderOverview(body) {
  body = body || {};
  const overview = body.overview || {};
  const metrics = [
    ["24h 请求数", overview.request_count_24h],
    ["24h Token 数", overview.total_tokens_24h],
    ["24h 错误数", overview.error_count_24h],
    ["未处理异常", overview.open_anomalies],
    ["未处理覆盖", overview.open_coverage_alerts],
    ["24h 仅原始数据", overview.raw_only_trace_count_24h],
  ];
  const cards = metrics
    .map(
      ([label, value]) => `
        <article class="metric">
          <div class="label">${escapeHTML(label)}</div>
          <div class="value">${formatNumber(value)}</div>
        </article>
      `,
    )
    .join("");
  renderShell(page("概览", `
    <div class="overview-layout">
      <section class="cards">${cards}</section>
      ${tokenUsageChart(overview.token_usage_daily)}
    </div>
  `));
  renderOverviewChart(overview.token_usage_daily);
}

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

function renderUsage(body = {}) {
  const trend = body.employee_usage || null;
  const summary = trend ? trend.summary || {} : {};
  const username = trend?.username || state.usage.username || "";
  const range = trend?.range || state.usage.range || "30d";
  const selectedModel = trend?.selected_model || state.usage.model || "";
  const models = arrayValue(trend?.models);
  const daily = arrayValue(trend?.daily);
  const modelSummary = arrayValue(trend?.model_summary);
  const ranges = ["1d", "7d", "30d"];
  const rangeTabs = ranges
    .map(
      (item) => `
        <button type="button" data-usage-range="${escapeHTML(item)}" class="${item === range ? "active" : ""}" aria-pressed="${item === range ? "true" : "false"}">
          ${escapeHTML(item)}
        </button>
      `,
    )
    .join("");
  const modelTabs = [
    `<button type="button" data-usage-model="" class="${selectedModel ? "" : "active"}" aria-pressed="${selectedModel ? "false" : "true"}">全部</button>`,
    ...models.map((model) => {
      const value = String(model || "");
      return `
        <button type="button" data-usage-model="${escapeHTML(value)}" class="${value === selectedModel ? "active" : ""}" aria-pressed="${value === selectedModel ? "true" : "false"}">
          ${escapeHTML(value || "unknown")}
        </button>
      `;
    }),
  ].join("");
  const searchPanel = `
    <section class="panel">
      <form id="usage-search" class="employee-search">
        <div class="field">
          <label for="usage-username">员工</label>
          <input id="usage-username" name="username" value="${escapeHTML(username)}" placeholder="输入员工用户名" autocomplete="off">
        </div>
        <button class="primary" type="submit">查询</button>
      </form>
    </section>
  `;

  if (!username || !trend) {
    renderShell(
      page(
        "用量",
        `
          ${searchPanel}
          <section class="panel empty-chart">选择员工后查看最近 30 天 token 与 model 使用趋势。</section>
        `,
      ),
    );
    bindUsageSearch();
    return;
  }

  const cards = [
    ["请求数", summary.request_count],
    ["Input", summary.prompt_tokens],
    ["Output", summary.completion_tokens],
    ["Cache", summary.cached_tokens],
    ["Total", summary.total_tokens],
  ]
    .map(
      ([label, value]) => `
        <article class="metric">
          <div class="label">${escapeHTML(label)}</div>
          <div class="value">${compactNumber(value)}</div>
        </article>
      `,
    )
    .join("");
  const rows = modelSummary.map((item) => [
    item.model || "unknown",
    formatNumber(item.request_count),
    formatNumber(item.prompt_tokens),
    formatNumber(item.completion_tokens),
    formatNumber(item.cached_tokens),
    formatNumber(item.total_tokens),
  ]);

  renderShell(
    page(
      "用量",
      `
        ${searchPanel}
        <section class="cards usage-summary">${cards}</section>
        <section class="panel usage-chart">
          <div class="chart-head">
            <div>
              <h2>${escapeHTML(username)} Token 使用趋势</h2>
              <div class="muted">按天汇总，默认展示最近 30 天</div>
            </div>
            <div class="range-tabs" aria-label="时间范围">${rangeTabs}</div>
          </div>
          <div class="model-tabs" aria-label="Model 筛选">${modelTabs}</div>
          ${usageChart(daily)}
        </section>
        <section class="panel">
          <h2>Model 汇总</h2>
          ${table(["Model", "请求数", "Input", "Output", "Cache", "Total"], rows)}
        </section>
      `,
    ),
  );
  bindUsageSearch();
  bindUsageControls();
  renderEmployeeUsageChart(daily);
}

function bindUsageSearch() {
  const form = document.querySelector("#usage-search");
  if (!form) return;
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const username = String(new FormData(event.currentTarget).get("username") || "").trim();
    state.usage.username = username;
    state.usage.model = "";
    await reloadUsageView();
  });
}

function bindUsageControls() {
  document.querySelectorAll("[data-usage-range]").forEach((button) => {
    button.addEventListener("click", async () => {
      state.usage.range = button.dataset.usageRange || "30d";
      await reloadUsageView();
    });
  });
  document.querySelectorAll("[data-usage-model]").forEach((button) => {
    button.addEventListener("click", async () => {
      state.usage.model = button.dataset.usageModel || "";
      await reloadUsageView();
    });
  });
}

function finiteNumber(value) {
  const number = Number(value || 0);
  return Number.isFinite(number) ? number : 0;
}

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

function renderTraces(body) {
  body = body || {};
  const rows = arrayValue(body.traces).map((trace) => [
    safeHTML(traceButton(trace.trace_id).html + (trace.needs_review ? badge("review").html : "")),
    formatTime(trace.created_at),
    trace.username || trace.fingerprint_display,
    trace.model_requested,
    trace.route_pattern || trace.path,
    trace.status_code,
    formatNumber(trace.usage_prompt_tokens),
    formatNumber(trace.usage_completion_tokens),
    formatNumber(trace.usage_cached_tokens),
    formatNumber(trace.usage_total_tokens),
  ]);
  renderShell(page("Trace", `<section class="panel">${table(["Trace", "时间 (UTC+8)", "员工", "Model", "Route", "Status", "Input", "Output", "Cached", "Total"], rows)}</section>`));
  document.querySelectorAll("[data-trace-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      try {
        const body = await api(`/traces/${encodeURIComponent(button.dataset.traceId)}`);
        renderTraceDetail(body);
      } catch (error) {
        renderShell(page("Trace", `<section class="panel error">${escapeHTML(error.message)}</section>`));
      }
    });
  });
}

function renderIdentities(body) {
  body = body || {};
  const rows = arrayValue(body.token_identities).map((item) => [
    item.username,
    item.display_name,
    item.department,
    item.fingerprint_display,
    item.new_api_token_id,
    item.token_name_raw,
    item.token_group,
    formatTime(item.last_seen_at),
  ]);
  renderShell(
    page(
      "员工目录",
      `<section class="panel">${table(["员工", "名称", "部门", "Fingerprint", "Token ID", "Token Name", "分组", "最后活跃 (UTC+8)"], rows)}</section>`,
    ),
  );
}

function renderTraceDetail(body) {
  body = body || {};
  const trace = body.trace || {};
  const evidenceLinks = ["request_body", "response_body"]
    .map((type) => {
      const refName = type === "request_body" ? trace.request_raw_ref : trace.response_raw_ref;
      if (!refName) {
        return "";
      }
      return rawEvidenceLink(trace.trace_id, type);
    })
    .filter(Boolean)
    .join(" ");
  const meta = [
    ["Trace", trace.trace_id],
    ["员工", trace.username || trace.fingerprint_display],
    ["Model", trace.model_requested],
    ["Route", trace.route_pattern || trace.path],
    ["Status", trace.status_code],
    ["Input Token", formatNumber(trace.usage_prompt_tokens)],
    ["Output Token", formatNumber(trace.usage_completion_tokens)],
    ["Cached Token", formatNumber(trace.usage_cached_tokens)],
    ["Total Token", formatNumber(trace.usage_total_tokens)],
    ["身份", trace.identity_resolution_status],
    ["分析", trace.analysis_status],
    ["原始证据", evidenceLinks || "无"],
  ]
    .map(([label, value]) => `<div class="meta-item"><span>${escapeHTML(label)}</span>${label === "原始证据" ? value : escapeHTML(value)}</div>`)
    .join("");
  const messages = arrayValue(trace.normalized_messages).map((item) => [
    item.sequence_index,
    item.direction,
    item.role,
    item.modality,
    item.protocol_item_type,
    truncate(item.content_text || item.media_url),
    formatNumber(item.token_count_estimate),
  ]);
  const analysis = arrayValue(trace.analysis_results).map((item) => [
    item.analyzer_name,
    item.category,
    item.label,
    item.score,
    item.confidence,
    badge(item.severity),
    formatTime(item.created_at),
  ]);
  const anomalies = arrayValue(trace.anomalies).map((item) => [
    item.anomaly_id,
    formatTime(item.created_at),
    badge(item.severity),
    item.anomaly_type,
    item.status,
    item.username || item.fingerprint_display,
    item.reason,
  ]);
  renderShell(
    page(
      "Trace 详情",
      `
        <section class="panel"><div class="meta-grid">${meta}</div></section>
        <section class="panel"><h2>归一化消息</h2>${table(["序号", "方向", "角色", "模态", "类型", "内容", "Token"], messages)}</section>
        <section class="panel"><h2>分析结果</h2>${table(["分析器", "分类", "标签", "分数", "置信度", "Severity", "时间 (UTC+8)"], analysis)}</section>
        <section class="panel"><h2>关联异常</h2>${table(["ID", "时间 (UTC+8)", "Severity", "类型", "状态", "员工", "原因"], anomalies)}</section>
      `,
      `<button type="button" id="back-to-traces">返回</button>`,
    ),
  );
  document.querySelector("#back-to-traces").addEventListener("click", async () => {
    state.view = "traces";
    await loadView();
  });
}

function renderAnomalies(body) {
  body = body || {};
  const rows = arrayValue(body.anomalies).map((item) => [
    item.anomaly_id,
    formatTime(item.created_at),
    badge(item.severity),
    item.anomaly_type,
    item.username || item.fingerprint_display,
    item.observed_value,
    item.reason,
  ]);
  renderShell(page("异常", `<section class="panel">${table(["ID", "时间 (UTC+8)", "Severity", "类型", "员工", "观测值", "原因"], rows)}</section>`));
}

function renderCoverage(body) {
  body = body || {};
  const rows = arrayValue(body.coverage_alerts).map((item) => [
    item.alert_id,
    formatTime(item.last_seen_at),
    badge(item.severity),
    item.alert_code,
    item.method,
    item.route_pattern || item.raw_path,
    formatNumber(item.occurrence_count),
  ]);
  renderShell(page("覆盖", `<section class="panel">${table(["ID", "最后发现 (UTC+8)", "Severity", "Code", "Method", "Route", "数量"], rows)}</section>`));
}

function renderLookup(result = "") {
  renderShell(
    page(
      "API Key 查询",
      `
        <section class="panel">
          <form id="lookup-form" class="filters">
            <div class="field">
              <label for="api_key">API Key</label>
              <input id="api_key" name="api_key" type="password" autocomplete="off" required>
            </div>
            <button class="primary" type="submit">查询</button>
          </form>
        </section>
        ${result}
      `,
    ),
  );
  document.querySelector("#lookup-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const apiKeyInput = event.currentTarget.elements.api_key;
    try {
      const apiKey = apiKeyInput.value;
      apiKeyInput.value = "";
      const body = await api("/api-key-lookup", {
        method: "POST",
        body: JSON.stringify({ api_key: apiKey }),
      });
      const lookup = body.lookup || {};
      renderLookup(`
        <section class="panel">
          <h2>查询结果</h2>
          <div class="meta-grid">
            <div class="meta-item"><span>Fingerprint</span>${escapeHTML(lookup.fingerprint_display)}</div>
            <div class="meta-item"><span>员工</span>${escapeHTML(lookup.username || lookup.token_name)}</div>
            <div class="meta-item"><span>未处理异常</span>${escapeHTML(formatNumber(lookup.open_anomaly_count))}</div>
          </div>
        </section>
      `);
    } catch (error) {
      renderLookup(`<section class="panel error">${escapeHTML(error.message)}</section>`);
    }
  });
}

function renderContext(body, message = "") {
  body = body || {};
  const rows = arrayValue(body.context_catalog).map((item) => [
    item.context_type,
    item.name,
    item.owner,
    arrayValue(item.keywords).join(", "),
    item.expected_usage_level,
    badge(item.active ? "active" : "inactive"),
    formatTime(item.created_at),
    formatTime(item.updated_at),
  ]);
  renderShell(
    page(
      "Context 目录",
      `
        <section class="panel">${table(["类型", "名称", "负责人", "关键词", "使用级别", "状态", "创建时间 (UTC+8)", "更新时间 (UTC+8)"], rows)}</section>
        ${message}
        <section class="panel">
          <h2>创建 Context</h2>
          <form id="context-form" class="filters">
            <div class="field">
              <label for="context_type">类型</label>
              <select id="context_type" name="context_type">
                <option value="repo">repo</option>
                <option value="service">service</option>
                <option value="team">team</option>
                <option value="project">project</option>
              </select>
            </div>
            <div class="field">
              <label for="name">名称</label>
              <input id="name" name="name" required>
            </div>
            <div class="field">
              <label for="owner">负责人</label>
              <input id="owner" name="owner">
            </div>
            <div class="field">
              <label for="expected_usage_level">使用级别</label>
              <select id="expected_usage_level" name="expected_usage_level">
                <option value="low">low</option>
                <option value="medium">medium</option>
                <option value="high">high</option>
              </select>
            </div>
            <div class="field">
              <label for="keywords">关键词</label>
              <input id="keywords" name="keywords" placeholder="gateway, audit, admin">
            </div>
            <div class="field">
              <label for="description">描述</label>
              <textarea id="description" name="description"></textarea>
            </div>
            <button class="primary" type="submit">创建</button>
          </form>
        </section>
      `,
    ),
  );
  document.querySelector("#context-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const keywords = String(form.get("keywords") || "")
      .split(",")
      .map((keyword) => keyword.trim())
      .filter(Boolean);
    try {
      await api("/context-catalog", {
        method: "POST",
        body: JSON.stringify({
          context_type: form.get("context_type"),
          name: form.get("name"),
          description: form.get("description"),
          keywords,
          aliases: [],
          owner: form.get("owner"),
          expected_task_categories: [],
          expected_models: [],
          expected_usage_level: form.get("expected_usage_level"),
          active: true,
        }),
      });
      const refreshed = await api("/context-catalog");
      renderContext(refreshed);
    } catch (error) {
      renderContext(body, `<section class="panel error">${escapeHTML(error.message)}</section>`);
    }
  });
}

function renderReviews(body) {
  body = body || {};
  const rows = arrayValue(body.review_decisions).map((item) => [
    formatTime(item.created_at),
    item.target_type,
    item.target_id,
    badge(item.decision),
    item.reviewer_username,
    item.note,
  ]);
  renderShell(page("审核记录", `<section class="panel">${table(["时间 (UTC+8)", "目标类型", "目标", "决定", "审核人", "备注"], rows)}</section>`));
}

function renderSettings(body) {
  body = body || {};
  const settings = body.settings || {};
  const rows = [
    ["员工匹配规则", settings.username_pattern],
    ["指标已启用", settings.metrics_enabled ? "true" : "false"],
    ["API Key 查询限额", settings.lookup_limit],
    ["原始证据访问限额", settings.raw_access_limit],
  ];
  renderShell(page("系统设置", `<section class="panel settings-list">${table(["设置项", "值"], rows)}</section>`));
}

function renderPasswordChange() {
  renderShell(
    page(
      "修改密码",
      `
        <section class="panel password-panel">
          <form id="password-form" class="stacked-form">
            <div class="field">
              <label for="current_password">当前密码</label>
              <input id="current_password" name="current_password" type="password" autocomplete="current-password" required>
            </div>
            <div class="field">
              <label for="new_password">新密码</label>
              <input id="new_password" name="new_password" type="password" autocomplete="new-password" minlength="12" required>
            </div>
            <div class="field">
              <label for="confirm_password">确认新密码</label>
              <input id="confirm_password" name="confirm_password" type="password" autocomplete="new-password" minlength="12" required>
            </div>
            ${state.password.error ? `<div class="error">${escapeHTML(state.password.error)}</div>` : ""}
            ${state.password.success ? `<div class="success">${escapeHTML(state.password.success)}</div>` : ""}
            <div class="field">
              <button class="primary" type="submit">更新密码</button>
            </div>
          </form>
        </section>
      `,
    ),
  );

  document.querySelector("#password-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const currentPassword = String(form.get("current_password") || "");
    const newPassword = String(form.get("new_password") || "");
    const confirmPassword = String(form.get("confirm_password") || "");

    state.password.error = "";
    state.password.success = "";
    if (!currentPassword || !newPassword || !confirmPassword) {
      state.password.error = "请填写所有密码字段。";
      renderPasswordChange();
      return;
    }
    if (passwordLength(newPassword) < 12) {
      state.password.error = "新密码至少需要 12 位。";
      renderPasswordChange();
      return;
    }
    if (newPassword !== confirmPassword) {
      state.password.error = "两次输入的新密码不一致。";
      renderPasswordChange();
      return;
    }

    try {
      await api("/me/password", {
        method: "POST",
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
          confirm_password: confirmPassword,
        }),
      });
      formElement.reset();
      state.password.success = "密码已更新，其他会话已退出。";
      renderPasswordChange();
    } catch (error) {
      state.password.error = error.message || "修改密码失败。";
      renderPasswordChange();
    }
  });
}

function renderAudit(body) {
  body = body || {};
  const rows = arrayValue(body.audit_logs).map((item) => [
    formatTime(item.created_at),
    item.actor_username,
    item.action,
    item.target_type,
    item.target_id,
    item.trace_id,
  ]);
  renderShell(page("审计日志", `<section class="panel">${table(["时间 (UTC+8)", "操作人", "操作", "目标类型", "目标", "Trace"], rows)}</section>`));
}

async function boot() {
  try {
    const body = await api("/me");
    state.user = body.user;
    renderShell(`<section class="loading-panel">Loading ${escapeHTML(currentView().label)}...</section>`);
    await loadView();
  } catch (error) {
    state.user = null;
    state.error = "";
    state.view = "overview";
    resetPasswordState();
    renderLogin();
  }
}

boot();
