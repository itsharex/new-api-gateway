const app = document.querySelector("#app");

const state = { user: null, view: "overview", error: "" };

const views = [
  { id: "overview", label: "Overview" },
  { id: "usage", label: "Usage" },
  { id: "traces", label: "Traces" },
  { id: "anomalies", label: "Anomalies" },
  { id: "coverage", label: "Coverage" },
  { id: "lookup", label: "API Key Lookup" },
  { id: "context", label: "Context Catalog" },
  { id: "audit", label: "Audit Logs" },
];

async function api(path, options = {}) {
  const response = await fetch(`/admin/api${path}`, {
    ...options,
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
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
    if (value && typeof value === "object" && value.html !== undefined) {
      return value.html;
    }
    return escapeHTML(value);
  };
  const bodyRows = (rows || []).length
    ? rows
        .map((row) => `<tr>${row.map((value) => `<td>${cell(value)}</td>`).join("")}</tr>`)
        .join("")
    : `<tr><td colspan="${headers.length}" class="muted">No rows found.</td></tr>`;
  return `<table><thead><tr>${safeHeaders}</tr></thead><tbody>${bodyRows}</tbody></table>`;
}

function formatNumber(value) {
  return Number(value || 0).toLocaleString();
}

function money(value) {
  if (value === null || value === undefined || value === "") {
    return "";
  }
  return `$${escapeHTML(value)}`;
}

function arrayValue(value) {
  return Array.isArray(value) ? value : [];
}

function badge(value) {
  const text = escapeHTML(value || "unknown");
  const severity = String(value || "unknown").toLowerCase();
  return { html: `<span class="badge ${escapeHTML(severity)}">${text}</span>` };
}

function html(value) {
  return { html: value };
}

function page(title, content, action = "") {
  return `
    <div class="toolbar">
      <div>
        <h1>${escapeHTML(title)}</h1>
        <div class="muted">Audit Gateway Admin</div>
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
        <h1>Audit Gateway Admin</h1>
        <p class="muted">Sign in to review gateway activity.</p>
        <div class="field">
          <label for="username">Username</label>
          <input id="username" name="username" autocomplete="username" required>
        </div>
        <div class="field">
          <label for="password">Password</label>
          <input id="password" name="password" type="password" autocomplete="current-password" required>
        </div>
        ${state.error ? `<div class="error">${escapeHTML(state.error)}</div>` : ""}
        <div class="field">
          <button class="primary" type="submit">Sign in</button>
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
      renderShell(`<section class="loading-panel">Loading ${escapeHTML(currentView().label)}...</section>`);
      await loadView();
    } catch (error) {
      state.error = error.message || "Login failed.";
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
        <div class="brand">Audit Gateway Admin</div>
        <nav class="nav" aria-label="Admin views">
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
          <button type="button" id="logout-button">Logout</button>
        </div>
      </aside>
      <section class="main">${content}</section>
    </div>
  `;

  document.querySelectorAll("[data-view]").forEach((button) => {
    button.addEventListener("click", async () => {
      state.view = button.dataset.view;
      state.error = "";
      renderShell(`<section class="loading-panel">Loading ${escapeHTML(currentView().label)}...</section>`);
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
    ["Requests 24h", overview.request_count_24h],
    ["Tokens 24h", overview.total_tokens_24h],
    ["Errors 24h", overview.error_count_24h],
    ["Open Anomalies", overview.open_anomalies],
    ["Open Coverage", overview.open_coverage_alerts],
    ["Raw Only 24h", overview.raw_only_trace_count_24h],
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
  renderShell(page("Overview", `<section class="cards">${cards}</section>`));
}

function renderUsage(body) {
  body = body || {};
  const rows = arrayValue(body.usage).map((item) => [
    item.bucket_start,
    item.employee_no || item.fingerprint_display,
    item.model,
    item.route_pattern,
    formatNumber(item.request_count),
    formatNumber(item.total_tokens),
    html(money(item.estimated_cost)),
  ]);
  renderShell(page("Usage", `<section class="panel">${table(["Bucket", "Employee", "Model", "Route", "Requests", "Tokens", "Cost"], rows)}</section>`));
}

function renderTraces(body) {
  body = body || {};
  const rows = arrayValue(body.traces).map((trace) => [
    html(`<button type="button" data-trace-id="${escapeHTML(trace.trace_id)}">${escapeHTML(trace.trace_id)}</button>`),
    trace.employee_no || trace.fingerprint_display,
    trace.model_requested,
    trace.route_pattern || trace.path,
    trace.status_code,
    formatNumber(trace.usage_total_tokens),
  ]);
  renderShell(page("Traces", `<section class="panel">${table(["Trace", "Employee", "Model", "Route", "Status", "Tokens"], rows)}</section>`));
  document.querySelectorAll("[data-trace-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      const body = await api(`/traces/${encodeURIComponent(button.dataset.traceId)}`);
      renderTraceDetail(body);
    });
  });
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
      const href = `/admin/api/raw-evidence/${encodeURIComponent(trace.trace_id)}/${type}`;
      return `<a href="${href}" target="_blank" rel="noreferrer">${escapeHTML(type)}</a>`;
    })
    .filter(Boolean)
    .join(" ");
  const meta = [
    ["Trace", trace.trace_id],
    ["Employee", trace.employee_no || trace.fingerprint_display],
    ["Model", trace.model_requested],
    ["Route", trace.route_pattern || trace.path],
    ["Status", trace.status_code],
    ["Tokens", formatNumber(trace.usage_total_tokens)],
    ["Identity", trace.identity_resolution_status],
    ["Analysis", trace.analysis_status],
    ["Raw Evidence", evidenceLinks || "None"],
  ]
    .map(([label, value]) => `<div class="meta-item"><span>${escapeHTML(label)}</span>${label === "Raw Evidence" ? value : escapeHTML(value)}</div>`)
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
      "Trace Detail",
      `
        <section class="panel"><div class="meta-grid">${meta}</div></section>
        <section class="panel"><h2>Normalized Messages</h2>${table(["Index", "Direction", "Role", "Modality", "Type", "Content", "Tokens"], messages)}</section>
        <section class="panel"><h2>Analysis</h2>${table(["Analyzer", "Category", "Label", "Score", "Confidence", "Severity"], analysis)}</section>
      `,
      `<button type="button" id="back-to-traces">Back</button>`,
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
    item.employee_no || item.fingerprint_display,
    item.observed_value,
    item.reason,
  ]);
  renderShell(page("Anomalies", `<section class="panel">${table(["ID", "Severity", "Type", "Employee", "Observed", "Reason"], rows)}</section>`));
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
  renderShell(page("Coverage", `<section class="panel">${table(["ID", "Severity", "Code", "Method", "Route", "Count"], rows)}</section>`));
}

function renderLookup(result = "") {
  renderShell(
    page(
      "API Key Lookup",
      `
        <section class="panel">
          <form id="lookup-form" class="filters">
            <div class="field">
              <label for="api_key">API Key</label>
              <input id="api_key" name="api_key" type="password" autocomplete="off" required>
            </div>
            <button class="primary" type="submit">Lookup</button>
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
      const body = await api("/api-key-lookup", {
        method: "POST",
        body: JSON.stringify({ api_key: apiKeyInput.value }),
      });
      apiKeyInput.value = "";
      const lookup = body.lookup || {};
      renderLookup(`
        <section class="panel">
          <h2>Lookup Result</h2>
          <div class="meta-grid">
            <div class="meta-item"><span>Fingerprint</span>${escapeHTML(lookup.fingerprint_display)}</div>
            <div class="meta-item"><span>Employee</span>${escapeHTML(lookup.employee_no || lookup.token_name)}</div>
            <div class="meta-item"><span>Open Anomalies</span>${escapeHTML(formatNumber(lookup.open_anomaly_count))}</div>
          </div>
        </section>
      `);
    } catch (error) {
      apiKeyInput.value = "";
      renderLookup(`<section class="panel error">${escapeHTML(error.message)}</section>`);
    }
  });
}

function renderContext(body) {
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
      "Context Catalog",
      `
        <section class="panel">${table(["Type", "Name", "Owner", "Keywords", "Usage", "Status"], rows)}</section>
        <section class="panel">
          <h2>Create Context</h2>
          <form id="context-form" class="filters">
            <div class="field">
              <label for="context_type">Type</label>
              <select id="context_type" name="context_type">
                <option value="repo">repo</option>
                <option value="service">service</option>
                <option value="team">team</option>
                <option value="project">project</option>
              </select>
            </div>
            <div class="field">
              <label for="name">Name</label>
              <input id="name" name="name" required>
            </div>
            <div class="field">
              <label for="owner">Owner</label>
              <input id="owner" name="owner">
            </div>
            <div class="field">
              <label for="expected_usage_level">Usage Level</label>
              <select id="expected_usage_level" name="expected_usage_level">
                <option value="low">low</option>
                <option value="medium">medium</option>
                <option value="high">high</option>
              </select>
            </div>
            <div class="field">
              <label for="keywords">Keywords</label>
              <input id="keywords" name="keywords" placeholder="gateway, audit, admin">
            </div>
            <div class="field">
              <label for="description">Description</label>
              <textarea id="description" name="description"></textarea>
            </div>
            <button class="primary" type="submit">Create</button>
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
  });
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
  renderShell(page("Audit Logs", `<section class="panel">${table(["Time", "Actor", "Action", "Target Type", "Target", "Trace"], rows)}</section>`));
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
