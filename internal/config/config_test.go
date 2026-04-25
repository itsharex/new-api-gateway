package config

import "testing"

func TestLoadFromEnvRequiresCoreValues(t *testing.T) {
	t.Setenv("AUDIT_GATEWAY_LISTEN_ADDR", ":18080")
	t.Setenv("NEW_API_BASE_URL", "http://127.0.0.1:3000")
	t.Setenv("AUDIT_HMAC_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("EVIDENCE_STORAGE_DIR", t.TempDir())
	t.Setenv("POSTGRES_DSN", "postgres://audit:pass@localhost:5432/audit?sslmode=disable")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("EMPLOYEE_NO_PATTERN", `^[A-Z][0-9]{5}$`)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if cfg.ListenAddr != ":18080" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.NewAPIBaseURL != "http://127.0.0.1:3000" {
		t.Fatalf("NewAPIBaseURL = %q", cfg.NewAPIBaseURL)
	}
	if cfg.EmployeeNoPattern.String() != `^[A-Z][0-9]{5}$` {
		t.Fatalf("EmployeeNoPattern = %q", cfg.EmployeeNoPattern.String())
	}
}

func TestLoadFromEnvRejectsMissingSecret(t *testing.T) {
	t.Setenv("AUDIT_GATEWAY_LISTEN_ADDR", ":18080")
	t.Setenv("NEW_API_BASE_URL", "http://127.0.0.1:3000")
	t.Setenv("EVIDENCE_STORAGE_DIR", t.TempDir())
	t.Setenv("POSTGRES_DSN", "postgres://audit:pass@localhost:5432/audit?sslmode=disable")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("EMPLOYEE_NO_PATTERN", `^[A-Z][0-9]{5}$`)

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error for missing AUDIT_HMAC_SECRET")
	}
}
