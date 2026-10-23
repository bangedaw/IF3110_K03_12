package main

import (
	"context"
	"errors"
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
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"

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
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := platformdb.Migrate(ctx, pool); err != nil {
		return err
	}

	handler, cleanup, err := newHandler(cfg, pool, logger)
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

func newHandler(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (http.Handler, func(), error) {
	cipher, err := secret.New(cfg.SecretKey)
	if err != nil {
		return nil, nil, err
	}
	store := &dbconn.SQLStore{Q: gen.New(pool)}
	registry := dbconn.NewRegistry(store, cipher)
	auditLog := audit.New(pool)
	az := audit.RecordDenied(authz.NewDBAuthorizer(pool), auditLog)
	authSvc := auth.NewService(pool, cfg.Session, cfg.CSRFKey, cfg.IsProduction())

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
			admin.Routes(p, admin.NewService(pool, az, auditLog))
			dbconn.Routes(p, dbconn.NewService(store, registry, cipher, az))
			explorer.Routes(p, explorer.NewService(store, registry, az))
		})
	})
	return r, registry.Close, nil
}
