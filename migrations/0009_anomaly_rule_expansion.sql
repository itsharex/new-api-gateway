INSERT INTO anomaly_rules (rule_key, threshold_json, severity, rule_window)
VALUES
    ('identity_db_error', '{"enabled": true}'::jsonb, 'high', 'per_trace'),
    ('daily_token_limit_exceeded', '{"total_tokens": 100000}'::jsonb, 'high', 'day'),
    ('short_window_token_spike', '{"total_tokens": 10000}'::jsonb, 'medium', '5m'),
    ('expensive_model_overuse', '{"models": ["gpt-4.5-preview", "o1-pro"], "total_tokens": 500}'::jsonb, 'high', 'per_trace'),
    ('long_output_anomaly', '{"completion_tokens": 8000}'::jsonb, 'medium', 'per_trace'),
    ('repeated_prompt', '{"repeat_count": 3}'::jsonb, 'medium', 'per_trace'),
    ('off_hours_high_usage', '{"local_timezone_offset_hours": 8, "total_tokens": 2000}'::jsonb, 'medium', 'per_trace'),
    ('possible_token_leak', '{"distinct_client_hashes": 3}'::jsonb, 'high', '1h')
ON CONFLICT (rule_key) DO NOTHING;
