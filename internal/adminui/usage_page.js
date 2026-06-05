(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
  }
  root.UsagePage = api;
})(typeof globalThis !== "undefined" ? globalThis : window, function () {
  function escapeHTML(value) {
    return String(value ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#39;");
  }

  function compactNumber(value) {
    return Number(value || 0).toLocaleString();
  }

  function formatActiveBucketHint(range, activeCount, expectedCount) {
    if (!expectedCount || activeCount >= expectedCount) return "";
    return `当前范围内仅 ${activeCount} 个时间桶有实际流量`;
  }

  function renderSearchResults(results, selectedEmployee) {
    if (!Array.isArray(results) || results.length === 0) return "";
    return `
      <div class="usage-search-results">
        <div class="label">搜索建议</div>
        ${results.map((item) => `
          <button type="button" data-usage-select="${escapeHTML(item.username)}" class="${item.username === selectedEmployee ? "active" : ""}">
            <strong>${escapeHTML(item.display_name || item.username)}</strong>
            <span>${escapeHTML(item.username)}</span>
            <span>${escapeHTML(item.department || "")}</span>
          </button>
        `).join("")}
      </div>
    `;
  }

  function renderTopEmployees(items) {
    const rows = Array.isArray(items) ? items : [];
    if (rows.length === 0) {
      return `<div class="muted">最近 30 天暂无员工用量数据。</div>`;
    }
    return `
      <div class="usage-top-employees">
        ${rows.map((item) => `
          <button type="button" class="usage-top-employee" data-usage-top-employee="${escapeHTML(item.username)}">
            <strong>${escapeHTML(item.display_name || item.username)}</strong>
            <span>${escapeHTML(item.username)}</span>
            <span>${compactNumber(item.total_tokens)} tokens</span>
          </button>
        `).join("")}
      </div>
    `;
  }

  function renderTopModels(items) {
    const rows = Array.isArray(items) ? items : [];
    if (rows.length === 0) {
      return `<div class="muted">最近 30 天暂无模型用量数据。</div>`;
    }
    return `
      <table>
        <thead><tr><th>Model</th><th>请求数</th><th>Total</th></tr></thead>
        <tbody>
          ${rows.map((item) => `
            <tr>
              <td>${escapeHTML(item.model || "unknown")}</td>
              <td>${compactNumber(item.request_count)}</td>
              <td>${compactNumber(item.total_tokens)}</td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    `;
  }

  function renderUsagePage(payload) {
    const globalUsage = payload.global_usage || {};
    const employeeUsage = payload.employee_usage || null;
    const usageState = payload.usageState || {};
    const hint = employeeUsage
      ? formatActiveBucketHint(employeeUsage.range, employeeUsage.active_bucket_count, employeeUsage.expected_bucket_count)
      : "";
    return `
      <section class="panel">
        <div class="field">
          <label for="usage-search-input">搜索员工</label>
          <input id="usage-search-input" data-usage-search-input value="${escapeHTML(usageState.searchQuery || "")}" placeholder="输入用户名或显示名">
        </div>
        ${usageState.searchError ? `<div class="error-inline">${escapeHTML(usageState.searchError)}</div>` : ""}
        ${renderSearchResults(usageState.searchResults || [], usageState.selectedEmployee || "")}
      </section>
      <section class="cards usage-summary">
        <article class="metric"><div class="label">30d 总 Token</div><div class="value">${compactNumber(globalUsage.total_tokens)}</div></article>
        <article class="metric"><div class="label">活跃员工</div><div class="value">${compactNumber(globalUsage.active_employees)}</div></article>
        <article class="metric"><div class="label">请求数</div><div class="value">${compactNumber(globalUsage.request_count)}</div></article>
        <article class="metric"><div class="label">活跃模型</div><div class="value">${compactNumber(globalUsage.active_models)}</div></article>
      </section>
      <section class="panel"><h2>Top 员工榜</h2>${renderTopEmployees(globalUsage.top_employees)}</section>
      <section class="panel"><h2>Top Models</h2>${renderTopModels(globalUsage.top_models)}</section>
      ${employeeUsage ? `<section class="panel usage-detail"><div class="detail-head"><h2>当前查看：${escapeHTML(employeeUsage.username)}</h2><button type="button" data-usage-clear>收起详情</button></div>${hint ? `<div class="muted">${escapeHTML(hint)}</div>` : ""}</section>` : ""}
    `;
  }

  return { renderUsagePage, formatActiveBucketHint };
});
