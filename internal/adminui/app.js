const app = document.querySelector("#app");

const state = { user: null, view: "overview", error: "" };

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
      renderShell(`<section class="loading-panel">正在加载${escapeHTML(currentView().label)}...</section>`);
      await loadView();
    } catch (error) {
      state.error = error.message || "登录失败。";
      renderLogin();
    }
  });
}

function currentView() {
  return views.find((view) => view.id === state.view) || views[0];
}

function renderShell(content) {
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
          <button type="button" id="logout-button">退出登录</button>
        </div>
      </aside>
      <section class="main">${content}</section>
    </div>
  `;

  document.querySelectorAll("[data-view]").forEach((button) => {
    button.addEventListener("click", async () => {
      state.view = button.dataset.view;
      state.error = "";
      renderShell(`<section class="loading-panel">正在加载${escapeHTML(currentView().label)}...</section>`);
      await loadView();
    });
  });

  document.querySelector("#logout-button").addEventListener("click", async () => {
    try {
      await api("/logout", { method: "POST" });
    } finally {
      state.user = null;
      state.error = "";
      renderLogin();
    }
  });
}

async function loadView() {
  try {
    if (state.view === "overview") {
      const body = await api("/overview");
      renderOverview(body);
    } else if (state.view === "usage") {
      const body = await api("/usage?bucket_size=hour");
      renderUsage(body);
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
    } else if (state.view === "audit") {
      const body = await api("/audit-logs");
      renderAudit(body);
    }
  } catch (error) {
    renderShell(page(currentView().label, `<section class="panel error">${escapeHTML(error.message)}</section>`));
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
  renderShell(page("概览", `<section class="cards">${cards}</section>`));
}

function renderUsage(body) {
  body = body || {};
  const rows = arrayValue(body.usage).map((item) => [
    item.bucket_start,
    item.username || item.fingerprint_display,
    item.model,
    item.route_pattern,
    formatNumber(item.request_count),
    formatNumber(item.total_tokens),
    money(item.estimated_cost),
  ]);
  renderShell(page("用量", `<section class="panel">${table(["时间段", "员工", "Model", "Route", "请求数", "Token", "费用"], rows)}</section>`));
}

function renderTraces(body) {
  body = body || {};
  const rows = arrayValue(body.traces).map((trace) => [
    traceButton(trace.trace_id),
    trace.username || trace.fingerprint_display,
    trace.model_requested,
    trace.route_pattern || trace.path,
    trace.status_code,
    formatNumber(trace.usage_total_tokens),
  ]);
  renderShell(page("Trace", `<section class="panel">${table(["Trace", "员工", "Model", "Route", "Status", "Token"], rows)}</section>`));
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
    item.last_seen_at,
  ]);
  renderShell(
    page(
      "员工目录",
      `<section class="panel">${table(["员工", "名称", "部门", "Fingerprint", "Token ID", "Token Name", "分组", "最后活跃"], rows)}</section>`,
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
    ["Token", formatNumber(trace.usage_total_tokens)],
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
    item.content_text || item.media_url,
    formatNumber(item.token_count_estimate),
  ]);
  const analysis = arrayValue(trace.analysis_results).map((item) => [
    item.analyzer_name,
    item.category,
    item.label,
    item.score,
    item.confidence,
    badge(item.severity),
  ]);
  renderShell(
    page(
      "Trace 详情",
      `
        <section class="panel"><div class="meta-grid">${meta}</div></section>
        <section class="panel"><h2>归一化消息</h2>${table(["序号", "方向", "角色", "模态", "类型", "内容", "Token"], messages)}</section>
        <section class="panel"><h2>分析结果</h2>${table(["分析器", "分类", "标签", "分数", "置信度", "Severity"], analysis)}</section>
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
    badge(item.severity),
    item.anomaly_type,
    item.username || item.fingerprint_display,
    item.observed_value,
    item.reason,
  ]);
  renderShell(page("异常", `<section class="panel">${table(["ID", "Severity", "类型", "员工", "观测值", "原因"], rows)}</section>`));
}

function renderCoverage(body) {
  body = body || {};
  const rows = arrayValue(body.coverage_alerts).map((item) => [
    item.alert_id,
    badge(item.severity),
    item.alert_code,
    item.method,
    item.route_pattern || item.raw_path,
    formatNumber(item.occurrence_count),
  ]);
  renderShell(page("覆盖", `<section class="panel">${table(["ID", "Severity", "Code", "Method", "Route", "数量"], rows)}</section>`));
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
  ]);
  renderShell(
    page(
      "Context 目录",
      `
        <section class="panel">${table(["类型", "名称", "负责人", "关键词", "使用级别", "状态"], rows)}</section>
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
    item.created_at,
    item.target_type,
    item.target_id,
    badge(item.decision),
    item.reviewer_username,
    item.note,
  ]);
  renderShell(page("审核记录", `<section class="panel">${table(["时间", "目标类型", "目标", "决定", "审核人", "备注"], rows)}</section>`));
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

function renderAudit(body) {
  body = body || {};
  const rows = arrayValue(body.audit_logs).map((item) => [
    item.created_at,
    item.actor_username,
    item.action,
    item.target_type,
    item.target_id,
    item.trace_id,
  ]);
  renderShell(page("审计日志", `<section class="panel">${table(["时间", "操作人", "操作", "目标类型", "目标", "Trace"], rows)}</section>`));
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
    renderLogin();
  }
}

boot();
