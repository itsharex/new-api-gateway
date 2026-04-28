package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/your-company/new-api-gateway/internal/admin"
	"github.com/your-company/new-api-gateway/internal/adminui"
	"github.com/your-company/new-api-gateway/internal/alerts"
	"github.com/your-company/new-api-gateway/internal/config"
	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/gateway"
	"github.com/your-company/new-api-gateway/internal/identity"
	"github.com/your-company/new-api-gateway/internal/jobs"
	"github.com/your-company/new-api-gateway/internal/routes"
	"github.com/your-company/new-api-gateway/internal/traces"
)

const shutdownTimeout = 10 * time.Second

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, log.Default()); err != nil {
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

	handler := buildHTTPHandler(cfg, pool, redisClient, logger)
	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
	}
	logger.Printf("audit gateway listening on %s", cfg.ListenAddr)
	if err := serveUntilContext(ctx, server, shutdownTimeout); err != nil {
		return err
	}
	return nil
}

func serveUntilContext(ctx context.Context, server *http.Server, shutdownTimeout time.Duration) error {
	addr := server.Addr
	if addr == "" {
		addr = ":http"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	errc := make(chan error, 1)
	go func() {
		errc <- server.Serve(listener)
	}()

	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
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

func buildHTTPHandler(cfg config.Config, pool *pgxpool.Pool, redisClient *redis.Client, logger *log.Logger) http.Handler {
	gatewayHandler := buildHandler(cfg, pool, redisClient, logger)
	uiHandler := adminui.Handler()
	if pool == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isAdminAPIPath(r.URL.Path) {
				http.Error(w, "admin database unavailable", http.StatusServiceUnavailable)
				return
			}
			if isAdminUIPath(r.URL.Path) {
				uiHandler.ServeHTTP(w, r)
				return
			}
			gatewayHandler.ServeHTTP(w, r)
		})
	}

	adminRepo := admin.NewRepository(pool)
	adminAuth := admin.Auth{
		Repo:          adminRepo,
		SessionSecret: cfg.AdminSessionSecret,
		CookieName:    cfg.AdminCookieName,
		CookieSecure:  cfg.AdminCookieSecure,
	}
	adminHandler := admin.NewHandler(admin.HandlerConfig{
		Repo:          adminRepo,
		Auth:          adminAuth,
		AuditSecret:   cfg.AuditHMACSecret,
		EvidenceStore: evidence.NewFilesystemStore(cfg.EvidenceStorageDir),
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAdminAPIPath(r.URL.Path) {
			adminHandler.ServeHTTP(w, r)
			return
		}
		if isAdminUIPath(r.URL.Path) {
			uiHandler.ServeHTTP(w, r)
			return
		}
		gatewayHandler.ServeHTTP(w, r)
	})
}

func isAdminAPIPath(path string) bool {
	return path == "/admin/api" || strings.HasPrefix(path, "/admin/api/")
}

func isAdminUIPath(path string) bool {
	return path == "/admin" || strings.HasPrefix(path, "/admin/")
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
