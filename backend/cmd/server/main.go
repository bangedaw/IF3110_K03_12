package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/admin"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/explorer"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/spa"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/telemetry"

	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/mysql"
	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(telemetry.NewHandler(slog.NewJSONHandler(os.Stdout, nil)))
	slog.SetDefault(logger)
	// Span ditulis ke stderr supaya stdout tetap berisi log saja.
	shutdownTracing, err := telemetry.Setup("dbmp-server", cfg.TracesExporter, os.Stderr)
	if err != nil {
		return err
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(flushCtx); err != nil {
			logger.Error("flush span", "err", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	poolCfg.ConnConfig.Tracer = sqltrace.Tracer{Target: "platform"}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := platformdb.Migrate(ctx, pool); err != nil {
		return err
	}

	handler, cleanup, err := newHandler(cfg, pool, logger, spa.Dist())
	if err != nil {
		return err
	}
	defer cleanup()

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func newHandler(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger, ui fs.FS) (http.Handler, func(), error) {
	cipher, err := secret.New(cfg.SecretKey)
	if err != nil {
		return nil, nil, err
	}
	store := &dbconn.SQLStore{Q: gen.New(pool)}
	registry := dbconn.NewRegistry(store, cipher)
	auditLog := audit.New(pool)
	az := audit.RecordDenied(authz.NewDBAuthorizer(pool), auditLog)
	authSvc := auth.NewService(pool, cfg.Session, cfg.CSRFKey, cfg.IsProduction())
	connSvc := dbconn.NewService(store, registry, cipher, az)
	explorerSvc := explorer.NewService(store, registry, az)
	adminSvc := admin.NewService(pool, az, auditLog)
	migrationRepo := migration.NewRepository(pool)
	migrationTargets := dbconn.Targets{Registry: registry}
	reconcileCtx, cancelReconcile := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReconcile()
	if err := (migration.Reconciler{Repo: migrationRepo, Targets: migrationTargets, Logger: logger}).Reconcile(reconcileCtx); err != nil {
		registry.Close()
		return nil, nil, err
	}

	r := httpx.NewRouter(logger)
	r.Route("/api", func(api chi.Router) {
		api.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
			if err := pool.Ping(r.Context()); err != nil {
				httpx.Error(w, r, apperr.Wrap(apperr.CodeDBUnreachable, "platform DB tidak terjangkau", err))
				return
			}
			httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})

		auth.Routes(api, authSvc)

		api.Group(func(p chi.Router) {
			p.Use(authSvc.Middleware)
			admin.Routes(p, adminSvc)
			dbconn.Routes(p, connSvc)
			explorer.Routes(p, explorerSvc)
			queue.Routes(p, queue.Handler{Jobs: queue.NewPostgres(pool), Authz: az})
		})
	})
	if h, ok := spa.Handler(ui); ok {
		r.Method(http.MethodGet, "/*", h)
		r.Method(http.MethodHead, "/*", h)
	} else {
		logger.Warn("frontend SPA belum di-build, hanya /api yang dilayani")
	}
	return r, registry.Close, nil
}
