package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/your-company/new-api-gateway/internal/alerts"
	"github.com/your-company/new-api-gateway/internal/config"
	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/gateway"
	"github.com/your-company/new-api-gateway/internal/identity"
	"github.com/your-company/new-api-gateway/internal/jobs"
	"github.com/your-company/new-api-gateway/internal/routes"
	"github.com/your-company/new-api-gateway/internal/traces"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}
	if err := run(context.Background(), cfg, log.Default()); err != nil {
		log.Fatalf("gateway error: %v", err)
	}
}

func run(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer redisClient.Close()

	handler := buildHandler(cfg, pool, redisClient, logger)
	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
	}
	logger.Printf("audit gateway listening on %s", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func buildHandler(cfg config.Config, pool *pgxpool.Pool, redisClient *redis.Client, logger *log.Logger) gateway.Handler {
	return gateway.Handler{
		UpstreamBaseURL: cfg.NewAPIBaseURL,
		Registry:        routes.DefaultRegistry(),
		EvidenceStore:   evidence.NewFilesystemStore(cfg.EvidenceStorageDir),
		TraceRepo:       traces.NewPostgresRepository(pool),
		IdentityResolver: identity.Resolver{
			Cache:             identity.RedisCache{Client: redisClient},
			Lookup:            identity.PostgresTokenLookup{Pool: pool},
			EmployeeNoPattern: cfg.EmployeeNoPattern,
		},
		AuditSecret:     cfg.AuditHMACSecret,
		AuditError:      auditErrorLogger(logger),
		JobPublisher:    jobs.NewRedisListPublisher(redisClient, jobs.DefaultRedisListName),
		CoverageEmitter: alerts.NewPostgresRepository(pool),
	}
}

func auditErrorLogger(logger *log.Logger) func(context.Context, error) {
	if logger == nil {
		logger = log.Default()
	}
	return func(ctx context.Context, err error) {
		if err == nil {
			return
		}
		logger.Printf("audit error: %s", redactSecrets(err.Error()))
	}
}

var bearerTokenPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)

func redactSecrets(value string) string {
	return bearerTokenPattern.ReplaceAllString(value, `${1}[REDACTED]`)
}
