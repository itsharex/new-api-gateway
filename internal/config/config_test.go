package config

import (
	"os"
	"strings"
	"testing"
)

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

func TestLoadFromEnvRejectsInvalidRegex(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EMPLOYEE_NO_PATTERN", `[invalid`)

	_, err := LoadFromEnv()
	assertErrorContains(t, err, "invalid EMPLOYEE_NO_PATTERN")
}

func TestLoadFromEnvRejectsShortSecret(t *testing.T) {
	setValidEnv(t)
	t.Setenv("AUDIT_HMAC_SECRET", "too-short")

	_, err := LoadFromEnv()
	assertErrorContains(t, err, "AUDIT_HMAC_SECRET must be at least 32 characters")
}

func TestLoadFromEnvLoadsAdminSettings(t *testing.T) {
	setValidEnv(t)
	t.Setenv("ADMIN_SESSION_SECRET", "admin-session-secret-0123456789abcdef")
	t.Setenv("ADMIN_COOKIE_NAME", "audit_admin_session")
	t.Setenv("ADMIN_COOKIE_SECURE", "true")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if cfg.AdminSessionSecret != "admin-session-secret-0123456789abcdef" {
		t.Fatalf("AdminSessionSecret = %q", cfg.AdminSessionSecret)
	}
	if cfg.AdminCookieName != "audit_admin_session" {
		t.Fatalf("AdminCookieName = %q", cfg.AdminCookieName)
	}
	if !cfg.AdminCookieSecure {
		t.Fatal("AdminCookieSecure = false, want true")
	}
}

func TestLoadFromEnvRejectsShortAdminSessionSecret(t *testing.T) {
	setValidEnv(t)
	t.Setenv("ADMIN_SESSION_SECRET", "short")

	_, err := LoadFromEnv()
	assertErrorContains(t, err, "ADMIN_SESSION_SECRET must be at least 32 characters")
}

func TestLoadFromEnvRejectsInvalidAdminCookieName(t *testing.T) {
	for _, cookieName := range []string{"bad name", "bad;name", "会话"} {
		t.Run(cookieName, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv("ADMIN_COOKIE_NAME", cookieName)

			_, err := LoadFromEnv()
			assertErrorContains(t, err, "ADMIN_COOKIE_NAME")
		})
	}
}

func TestLoadFromEnvUsesListenAndRedisDefaultsWhenUnset(t *testing.T) {
	setValidEnv(t)
	unsetEnv(t, "AUDIT_GATEWAY_LISTEN_ADDR")
	unsetEnv(t, "REDIS_ADDR")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.RedisAddr != "localhost:6379" {
		t.Fatalf("RedisAddr = %q", cfg.RedisAddr)
	}
}

func TestLoadFromEnvRejectsInvalidBaseURL(t *testing.T) {
	for _, rawURL := range []string{
		"localhost:3000",
		"ftp://localhost:3000",
		"http:///audit",
		"http://localhost:99999",
		"http://user:pass@localhost:3000",
		"http://localhost:3000?x=1",
		"http://localhost:3000?",
		"http://localhost:3000#frag",
	} {
		t.Run(rawURL, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv("NEW_API_BASE_URL", rawURL)

			_, err := LoadFromEnv()
			assertErrorContains(t, err, "invalid NEW_API_BASE_URL")
		})
	}
}

func TestLoadFromEnvSanitizesMalformedBaseURLError(t *testing.T) {
	rawURL := "http://gateway-user:super-secret-token@localhost:bad-port"
	setValidEnv(t)
	t.Setenv("NEW_API_BASE_URL", rawURL)

	_, err := LoadFromEnv()
	assertErrorContains(t, err, "invalid NEW_API_BASE_URL")
	assertErrorOmits(t, err, rawURL)
	assertErrorOmits(t, err, "gateway-user")
	assertErrorOmits(t, err, "super-secret-token")
	assertErrorOmits(t, err, "gateway-user:super-secret-token")
}

func TestLoadFromEnvRejectsInvalidPostgresDSN(t *testing.T) {
	setValidEnv(t)
	t.Setenv("POSTGRES_DSN", "://not-a-dsn")

	_, err := LoadFromEnv()
	assertErrorContains(t, err, "invalid POSTGRES_DSN")
}

func TestLoadFromEnvRejectsExplicitBlankDefaultedValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "blank", value: "  "},
	} {
		for _, key := range []string{"AUDIT_GATEWAY_LISTEN_ADDR", "REDIS_ADDR", "EMPLOYEE_NO_PATTERN"} {
			t.Run(key+"/"+tc.name, func(t *testing.T) {
				setValidEnv(t)
				t.Setenv(key, tc.value)

				_, err := LoadFromEnv()
				assertErrorContains(t, err, key)
			})
		}
	}
}

func TestLoadFromEnvRejectsMalformedListenAddr(t *testing.T) {
	for _, listenAddr := range []string{"localhost", ":0", ":70000", "localhost:70000"} {
		t.Run(listenAddr, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv("AUDIT_GATEWAY_LISTEN_ADDR", listenAddr)

			_, err := LoadFromEnv()
			assertErrorContains(t, err, "AUDIT_GATEWAY_LISTEN_ADDR")
		})
	}
}

func TestLoadFromEnvRejectsMalformedRedisAddr(t *testing.T) {
	for _, redisAddr := range []string{"localhost", "localhost:0", "localhost:70000", "redis://localhost:6379", ":6379"} {
		t.Run(redisAddr, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv("REDIS_ADDR", redisAddr)

			_, err := LoadFromEnv()
			assertErrorContains(t, err, "REDIS_ADDR")
		})
	}
}

func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AUDIT_GATEWAY_LISTEN_ADDR", ":18080")
	t.Setenv("NEW_API_BASE_URL", "http://127.0.0.1:3000")
	t.Setenv("AUDIT_HMAC_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("EVIDENCE_STORAGE_DIR", t.TempDir())
	t.Setenv("POSTGRES_DSN", "postgres://audit:pass@localhost:5432/audit?sslmode=disable")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("EMPLOYEE_NO_PATTERN", `^[A-Z][0-9]{5}$`)
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	oldValue, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, oldValue)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err.Error(), want)
	}
}

func assertErrorOmits(t *testing.T, err error, forbidden string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error that omits %q", forbidden)
	}
	if strings.Contains(err.Error(), forbidden) {
		t.Fatalf("error = %q, must not contain %q", err.Error(), forbidden)
	}
}
