package config

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	ListenAddr               string
	NewAPIBaseURL            string
	AuditHMACSecret          string
	EvidenceStorageDir       string
	EvidenceStorageBackend   string
	OSSEndpoint              string
	OSSBucket                string
	OSSAccessKeyID           string
	OSSAccessKeySecret       string
	DegradedSpoolDir         string
	PostgresDSN              string
	NewAPIPostgresDSN        string
	RedisAddr                string
	AdminSessionSecret       string
	AdminCookieName          string
	AdminCookieSecure        bool
	OpsCheckTimeout          time.Duration
	OpsWorkerHeartbeatMaxAge time.Duration
	OpsQueueLagWarnThreshold int64
	OpsMetricsEnabled        bool
}

func LoadFromEnv() (Config, error) {
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
	if len(auditHMACSecret) < 32 {
		return Config{}, errors.New("AUDIT_HMAC_SECRET must be at least 32 characters")
	}

	adminSessionSecret, err := getenvDefault("ADMIN_SESSION_SECRET", auditHMACSecret)
	if err != nil {
		return Config{}, err
	}
	if len(adminSessionSecret) < 32 {
		return Config{}, errors.New("ADMIN_SESSION_SECRET must be at least 32 characters")
	}

	adminCookieName, err := getenvDefault("ADMIN_COOKIE_NAME", "audit_admin_session")
	if err != nil {
		return Config{}, err
	}
	if err := (&http.Cookie{Name: adminCookieName, Value: "x"}).Valid(); err != nil {
		return Config{}, fmt.Errorf("invalid ADMIN_COOKIE_NAME: %w", err)
	}

	adminCookieSecureRaw, err := getenvDefault("ADMIN_COOKIE_SECURE", "false")
	if err != nil {
		return Config{}, err
	}
	adminCookieSecure, err := strconv.ParseBool(adminCookieSecureRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid ADMIN_COOKIE_SECURE: must be true or false")
	}

	opsCheckTimeout, err := getenvDurationDefault("OPS_CHECK_TIMEOUT", 2*time.Second)
	if err != nil {
		return Config{}, err
	}
	opsWorkerHeartbeatMaxAge, err := getenvDurationDefault("OPS_WORKER_HEARTBEAT_MAX_AGE", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	opsQueueLagWarnThreshold, err := getenvInt64Default("OPS_QUEUE_LAG_WARN_THRESHOLD", 1000)
	if err != nil {
		return Config{}, err
	}
	opsMetricsEnabledRaw, err := getenvDefault("OPS_METRICS_ENABLED", "true")
	if err != nil {
		return Config{}, err
	}
	opsMetricsEnabled, err := strconv.ParseBool(opsMetricsEnabledRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid OPS_METRICS_ENABLED: must be true or false")
	}

	evidenceStorageBackend, err := requiredEnv("EVIDENCE_STORAGE_BACKEND")
	if err != nil {
		return Config{}, err
	}
	if evidenceStorageBackend != "filesystem" && evidenceStorageBackend != "oss" {
		return Config{}, fmt.Errorf("EVIDENCE_STORAGE_BACKEND must be filesystem or oss, got %q", evidenceStorageBackend)
	}

	var evidenceStorageDir string
	var ossEndpoint, ossBucket, ossAccessKeyID, ossAccessKeySecret string
	switch evidenceStorageBackend {
	case "filesystem":
		evidenceStorageDir, err = requiredEnv("EVIDENCE_STORAGE_DIR")
		if err != nil {
			return Config{}, err
		}
	case "oss":
		ossEndpoint, err = requiredEnv("OSS_ENDPOINT")
		if err != nil {
			return Config{}, err
		}
		ossBucket, err = requiredEnv("OSS_BUCKET")
		if err != nil {
			return Config{}, err
		}
		ossAccessKeyID, err = requiredEnv("OSS_ACCESS_KEY_ID")
		if err != nil {
			return Config{}, err
		}
		ossAccessKeySecret, err = requiredEnv("OSS_ACCESS_KEY_SECRET")
		if err != nil {
			return Config{}, err
		}
	}

	degradedSpoolDir, err := getenvDefault("DEGRADED_SPOOL_DIR", filepath.Join(os.TempDir(), "new-api-gateway-spool"))
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

	newAPIPostgresDSN, err := requiredEnv("NEW_API_POSTGRES_DSN")
	if err != nil {
		return Config{}, err
	}
	if err := validatePostgresDSN(newAPIPostgresDSN); err != nil {
		return Config{}, fmt.Errorf("invalid NEW_API_POSTGRES_DSN: %w", err)
	}

	redisAddr, err := getenvDefault("REDIS_ADDR", "localhost:6379")
	if err != nil {
		return Config{}, err
	}
	if err := validateAddr("REDIS_ADDR", redisAddr, false); err != nil {
		return Config{}, err
	}

	cfg := Config{
		ListenAddr:               listenAddr,
		NewAPIBaseURL:            newAPIBaseURL,
		AuditHMACSecret:          auditHMACSecret,
		EvidenceStorageDir:       evidenceStorageDir,
		EvidenceStorageBackend:   evidenceStorageBackend,
		OSSEndpoint:              ossEndpoint,
		OSSBucket:                ossBucket,
		OSSAccessKeyID:           ossAccessKeyID,
		OSSAccessKeySecret:       ossAccessKeySecret,
		DegradedSpoolDir:         degradedSpoolDir,
		PostgresDSN:              postgresDSN,
		NewAPIPostgresDSN:        newAPIPostgresDSN,
		RedisAddr:                redisAddr,
		AdminSessionSecret:       adminSessionSecret,
		AdminCookieName:          adminCookieName,
		AdminCookieSecure:        adminCookieSecure,
		OpsCheckTimeout:          opsCheckTimeout,
		OpsWorkerHeartbeatMaxAge: opsWorkerHeartbeatMaxAge,
		OpsQueueLagWarnThreshold: opsQueueLagWarnThreshold,
		OpsMetricsEnabled:        opsMetricsEnabled,
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

func getenvDurationDefault(key string, fallback time.Duration) (time.Duration, error) {
	raw, err := getenvDefault(key, fallback.String())
	if err != nil {
		return 0, err
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func getenvInt64Default(key string, fallback int64) (int64, error) {
	raw, err := getenvDefault(key, strconv.FormatInt(fallback, 10))
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
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
