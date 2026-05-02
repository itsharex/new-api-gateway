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
	"github.com/your-company/new-api-gateway/internal/ops"
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

	newAPIPool, err := pgxpool.New(ctx, cfg.NewAPIPostgresDSN)
	if err != nil {
		return err
	}
	defer newAPIPool.Close()

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer redisClient.Close()

	handler := buildHTTPHandler(cfg, pool, newAPIPool, redisClient, logger)
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

func buildHandler(cfg config.Config, pool *pgxpool.Pool, newAPIPool *pgxpool.Pool, redisClient *redis.Client, logger *log.Logger) gateway.Handler {
	return gateway.Handler{
		UpstreamBaseURL: cfg.NewAPIBaseURL,
		Registry:        routes.DefaultRegistry(),
		EvidenceStore:   evidence.NewFilesystemStore(cfg.EvidenceStorageDir),
		TraceRepo:       traces.NewPostgresRepository(pool),
		IdentityResolver: identity.Resolver{
			Cache: identity.ChainCache{Caches: []identity.Cache{
				identity.RedisCache{Client: redisClient},
				identity.PostgresCache{DB: pool},
			}},
			Lookup:            identity.NewAPILookup{Pool: newAPIPool},
		},
		AuditSecret:     cfg.AuditHMACSecret,
		AuditError:      auditErrorLogger(logger),
		JobPublisher:    jobs.NewRedisListPublisher(redisClient, jobs.DefaultRedisListName),
		CoverageEmitter: alerts.NewPostgresRepository(pool),
		Spool:           gateway.NewFilesystemSpool(cfg.DegradedSpoolDir),
	}
}

func buildHTTPHandler(cfg config.Config, pool *pgxpool.Pool, newAPIPool *pgxpool.Pool, redisClient *redis.Client, logger *log.Logger) http.Handler {
	gatewayHandler := buildHandler(cfg, pool, newAPIPool, redisClient, logger)
	uiHandler := adminui.Handler()
	opsHandler := buildOpsHandler(cfg, pool, redisClient)
	if pool == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isOpsPath(r.URL.Path) {
				opsHandler.ServeHTTP(w, r)
				return
			}
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
		if isOpsPath(r.URL.Path) {
			opsHandler.ServeHTTP(w, r)
			return
		}
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

func buildOpsHandler(cfg config.Config, pool *pgxpool.Pool, redisClient *redis.Client) http.Handler {
	service := ops.Service{
		StartedAt: time.Now().UTC(),
		Now:       time.Now,
		PostgresCheck: func(ctx context.Context) error {
			if pool == nil {
				return errors.New("postgres pool is nil")
			}
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			return pool.Ping(ctx)
		},
		RedisCheck: func(ctx context.Context) error {
			if redisClient == nil {
				return errors.New("redis client is nil")
			}
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			return redisClient.Ping(ctx).Err()
		},
		EvidenceCheck: func(ctx context.Context) error {
			store := evidence.NewFilesystemStore(cfg.EvidenceStorageDir)
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			object, err := store.Put(ctx, evidence.PutRequest{
				TraceID:     "ops_healthcheck",
				ObjectType:  "readiness",
				ContentType: "text/plain",
				Reader:      strings.NewReader("ok"),
			})
			if err != nil {
				return err
			}
			reader, err := store.Get(ctx, object.ObjectRef)
			if err != nil {
				return err
			}
			return reader.Close()
		},
		WorkerHeartbeatCheck: func(ctx context.Context) (ops.WorkerHeartbeatStatus, error) {
			if pool == nil {
				return ops.WorkerHeartbeatStatus{}, errors.New("postgres pool is nil")
			}
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			var status ops.WorkerHeartbeatStatus
			status.MaxAge = cfg.OpsWorkerHeartbeatMaxAge
			err := pool.QueryRow(ctx, `
SELECT COALESCE(MAX(last_seen_at), to_timestamp(0)), COUNT(*)
FROM worker_heartbeats
WHERE worker_kind = 'analysis'`).Scan(&status.LastSeenAt, &status.WorkerCount)
			return status, err
		},
		QueueLagCheck: func(ctx context.Context) (ops.QueueLagStatus, error) {
			status := ops.QueueLagStatus{QueueName: jobs.DefaultRedisListName, WarnThreshold: cfg.OpsQueueLagWarnThreshold}
			if redisClient == nil {
				return status, errors.New("redis client is nil")
			}
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			depth, err := redisClient.LLen(ctx, jobs.DefaultRedisListName).Result()
			status.Depth = depth
			return status, err
		},
		RuntimeMetricsCheck: func(ctx context.Context) (ops.RuntimeMetrics, error) {
			metrics := ops.RuntimeMetrics{IdentityStatuses: map[string]int64{}}
			if pool == nil {
				return metrics, errors.New("postgres pool is nil")
			}
			ctx, cancel := context.WithTimeout(ctx, cfg.OpsCheckTimeout)
			defer cancel()
			err := pool.QueryRow(ctx, `
SELECT
  COUNT(*),
  COUNT(*) FILTER (WHERE error_type = 'capture_degraded'),
  COUNT(*) FILTER (WHERE route_support_level IN ('raw_only','raw_minimal')),
  (SELECT COUNT(*) FROM coverage_alerts WHERE status = 'open'),
  (SELECT COUNT(*) FROM usage_anomalies WHERE status = 'open')
FROM traces
WHERE created_at >= now() - interval '24 hours'`).Scan(
				&metrics.RequestCount,
				&metrics.CaptureFailureCount,
				&metrics.RawOnlyRouteCount,
				&metrics.CoverageOpenCount,
				&metrics.AnomalyOpenCount,
			)
			if err != nil {
				return metrics, err
			}

			rows, err := pool.Query(ctx, `
SELECT identity_resolution_status, COUNT(*)
FROM traces
WHERE created_at >= now() - interval '24 hours'
GROUP BY identity_resolution_status`)
			if err != nil {
				return metrics, err
			}
			defer rows.Close()

			for rows.Next() {
				var status string
				var count int64
				if err := rows.Scan(&status, &count); err != nil {
					return metrics, err
				}
				metrics.IdentityStatuses[status] = count
			}
			if err := rows.Err(); err != nil {
				return metrics, err
			}
			return metrics, nil
		},
	}
	return ops.Handler(service, cfg.OpsMetricsEnabled)
}

func isOpsPath(path string) bool {
	return path == "/healthz" || path == "/readyz" || path == "/metrics"
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
