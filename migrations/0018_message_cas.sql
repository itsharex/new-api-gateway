-- 0018: 消息级 content-addressed storage
-- 旧 normalized_messages 数据全部丢弃；新表只对新 trace 生效。
-- 视图保留同名 normalized_messages 以兼容 admin 读路径。

DROP TABLE IF EXISTS normalized_messages CASCADE;

CREATE TABLE messages (
    message_id           BIGSERIAL PRIMARY KEY,
    message_key          TEXT NOT NULL UNIQUE,
    role                 TEXT NOT NULL,
    modality             TEXT NOT NULL DEFAULT 'text',
    content_text         TEXT NOT NULL,
    content_text_hash    TEXT NOT NULL,
    token_count_estimate INTEGER NOT NULL DEFAULT 0,
    first_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    first_trace_id       TEXT NOT NULL,
    occurrence_count     BIGINT NOT NULL DEFAULT 1
);

CREATE INDEX idx_messages_content_hash ON messages(content_text_hash);
CREATE INDEX idx_messages_role_modality ON messages(role, modality);

CREATE TABLE trace_messages (
    trace_id           TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    message_id         BIGINT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    direction          TEXT NOT NULL,
    sequence_index     INTEGER NOT NULL,
    source_path        TEXT NOT NULL DEFAULT '',
    protocol_item_type TEXT NOT NULL DEFAULT '',
    media_url          TEXT NOT NULL DEFAULT '',
    media_object_id    BIGINT,
    metadata_json      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (trace_id, direction, sequence_index, source_path)
);

CREATE INDEX idx_trace_messages_message ON trace_messages(message_id);
CREATE INDEX idx_trace_messages_trace ON trace_messages(trace_id);

CREATE VIEW normalized_messages AS
SELECT
    tm.trace_id,
    tm.direction,
    tm.sequence_index,
    m.role,
    m.modality,
    m.content_text,
    m.content_text_hash,
    tm.media_url,
    tm.source_path,
    tm.protocol_item_type,
    m.token_count_estimate,
    tm.metadata_json,
    tm.created_at
FROM trace_messages tm
JOIN messages m ON m.message_id = tm.message_id;
