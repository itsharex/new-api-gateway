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

function formatTimestamp(value) {
  const timestamp = Date.parse(String(value ?? ""));
  return Number.isFinite(timestamp) ? timestamp : 0;
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
    case "needs_review":
      return "需进一步复核";
    case "unknown":
      return "未知";
    default:
      return "结论待定";
  }
}

function formatAction(value) {
  switch (String(value || "").toLowerCase()) {
    case "record_only":
      return "仅记录";
    case "allow":
      return "允许";
    case "alert_non_work":
      return "提示非工作用途";
    case "review_conflict":
      return "复核冲突";
    case "block":
      return "阻止";
    case "review":
      return "人工复核";
    default:
      return "仅记录";
  }
}

function formatCategory(value) {
  switch (String(value || "").toLowerCase()) {
    case "coding":
      return "编码";
    case "software_development":
      return "软件开发";
    case "debugging":
      return "调试排障";
    case "documentation":
      return "文档编写";
    case "code_review":
      return "代码评审";
    case "job_search":
      return "求职应聘";
    case "unknown":
      return "未分类";
    case "personal_chat":
      return "个人闲聊";
    case "side_business":
      return "副业经营";
    case "policy_violation":
      return "违规风险";
    default:
      return "未分类";
  }
}

function formatSubtitleCategory(value) {
  switch (String(value || "").toLowerCase()) {
    case "coding":
      return "编码相关";
    case "software_development":
      return "软件开发";
    case "debugging":
      return "调试排障";
    case "documentation":
      return "文档编写";
    case "code_review":
      return "代码评审";
    case "job_search":
      return "求职应聘";
    case "unknown":
      return "未分类";
    case "personal_chat":
      return "个人闲聊";
    case "side_business":
      return "副业经营";
    case "policy_violation":
      return "违规风险";
    default:
      return formatCategory(value);
  }
}

function formatConfidenceLabel(value) {
  const score = finiteNumber(value);
  if (score >= 0.8) {
    return "高";
  }
  if (score >= 0.5) {
    return "中";
  }
  return "低";
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
  const category = pickFirst(payload.task_category, "unknown");
  const decision = pickFirst(payload.decision, "pending");
  const action = pickFirst(payload.recommended_action, item.action, "record_only");
  const confidenceValue = pickFirst(payload.confidence, item.confidence, 0);
  const severity = String(item.severity || "").toLowerCase();
  return {
    variant: "work_relevance",
    identity: identity.identity,
    analyzerName: item.analyzer_name || "work_relevance",
    badge: payload.needs_review || severity === "review" ? "review" : severity,
    title: identity.title,
    subtitle: `${formatDecision(decision)} · ${formatSubtitleCategory(category)}`,
    marker: identity.marker,
    emphasis: identity.emphasis,
    summaryItems: [
      `建议：${formatAction(action)}`,
      `置信度：${formatConfidenceLabel(confidenceValue)}（${formatScore(confidenceValue)}）`,
    ],
    detailItems: [
      `类别：${formatCategory(category)}`,
      `工作分：${formatScore(breakdown.work ?? payload.work_related_score ?? item.score)}`,
      `非工作分：${formatScore(breakdown.non_work ?? payload.personal_use_score)}`,
      `风险分：${formatScore(breakdown.risk ?? 0)}`,
    ],
    extraDetailItems: [],
    meta: [formatTime(item.created_at)],
    sortOrder: identity.sortOrder,
    createdAtMs: formatTimestamp(item.created_at),
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
    extraDetailItems: [],
    meta: [`总量 ${formatCount(totalTokens)}`, formatTime(item.created_at)],
    sortOrder: 2,
    createdAtMs: formatTimestamp(item.created_at),
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
    extraDetailItems: [],
    meta: [item.category || "unknown", formatTime(item.created_at)],
    sortOrder: 4,
    createdAtMs: formatTimestamp(item.created_at),
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

function buildSupplementalDetailItems(models) {
  if (!models.length) return [];
  const items = [`附加结果 ${models.length} 条`];
  for (const model of models) {
    items.push(
      `${model.subtitle}；${[...(model.summaryItems || []), ...(model.detailItems || [])].join("；")}`
    );
  }
  return items;
}

function mergeAnalysisResultCardModels(models) {
  const grouped = new Map();
  const merged = [];

  for (const model of models) {
    if (model.variant === "work_relevance" && (model.identity === "primary" || model.identity === "secondary")) {
      const existing = grouped.get(model.identity);
      if (!existing) {
        grouped.set(model.identity, {
          primary: model,
          extras: [],
        });
        continue;
      }

      if ((model.createdAtMs ?? 0) >= (existing.primary.createdAtMs ?? 0)) {
        existing.extras.push(existing.primary);
        existing.primary = model;
      } else {
        existing.extras.push(model);
      }
      continue;
    }

    merged.push(model);
  }

  for (const identity of ["primary", "secondary"]) {
    const group = grouped.get(identity);
    if (!group) continue;
    const extras = group.extras.sort((left, right) => (right.createdAtMs ?? 0) - (left.createdAtMs ?? 0));
    merged.push({
      ...group.primary,
      extraDetailItems: [...(group.primary.extraDetailItems || []), ...buildSupplementalDetailItems(extras)],
    });
  }

  return merged.sort(
    (left, right) =>
      (left.sortOrder ?? 99) - (right.sortOrder ?? 99) ||
      (right.createdAtMs ?? 0) - (left.createdAtMs ?? 0)
  );
}

function renderAnalysisResultCard(item) {
  const model = item && Array.isArray(item.summaryItems) ? item : buildAnalysisResultCardModel(item);
  const summary = model.summaryItems.map((entry) => `<span>${escapeHTML(entry)}</span>`).join("");
  const details = (model.detailItems || [])
    .map((entry) => `<span>${escapeHTML(entry)}</span>`)
    .join("");
  const extraDetails = (model.extraDetailItems || [])
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
        ${extraDetails ? `<div class="analysis-card-details-inline">${extraDetails}</div>` : ""}
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
  const merged = mergeAnalysisResultCardModels(list.map(buildAnalysisResultCardModel));
  return `<div class="analysis-result-grid">${merged.map(renderAnalysisResultCard).join("")}</div>`;
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
