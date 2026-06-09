const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

function loadAppModule(overrides = {}) {
  const sourcePath = path.join(__dirname, "app.js");
  const source = fs.readFileSync(sourcePath, "utf8").replace(/\nboot\(\);\s*$/, "\n");
  const fakeApp = { innerHTML: "" };
  const fakeElement = {
    addEventListener() {},
    removeEventListener() {},
    getAttribute() { return ""; },
    matches() { return false; },
    closest() { return null; },
    getBoundingClientRect() {
      return { top: 0, left: 0, bottom: 0, width: 0, height: 0 };
    },
    scrollWidth: 0,
    clientWidth: 0,
    textContent: "",
    style: {},
  };
  const sandbox = {
    console,
    setTimeout: overrides.setTimeout || setTimeout,
    clearTimeout: overrides.clearTimeout || clearTimeout,
    URLSearchParams,
    FormData,
    document: {
      cookie: "",
      body: {
        appendChild() {},
      },
      getElementById(id) {
        if (id === "employee-usage-chart") {
          return {
            closest() {
              return null;
            },
          };
        }
        return null;
      },
      createElement() {
        return {
          className: "",
          textContent: "",
          style: {},
          getBoundingClientRect() {
            return { width: 0, height: 0 };
          },
          remove() {},
        };
      },
      querySelector(selector) {
        if (typeof overrides.querySelector === "function") {
          const overrideResult = overrides.querySelector(selector);
          if (overrideResult !== undefined) return overrideResult;
        }
        if (selector === "#app") return fakeApp;
        if (selector === "#change-password-button") return fakeElement;
        if (selector === "#logout-button") return fakeElement;
        if (selector === ".main") return fakeElement;
        return null;
      },
      querySelectorAll() {
        return [];
      },
    },
    window: {
      innerHeight: 900,
      innerWidth: 1440,
      UsagePage: overrides.usagePage || { renderUsagePage: () => "<section>usage</section>" },
      AdminAnalysisResultCards: { renderAnalysisResultCards: () => "" },
      Chart: overrides.Chart || function Chart() {},
    },
    fetch: overrides.fetch || (async () => ({
      ok: true,
      status: 200,
      json: async () => ({}),
      text: async () => "",
    })),
    module: { exports: {} },
    exports: {},
  };

  vm.runInNewContext(
    `${source}
module.exports = {
  state,
  loadUsage,
  renderOverview: typeof renderOverview !== "undefined" ? renderOverview : undefined,
  reloadUsageView,
  renderUsage,
  usageChart,
  renderEmployeeUsageChart,
  loadUsageSearchResults: typeof loadUsageSearchResults !== "undefined" ? loadUsageSearchResults : undefined,
  selectUsageEmployee: typeof selectUsageEmployee !== "undefined" ? selectUsageEmployee : undefined,
  bindUsageSearch: typeof bindUsageSearch !== "undefined" ? bindUsageSearch : undefined,
  __setRenderUsage(fn) { renderUsage = fn; },
  __setReloadUsageView(fn) { reloadUsageView = fn; },
  __getUsageRequestSeq() { return usageRequestSeq; },
  __getUsageSearchSeq() { return typeof usageSearchSeq !== "undefined" ? usageSearchSeq : undefined; },
};
`,
    sandbox,
  );

  return { app: sandbox.module.exports, fakeApp };
}

test("loadUsage fetches global usage by default and caches the response body", async () => {
  const calls = [];
  const responseBody = {
    global_usage: { total_tokens: 42 },
    employee_usage: null,
  };
  const { app } = loadAppModule({
    fetch: async (url) => {
      calls.push(url);
      return {
        ok: true,
        status: 200,
        json: async () => responseBody,
        text: async () => "",
      };
    },
  });
  const rendered = [];
  app.__setRenderUsage((body) => rendered.push(body));

  await app.loadUsage();

  assert.deepEqual(calls, ["/admin/api/usage"]);
  assert.equal(app.state.usage.body, responseBody);
  assert.deepEqual(rendered, [responseBody]);
});

test("loadUsageSearchResults queries fuzzy search endpoint and selectUsageEmployee resets model", async () => {
  const calls = [];
  const results = [{ username: "roy.zhang", display_name: "Roy Zhang" }];
  const { app } = loadAppModule({
    fetch: async (url) => {
      calls.push(url);
      return {
        ok: true,
        status: 200,
        json: async () => ({ employees: results }),
        text: async () => "",
      };
    },
  });
  let reloadCount = 0;
  app.__setReloadUsageView(async () => {
    reloadCount += 1;
  });
  app.state.usage.model = "gpt-5.2";

  await app.loadUsageSearchResults("roy");
  assert.deepEqual(app.state.usage.searchResults, results);
  await app.selectUsageEmployee("roy.zhang");

  assert.deepEqual(calls, ["/admin/api/usage-employees?q=roy"]);
  assert.equal(app.state.usage.searchResults.length, 0);
  assert.equal(app.state.usage.selectedEmployee, "roy.zhang");
  assert.equal(app.state.usage.model, "");
  assert.equal(reloadCount, 1);
});

test("loadUsage forwards selected employee, range, and model filters", async () => {
  const calls = [];
  const responseBody = {
    global_usage: { total_tokens: 84 },
    employee_usage: { username: "roy.zhang", points: [] },
  };
  const { app } = loadAppModule({
    fetch: async (url) => {
      calls.push(url);
      return {
        ok: true,
        status: 200,
        json: async () => responseBody,
        text: async () => "",
      };
    },
  });
  app.state.usage.selectedEmployee = "roy.zhang";
  app.state.usage.range = "1d";
  app.state.usage.model = "gpt-5.2";
  app.__setRenderUsage(() => {});

  await app.loadUsage();

  assert.deepEqual(calls, ["/admin/api/usage?username=roy.zhang&range=1d&model=gpt-5.2"]);
  assert.equal(app.state.usage.body, responseBody);
});

test("loadUsage keeps the cached global layer visible when employee detail loading fails", async () => {
  const renderCalls = [];
  const cachedBody = {
    global_usage: {
      total_tokens: 128,
      top_employees: [{ username: "roy.zhang", total_tokens: 128 }],
    },
    employee_usage: {
      username: "roy.zhang",
      points: [{ bucket_start: "2026-06-05", total_tokens: 128 }],
    },
  };
  const { app } = loadAppModule({
    fetch: async () => ({
      ok: false,
      status: 500,
      json: async () => ({}),
      text: async () => "detail exploded",
    }),
  });
  app.state.usage.body = cachedBody;
  app.state.usage.selectedEmployee = "someone-else";
  app.__setRenderUsage((body) => renderCalls.push(body));

  await app.loadUsage();

  assert.equal(app.state.usage.error, "detail exploded");
  assert.equal(renderCalls.length, 1);
  assert.equal(renderCalls[0].global_usage, cachedBody.global_usage);
  assert.equal(renderCalls[0].employee_usage, null);
});

test("loadUsageSearchResults clears stale suggestions when the query becomes empty", async () => {
  const calls = [];
  const { app } = loadAppModule({
    fetch: async (url) => {
      calls.push(url);
      return {
        ok: true,
        status: 200,
        json: async () => ({ employees: [] }),
        text: async () => "",
      };
    },
  });
  app.state.usage.body = { global_usage: { total_tokens: 5 } };
  app.state.usage.searchResults = [{ username: "roy.zhang" }];
  app.state.usage.searchError = "old error";

  await app.loadUsageSearchResults("");

  assert.equal(calls.length, 0);
  assert.equal(app.state.usage.searchResults.length, 0);
  assert.equal(app.state.usage.searchError, "");
});

test("renderEmployeeUsageChart still renders zero-valued ranges to preserve the padded timeline", () => {
  const chartCalls = [];
  const { app } = loadAppModule({
    Chart: function Chart(_canvas, config) {
      chartCalls.push(config);
      return { destroy() {} };
    },
  });

  app.renderEmployeeUsageChart([
    {
      bucket_start: "2026-06-05T00:00:00Z",
      bucket_size: "hour",
      total_tokens: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
      cached_tokens: 0,
    },
    {
      bucket_start: "2026-06-05T01:00:00Z",
      bucket_size: "hour",
      total_tokens: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
      cached_tokens: 0,
    },
  ]);

  assert.equal(chartCalls.length, 1);
  assert.deepEqual(chartCalls[0].data.labels, ["00:00", "01:00"]);
  assert.deepEqual(chartCalls[0].data.datasets[0].data, [0, 0]);
});

test("selectUsageEmployee clears any pending debounced employee search", async () => {
  const clearCalls = [];
  const inputListeners = {};
  const fakeInput = {
    value: "roy",
    addEventListener(event, handler) {
      inputListeners[event] = handler;
    },
  };
  const { app } = loadAppModule({
    querySelector(selector) {
      if (selector === "[data-usage-search-input]") return fakeInput;
      return undefined;
    },
    setTimeout(handler) {
      inputListeners.timeout = handler;
      return 99;
    },
    clearTimeout(timer) {
      clearCalls.push(timer);
    },
  });
  app.__setReloadUsageView(async () => {});

  app.bindUsageSearch();
  inputListeners.input();
  await app.selectUsageEmployee("roy.zhang");

  assert.deepEqual(clearCalls, [99]);
});

test("renderOverview shows business metrics without runtime cards", () => {
  const { app, fakeApp } = loadAppModule();

  app.renderOverview({
    overview: {
      request_count_24h: 120,
      total_tokens_24h: 5600,
      error_count_24h: 3,
      open_anomalies: 2,
      open_coverage_alerts: 1,
      raw_only_trace_count_24h: 4,
      token_usage_daily: [],
    },
    analysis_runtime: {
      core: {
        queue_depth: 9,
        oldest_pending_age_seconds: 42,
      },
      enrichment: {
        queue_depth: 7,
      },
    },
  });

  assert.match(fakeApp.innerHTML, /24h 请求数/);
  assert.match(fakeApp.innerHTML, /24h Token 数/);
  assert.match(fakeApp.innerHTML, /24h 错误数/);
  assert.doesNotMatch(fakeApp.innerHTML, /分析运行摘要/);
  assert.doesNotMatch(fakeApp.innerHTML, /队列深度/);
  assert.doesNotMatch(fakeApp.innerHTML, /待处理时长/);
  assert.doesNotMatch(fakeApp.innerHTML, /队列积压/);
});
