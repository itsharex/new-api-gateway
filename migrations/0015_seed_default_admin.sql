-- Seed default admin user (idempotent)
CREATE EXTENSION IF NOT EXISTS pgcrypto;

INSERT INTO audit_users (username, password_hash, role, display_name)
VALUES ('admin', crypt('admin', gen_salt('bf')), 'admin', 'Admin')
ON CONFLICT (username) DO NOTHING;
