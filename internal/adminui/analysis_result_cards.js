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
    badge:
      payload.needs_review || String(item.severity || "").toLowerCase() === "review"
        ? "review"
        : "",
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
  const available =
    finiteNumber(payload.total_tokens ?? item.score) > 0 && finiteNumber(item.confidence) > 0;
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
    meta: [available ? "usage available" : "usage missing", formatTime(item.created_at)],
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
    meta: [item.category || "unknown", formatTime(item.created_at)],
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
