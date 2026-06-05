(function attachRuntimeCopy(globalObject) {
  const rangeLabels = {
    "15m": "近 15 分钟",
    "1h": "近 1 小时",
    "24h": "近 24 小时",
  };

  function runtimeSamplingRangeLabel(range) {
    const normalized = String(range || "").trim();
    return rangeLabels[normalized] || rangeLabels["1h"];
  }

  function runtimeSamplingSummary(range, sampleCount) {
    const label = runtimeSamplingRangeLabel(range);
    const count = Number(sampleCount);
    if (!Number.isFinite(count) || count <= 0) {
      return `${label}暂无采样`;
    }
    return `${label}采样 ${count.toLocaleString()} 次`;
  }

  function runtimeSamplingTooltip() {
    return "采样次数表示当前时间范围内记录到的运行采样数量，不代表请求数、队列长度或时延值。采样通常按固定间隔生成；如果 worker 重启、暂停或采样间隔调整，次数可能不是固定值。";
  }

  const api = {
    runtimeSamplingRangeLabel,
    runtimeSamplingSummary,
    runtimeSamplingTooltip,
  };

  globalObject.AdminRuntimeCopy = api;

  if (typeof module !== "undefined" && module.exports) {
    module.exports = api;
  }
})(typeof window !== "undefined" ? window : globalThis);
