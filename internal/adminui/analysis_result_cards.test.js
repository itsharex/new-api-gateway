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
