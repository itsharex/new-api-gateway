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

function pickFirst(...values) {
  for (const value of values) {
    if (value !== undefined && value !== null && value !== "") {
      return value;
    }
  }
  return "";
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

function formatDecision(value) {
  switch (String(value || "").toLowerCase()) {
    case "work_related":
      return "工作相关";
    case "non_work_related":
      return "非工作相关";
    case "unknown":
      return "未知";
    default:
      return value ? String(value) : "未知";
  }
}

function formatAction(value) {
  switch (String(value || "").toLowerCase()) {
    case "record_only":
      return "仅记录";
    case "allow":
      return "允许";
    case "block":
      return "阻止";
    case "review":
      return "人工复核";
    default:
      return value ? String(value) : "未知";
  }
}

function formatCategory(value) {
  switch (String(value || "").toLowerCase()) {
    case "coding":
      return "编码";
    case "software_development":
      return "软件开发";
    case "debugging":
      return "调试";
    default:
      return value ? String(value) : "未知";
  }
}

function formatSubtitleCategory(value) {
  switch (String(value || "").toLowerCase()) {
    case "coding":
      return "编码相关";
    case "software_development":
      return "软件开发";
    case "debugging":
      return "调试";
    default:
      return formatCategory(value);
  }
}

function formatConfidenceLabel(value) {
  switch (String(value || "").toLowerCase()) {
    case "high":
      return "高";
    case "medium":
      return "中";
    case "low":
      return "低";
    default:
      return value ? String(value) : "未知";
  }
}

function resolveWorkRelevanceIdentity(item) {
  const stage = String(item?.stage || "").toLowerCase();
  const producer = String(item?.producer || "").toLowerCase();
  const resultKey = String(item?.result_key || "").toLowerCase();

  if (
    stage === "core" &&
    producer === "heuristic_work_relevance" &&
    resultKey === "work_relevance_primary"
  ) {
    return {
      identity: "primary",
      title: "初步判断",
      marker: "",
      emphasis: "normal",
      sortOrder: 0,
    };
  }

  if (
    stage === "enrichment" &&
    producer === "llm_judge" &&
    resultKey === "work_relevance_secondary"
  ) {
    return {
      identity: "secondary",
      title: "复核判断",
      marker: "最终参考",
      emphasis: "strong",
      sortOrder: 1,
    };
  }

  return {
    identity: "generic",
    title: "工作相关性判断",
    marker: "",
    emphasis: "normal",
    sortOrder: 3,
  };
}

function buildWorkRelevanceCard(item, payload) {
  const breakdown = payload.score_breakdown || {};
  const identity = resolveWorkRelevanceIdentity(item);
  const category = pickFirst(payload.task_category, item.label, "unknown");
  const decision = pickFirst(payload.decision, item.label, "unknown");
  const action = pickFirst(payload.recommended_action, item.action, "record_only");
  const confidenceValue = pickFirst(payload.confidence, item.confidence, 0);
  const confidenceLabel = pickFirst(
    payload.confidence_label,
    payload.confidence_level,
    item.confidence_label,
    "unknown"
  );
  const severity = String(item.severity || "").toLowerCase();
  return {
    variant: "work_relevance",
    analyzerName: item.analyzer_name || "work_relevance",
    badge: payload.needs_review || severity === "review" ? "review" : severity,
    title: identity.title,
    subtitle: `${formatDecision(decision)} · ${formatSubtitleCategory(category)}`,
    marker: identity.marker,
    emphasis: identity.emphasis,
    summaryItems: [
      `建议：${formatAction(action)}`,
      `置信度：${formatConfidenceLabel(confidenceLabel)}（${formatScore(confidenceValue)}）`,
    ],
    detailItems: [
      `类别：${formatCategory(category)}`,
      `工作分：${formatScore(breakdown.work ?? payload.work_related_score ?? item.score)}`,
      `非工作分：${formatScore(breakdown.non_work ?? payload.personal_use_score)}`,
      `风险分：${formatScore(breakdown.risk ?? 0)}`,
    ],
    meta: [formatTime(item.created_at)],
    sortOrder: identity.sortOrder,
    detailsJSON: JSON.stringify(payload, null, 2),
  };
}

function buildUsageExtractionCard(item, payload) {
  const totalTokens = payload.total_tokens ?? item.score ?? 0;
  return {
    variant: "usage_extraction",
    analyzerName: item.analyzer_name || "usage_extraction",
    badge: String(item.severity || "").toLowerCase(),
    title: "用量信息",
    subtitle: `${formatCount(totalTokens)} 总 tokens`,
    marker: "",
    emphasis: "normal",
    summaryItems: [
      `输入 ${formatCount(payload.prompt_tokens)}`,
      `输出 ${formatCount(payload.completion_tokens)}`,
      `缓存 ${formatCount(payload.cached_tokens)}`,
      `推理 ${formatCount(payload.reasoning_tokens)}`,
    ],
    detailItems: [],
    meta: [`总量 ${formatCount(totalTokens)}`, formatTime(item.created_at)],
    sortOrder: 2,
    detailsJSON: JSON.stringify(payload, null, 2),
  };
}

function buildGenericCard(item, payload) {
  return {
    variant: "generic",
    analyzerName: item.analyzer_name || "unknown",
    badge: String(item.severity || "").toLowerCase(),
    title: item.analyzer_name || item.category || "unknown",
    subtitle: "",
    marker: "",
    emphasis: "normal",
    summaryItems: [
      `label ${item.label || "unknown"}`,
      `score ${item.score || "0"}`,
      `confidence ${item.confidence || "0"}`,
    ],
    detailItems: [],
    meta: [item.category || "unknown", formatTime(item.created_at)],
    sortOrder: 4,
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
  const details = (model.detailItems || [])
    .map((entry) => `<span>${escapeHTML(entry)}</span>`)
    .join("");
  const meta = model.meta.map((entry) => `<span>${escapeHTML(entry)}</span>`).join("");
  const marker = model.marker
    ? `<span class="analysis-card-marker">${escapeHTML(model.marker)}</span>`
    : "";
  const subtitle = model.subtitle
    ? `<div class="analysis-card-subtitle">${escapeHTML(model.subtitle)}</div>`
    : "";
  return `
    <article class="analysis-card analysis-card-${escapeHTML(model.variant)} analysis-card-emphasis-${escapeHTML(model.emphasis || "normal")}">
      <div class="analysis-card-head">
        <div>
          <div class="analysis-card-kicker">${escapeHTML(model.analyzerName)}</div>
          <div class="analysis-card-title">${escapeHTML(model.title)}</div>
          ${subtitle}
        </div>
        <div class="analysis-card-head-side">
          ${marker}
          ${renderBadge(model.badge)}
        </div>
      </div>
      <div class="analysis-card-summary">${summary}</div>
      ${details ? `<div class="analysis-card-details-inline">${details}</div>` : ""}
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
  const sorted = list
    .map((item, index) => ({
      item,
      index,
      sortOrder: buildAnalysisResultCardModel(item).sortOrder ?? 99,
    }))
    .sort((left, right) => left.sortOrder - right.sortOrder || left.index - right.index)
    .map((entry) => entry.item);
  return `<div class="analysis-result-grid">${sorted.map(renderAnalysisResultCard).join("")}</div>`;
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
