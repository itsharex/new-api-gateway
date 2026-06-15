-- 0019: drop unused anomaly_rules table
-- 该表在 0004/0005/0009 中被 INSERT，但 worker 和 admin 从未 SELECT。
-- 规则阈值在 workers/analysis_worker/rules.py 中以硬编码常量形式存在，
-- 与 anomaly_rules.threshold_json 完全脱钩。整表为死配置，直接 drop。

DROP TABLE IF EXISTS anomaly_rules;
