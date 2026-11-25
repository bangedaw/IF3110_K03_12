package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/telemetry"

	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/mysql"
	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped", "err", err)
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
	shutdownTracing, err := telemetry.Setup("dbmp-worker", cfg.TracesExporter, os.Stderr)
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
	if err := pool.Ping(ctx); err != nil {
		return err
	}

	w, cleanup, err := newWorker(cfg, pool, logger)
	if err != nil {
		return err
	}
	defer cleanup()
	logger.Info("worker started", "concurrency", w.Concurrency, "env", cfg.Env)
	w.Run(ctx)
	return nil
}

func newWorker(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (*queue.Worker, func(), error) {
	cipher, err := secret.New(cfg.SecretKey)
	if err != nil {
		return nil, nil, err
	}
	registry := dbconn.NewRegistry(&dbconn.SQLStore{Q: gen.New(pool)}, cipher)
	jobs := queue.NewPostgres(pool)
	return &queue.Worker{
		Jobs:        jobs,
		Locks:       jobs,
		Lifecycle:   migration.Lifecycle{Pool: pool, Auth: authz.NewDBAuthorizer(pool), Audit: audit.New(pool)},
		Targets:     dbconn.Targets{Registry: registry},
		Logger:      logger,
		Concurrency: cfg.WorkerConcurrency,
	}, registry.Close, nil
}
