const test = require("node:test");
const assert = require("node:assert/strict");

const {
  buildAnalysisResultCardModel,
  renderAnalysisResultCards,
} = require("./analysis_result_cards.js");

test("buildAnalysisResultCardModel maps primary work_relevance to 初步判断", () => {
  const model = buildAnalysisResultCardModel({
    analyzer_name: "work_relevance",
    category: "work_relevance",
    label: "coding",
    score: "0.92",
    confidence: "0.95",
    severity: "review",
    created_at: "2026-06-05 09:15:00+00",
    stage: "core",
    producer: "heuristic_work_relevance",
    result_key: "work_relevance_primary",
    result_json: JSON.stringify({
      task_category: "coding",
      decision: "work_related",
      recommended_action: "record_only",
      confidence_label: "high",
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
  assert.equal(model.title, "初步判断");
  assert.equal(model.subtitle, "工作相关");
  assert.deepEqual(model.summaryItems, ["建议：仅记录", "置信度：高（0.95）"]);
  assert.deepEqual(model.detailItems, [
    "类别：编码",
    "工作分：0.92",
    "非工作分：0.02",
    "风险分：0.00",
  ]);
  assert.equal(model.marker, "");
  assert.equal(model.emphasis, "normal");
  assert.equal(model.badge, "review");
  assert.match(model.meta.join(" | "), /09:15:00\+00/);
  assert.match(model.detailsJSON, /"recommended_action": "record_only"/);
});

test("buildAnalysisResultCardModel maps secondary work_relevance to 复核判断 with 最终参考", () => {
  const model = buildAnalysisResultCardModel({
    analyzer_name: "work_relevance",
    category: "work_relevance",
    label: "software_development",
    score: "0.72",
    confidence: "0.72",
    severity: "",
    created_at: "2026-06-05 09:16:00+00",
    stage: "enrichment",
    producer: "llm_judge",
    result_key: "work_relevance_secondary",
    result_json: JSON.stringify({
      task_category: "software_development",
      decision: "unknown",
      recommended_action: "allow",
      confidence_label: "medium",
      score_breakdown: {
        work: 0.61,
        non_work: 0.21,
        risk: 0.18,
      },
    }),
  });

  assert.equal(model.title, "复核判断");
  assert.equal(model.subtitle, "未知");
  assert.deepEqual(model.summaryItems, ["建议：允许", "置信度：中（0.72）"]);
  assert.deepEqual(model.detailItems, [
    "类别：软件开发",
    "工作分：0.61",
    "非工作分：0.21",
    "风险分：0.18",
  ]);
  assert.equal(model.marker, "最终参考");
  assert.equal(model.emphasis, "strong");
});

test("buildAnalysisResultCardModel falls back to 工作相关性判断 when source metadata is missing", () => {
  const model = buildAnalysisResultCardModel({
    analyzer_name: "work_relevance",
    category: "work_relevance",
    label: "coding",
    score: "0.20",
    confidence: "0.20",
    severity: "low",
    created_at: "2026-06-05 09:17:00+00",
    result_json: JSON.stringify({
      task_category: "coding",
      decision: "work_related",
      recommended_action: "record_only",
      confidence_label: "low",
    }),
  });

  assert.equal(model.title, "工作相关性判断");
  assert.equal(model.subtitle, "工作相关");
  assert.deepEqual(model.summaryItems, ["建议：仅记录", "置信度：低（0.20）"]);
  assert.equal(model.emphasis, "normal");
  assert.equal(model.badge, "low");
});

test("buildAnalysisResultCardModel formats usage_extraction as 用量信息", () => {
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
  assert.equal(model.title, "用量信息");
  assert.deepEqual(model.summaryItems, [
    "输入 8,200",
    "输出 10,000",
    "缓存 220",
    "推理 18",
  ]);
  assert.match(model.meta.join(" | "), /18,420/);
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
      analyzer_name: "work_relevance",
      category: "work_relevance",
      label: "software_development",
      score: "0.72",
      confidence: "0.72",
      severity: "",
      created_at: "2026-06-05 09:16:00+00",
      stage: "enrichment",
      producer: "llm_judge",
      result_key: "work_relevance_secondary",
      result_json: JSON.stringify({
        task_category: "software_development",
        decision: "unknown",
        recommended_action: "allow",
        confidence_label: "medium",
        score_breakdown: {
          work: 0.61,
          non_work: 0.21,
          risk: 0.18,
        },
      }),
    },
    {
      analyzer_name: "usage_extraction",
      category: "usage_extraction",
      label: "usage_from_gateway_job",
      score: "18420",
      confidence: "1",
      severity: "",
      created_at: "2026-06-05 09:18:00+00",
      result_json: JSON.stringify({
        prompt_tokens: 8200,
        completion_tokens: 10000,
        cached_tokens: 220,
        reasoning_tokens: 18,
        total_tokens: 18420,
      }),
    },
    {
      analyzer_name: "work_relevance",
      category: "work_relevance",
      label: "coding",
      score: "0.92",
      confidence: "0.95",
      severity: "review",
      created_at: "2026-06-05 09:15:00+00",
      stage: "core",
      producer: "heuristic_work_relevance",
      result_key: "work_relevance_primary",
      result_json: JSON.stringify({
        task_category: "coding",
        decision: "work_related",
        recommended_action: "record_only",
        confidence_label: "high",
        needs_review: true,
        work_related_score: 0.92,
        personal_use_score: 0.02,
        score_breakdown: {
          work: 0.92,
          non_work: 0.02,
          risk: 0.0,
        },
      }),
    },
  ]);
  assert.match(html, /analysis-result-grid/);
  assert.match(html, /analysis-card-title">初步判断</);
  assert.match(html, /analysis-card-subtitle">工作相关</);
  assert.match(html, /analysis-card-title">复核判断</);
  assert.match(html, /analysis-card-marker">最终参考</);
  assert.match(html, /analysis-card-title">用量信息</);
  assert.match(html, /建议：仅记录/);
  assert.match(html, /建议：允许/);
  assert.match(html, /类别：编码/);
  assert.match(html, /类别：软件开发/);
  assert.match(html, /置信度：高（0\.95）/);
  assert.match(html, /置信度：中（0\.72）/);
  assert.match(html, /class="badge review">review</);
  assert.match(html, /查看原始 JSON/);
  assert.ok(
    html.indexOf('analysis-card-title">初步判断') <
      html.indexOf('analysis-card-title">复核判断')
  );
  assert.ok(
    html.indexOf('analysis-card-title">复核判断') <
      html.indexOf('analysis-card-title">用量信息')
  );
  assert.match(renderAnalysisResultCards([]), /暂无分析结果/);
});

test("renderAnalysisResultCards escapes HTML in rendered fields", () => {
  const html = renderAnalysisResultCards([
    {
      analyzer_name: "<script>alert(1)</script>",
      category: "custom_rule",
      label: "odd_shape",
      score: "7",
      confidence: "0.4",
      severity: "low",
      created_at: "2026-06-05 09:15:00+00",
      result_json: "{\"snippet\":\"<b>bold</b>\"}",
    },
  ]);

  assert.match(html, /&lt;script&gt;alert\(1\)&lt;\/script&gt;/);
  assert.match(html, /&lt;b&gt;bold&lt;\/b&gt;/);
  assert.doesNotMatch(html, /<script>alert\(1\)<\/script>/);
  assert.doesNotMatch(html, /<b>bold<\/b>/);
});
