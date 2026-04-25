package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
)

type Config struct {
	ListenAddr         string
	NewAPIBaseURL      string
	AuditHMACSecret    string
	EvidenceStorageDir string
	PostgresDSN        string
	RedisAddr          string
	EmployeeNoPattern  *regexp.Regexp
}

func LoadFromEnv() (Config, error) {
	pattern := getenv("EMPLOYEE_NO_PATTERN", `^[A-Za-z0-9_-]{2,64}$`)
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return Config{}, fmt.Errorf("invalid EMPLOYEE_NO_PATTERN: %w", err)
	}

	cfg := Config{
		ListenAddr:         getenv("AUDIT_GATEWAY_LISTEN_ADDR", ":8080"),
		NewAPIBaseURL:      os.Getenv("NEW_API_BASE_URL"),
		AuditHMACSecret:    os.Getenv("AUDIT_HMAC_SECRET"),
		EvidenceStorageDir: os.Getenv("EVIDENCE_STORAGE_DIR"),
		PostgresDSN:        os.Getenv("POSTGRES_DSN"),
		RedisAddr:          getenv("REDIS_ADDR", "localhost:6379"),
		EmployeeNoPattern:  compiled,
	}
	if cfg.NewAPIBaseURL == "" {
		return Config{}, errors.New("NEW_API_BASE_URL is required")
	}
	if cfg.AuditHMACSecret == "" {
		return Config{}, errors.New("AUDIT_HMAC_SECRET is required")
	}
	if len(cfg.AuditHMACSecret) < 32 {
		return Config{}, errors.New("AUDIT_HMAC_SECRET must be at least 32 characters")
	}
	if cfg.EvidenceStorageDir == "" {
		return Config{}, errors.New("EVIDENCE_STORAGE_DIR is required")
	}
	if cfg.PostgresDSN == "" {
		return Config{}, errors.New("POSTGRES_DSN is required")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
