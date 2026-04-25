package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
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
	pattern, err := getenvDefault("EMPLOYEE_NO_PATTERN", `^[A-Za-z0-9_-]{2,64}$`)
	if err != nil {
		return Config{}, err
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return Config{}, fmt.Errorf("invalid EMPLOYEE_NO_PATTERN: %w", err)
	}

	listenAddr, err := getenvDefault("AUDIT_GATEWAY_LISTEN_ADDR", ":8080")
	if err != nil {
		return Config{}, err
	}
	if err := validateAddr("AUDIT_GATEWAY_LISTEN_ADDR", listenAddr, true); err != nil {
		return Config{}, err
	}

	newAPIBaseURL, err := requiredEnv("NEW_API_BASE_URL")
	if err != nil {
		return Config{}, err
	}
	if err := validateBaseURL(newAPIBaseURL); err != nil {
		return Config{}, err
	}

	auditHMACSecret, err := requiredEnv("AUDIT_HMAC_SECRET")
	if err != nil {
		return Config{}, err
	}

	evidenceStorageDir, err := requiredEnv("EVIDENCE_STORAGE_DIR")
	if err != nil {
		return Config{}, err
	}

	postgresDSN, err := requiredEnv("POSTGRES_DSN")
	if err != nil {
		return Config{}, err
	}
	if err := validatePostgresDSN(postgresDSN); err != nil {
		return Config{}, err
	}

	redisAddr, err := getenvDefault("REDIS_ADDR", "localhost:6379")
	if err != nil {
		return Config{}, err
	}
	if err := validateAddr("REDIS_ADDR", redisAddr, false); err != nil {
		return Config{}, err
	}

	cfg := Config{
		ListenAddr:         listenAddr,
		NewAPIBaseURL:      newAPIBaseURL,
		AuditHMACSecret:    auditHMACSecret,
		EvidenceStorageDir: evidenceStorageDir,
		PostgresDSN:        postgresDSN,
		RedisAddr:          redisAddr,
		EmployeeNoPattern:  compiled,
	}
	if len(cfg.AuditHMACSecret) < 32 {
		return Config{}, errors.New("AUDIT_HMAC_SECRET must be at least 32 characters")
	}
	return cfg, nil
}

func getenvDefault(key, fallback string) (string, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s cannot be blank", key)
	}
	return value, nil
}

func requiredEnv(key string) (string, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func validateBaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid NEW_API_BASE_URL: malformed URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid NEW_API_BASE_URL: scheme must be http or https")
	}
	if parsed.User != nil {
		return fmt.Errorf("invalid NEW_API_BASE_URL: userinfo is not allowed")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("invalid NEW_API_BASE_URL: host is required")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return fmt.Errorf("invalid NEW_API_BASE_URL: port is required after colon")
	}
	if parsed.Port() != "" {
		portNumber, err := strconv.Atoi(parsed.Port())
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("invalid NEW_API_BASE_URL: port must be between 1 and 65535")
		}
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return fmt.Errorf("invalid NEW_API_BASE_URL: query string is not allowed")
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("invalid NEW_API_BASE_URL: fragment is not allowed")
	}
	return nil
}

func validatePostgresDSN(dsn string) error {
	if _, err := pgxpool.ParseConfig(dsn); err != nil {
		return fmt.Errorf("invalid POSTGRES_DSN: %w", err)
	}
	return nil
}

func validateAddr(key, value string, allowEmptyHost bool) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", key, err)
	}
	if !allowEmptyHost && host == "" {
		return fmt.Errorf("invalid %s: host is required", key)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("invalid %s: port must be between 1 and 65535", key)
	}
	return nil
}
