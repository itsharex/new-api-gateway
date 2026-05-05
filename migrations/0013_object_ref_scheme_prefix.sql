-- 0013_object_ref_scheme_prefix.sql
-- Add file:/// scheme prefix to all existing filesystem object_ref values.
-- Idempotent: NOT LIKE guard prevents double-prefixing.

UPDATE raw_evidence_objects
SET object_ref = 'file:///' || object_ref
WHERE storage_backend = 'filesystem'
  AND object_ref NOT LIKE 'file:///%';
