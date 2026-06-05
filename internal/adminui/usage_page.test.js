const test = require("node:test");
const assert = require("node:assert/strict");

const {
  renderUsagePage,
  formatActiveBucketHint,
} = require("./usage_page.js");

test("renderUsagePage shows global usage before an employee is selected", () => {
  const html = renderUsagePage({
    global_usage: {
      total_tokens: 18420,
      active_employees: 17,
      request_count: 42,
      active_models: 6,
      top_employees: [
        { username: "roy.zhang", display_name: "Roy Zhang", department: "Platform", total_tokens: 9000, request_count: 12, last_seen_at: "2026-06-05 08:00:00+00" },
      ],
      top_models: [
        { model: "gpt-5.2", total_tokens: 12000, request_count: 21, success_count: 21, error_count: 0, prompt_tokens: 0, completion_tokens: 0, cached_tokens: 0 },
      ],
    },
    employee_usage: null,
    usageState: { searchQuery: "", searchResults: [], searchError: "", selectedEmployee: "" },
  });

  assert.match(html, /搜索员工/);
  assert.match(html, /Top 员工榜/);
  assert.match(html, /Roy Zhang/);
  assert.doesNotMatch(html, /当前查看：/);
});

test("renderUsagePage renders fuzzy search suggestions and expanded detail panel", () => {
  const html = renderUsagePage({
    global_usage: {
      total_tokens: 18420,
      active_employees: 17,
      request_count: 42,
      active_models: 6,
      top_employees: [],
      top_models: [],
    },
    employee_usage: {
      username: "roy.zhang",
      range: "1d",
      bucket_size: "hour",
      active_bucket_count: 1,
      expected_bucket_count: 24,
      selected_model: "",
      models: ["gpt-5.2"],
      summary: { request_count: 2, prompt_tokens: 100, completion_tokens: 40, cached_tokens: 0, total_tokens: 140 },
      points: [{ bucket_start: "2026-06-04T15:00:00Z", bucket_size: "hour", request_count: 2, success_count: 2, error_count: 0, prompt_tokens: 100, completion_tokens: 40, cached_tokens: 0, total_tokens: 140 }],
      model_summary: [{ model: "gpt-5.2", request_count: 2, success_count: 2, error_count: 0, prompt_tokens: 100, completion_tokens: 40, cached_tokens: 0, total_tokens: 140 }],
    },
    usageState: {
      searchQuery: "roy",
      searchResults: [{ username: "roy.zhang", display_name: "Roy Zhang", department: "Platform", last_seen_at: "2026-06-05 08:00:00+00" }],
      searchError: "",
      selectedEmployee: "roy.zhang",
    },
  });

  assert.match(html, /搜索建议/);
  assert.match(html, /当前查看：roy\.zhang/);
  assert.match(html, /仅 1 个时间桶有实际流量/);
  assert.match(html, /收起详情/);
});

test("formatActiveBucketHint matches sparse hourly and daily copy", () => {
  assert.equal(formatActiveBucketHint("1d", 1, 24), "当前范围内仅 1 个时间桶有实际流量");
  assert.equal(formatActiveBucketHint("30d", 2, 30), "当前范围内仅 2 个时间桶有实际流量");
  assert.equal(formatActiveBucketHint("30d", 30, 30), "");
});
