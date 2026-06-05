const test = require("node:test");
const assert = require("node:assert/strict");

const {
  runtimeSamplingRangeLabel,
  runtimeSamplingSummary,
  runtimeSamplingTooltip,
} = require("./runtime_copy.js");

test("runtimeSamplingRangeLabel maps supported ranges to Chinese labels", () => {
  assert.equal(runtimeSamplingRangeLabel("15m"), "近 15 分钟");
  assert.equal(runtimeSamplingRangeLabel("1h"), "近 1 小时");
  assert.equal(runtimeSamplingRangeLabel("24h"), "近 24 小时");
});

test("runtimeSamplingRangeLabel falls back to 1h label for unknown range", () => {
  assert.equal(runtimeSamplingRangeLabel("6h"), "近 1 小时");
  assert.equal(runtimeSamplingRangeLabel(""), "近 1 小时");
});

test("runtimeSamplingSummary renders count copy for non-empty history", () => {
  assert.equal(runtimeSamplingSummary("15m", 12), "近 15 分钟采样 12 次");
  assert.equal(runtimeSamplingSummary("1h", 83), "近 1 小时采样 83 次");
  assert.equal(runtimeSamplingSummary("24h", 1248), "近 24 小时采样 1,248 次");
});

test("runtimeSamplingSummary renders empty copy instead of zero count", () => {
  assert.equal(runtimeSamplingSummary("15m", 0), "近 15 分钟暂无采样");
  assert.equal(runtimeSamplingSummary("1h", -3), "近 1 小时暂无采样");
});

test("runtimeSamplingTooltip explains the metric semantics", () => {
  assert.match(runtimeSamplingTooltip(), /不代表请求数、队列长度或时延值/);
  assert.match(runtimeSamplingTooltip(), /worker 重启、暂停或采样间隔调整/);
});
