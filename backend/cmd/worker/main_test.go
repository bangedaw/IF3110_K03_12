package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

func TestWorkerRunsQueuedJobAndStops(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	target := testutil.NewPostgresTarget(t)
	t.Cleanup(target.Close)
	cfg := config.Config{SecretKey: "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8=", WorkerConcurrency: 2}
	cipher, err := secret.New(cfg.SecretKey)
	require.NoError(t, err)

	conn := target.Config().ConnConfig
	var user, group, database, migrationID, request int64
	scan := func(dst *int64, sql string, args ...any) {
		t.Helper()
		require.NoError(t, pool.QueryRow(ctx, sql, args...).Scan(dst))
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	scan(&user, `INSERT INTO users (username, password_hash) VALUES ('u', 'x') RETURNING id`)
	scan(&group, `INSERT INTO groups (name) VALUES ('g') RETURNING id`)
	exec(`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, group, user)
	exec(`UPDATE groups SET leader_user_id = $2 WHERE id = $1`, group, user)
	scan(&database, `INSERT INTO database_connections (name, kind, host, port, database_name, username, password_ciphertext)
		VALUES ('d', 'postgres', $1, $2, $3, $4, $5) RETURNING id`, conn.Host, int(conn.Port), conn.Database, conn.User, cipher.Encrypt([]byte(conn.Password)))
	exec(`INSERT INTO group_database_grants (group_id, database_id, level) VALUES ($1, $2, 'edit')`, group, database)
	scan(&migrationID, `INSERT INTO migrations (database_id, title, sql_text, down_sql, checksum, created_by, updated_by)
		VALUES ($1, 't', 'CREATE TABLE app.worker_made (id int)', 'DROP TABLE app.worker_made', '\x00', $2, $2) RETURNING id`, database, user)
	scan(&request, `INSERT INTO migration_requests (migration_id, database_id, kind, revision, status, requested_by)
		VALUES ($1, $2, 'apply', 1, 'queued', $3) RETURNING id`, migrationID, database, user)
	exec(`INSERT INTO migration_approvals (request_id, group_id, approved, decided_by) VALUES ($1, $2, TRUE, $3)`, request, group, user)

	w, cleanup, err := newWorker(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(cleanup)
	w.PollInterval = 10 * time.Millisecond
	_, err = queue.NewPostgres(pool).Publish(ctx, queue.PublishInput{RequestID: request, DatabaseID: database, ActorID: user})
	require.NoError(t, err)

	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(runCtx)
	}()
	assert.Eventually(t, func() bool {
		var status string
		_ = pool.QueryRow(ctx, `SELECT status::text FROM jobs WHERE request_id = $1`, request).Scan(&status)
		return status == "succeeded"
	}, 20*time.Second, 20*time.Millisecond)
	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker tidak berhenti")
	}
	var exists bool
	require.NoError(t, target.QueryRow(ctx, `SELECT to_regclass('app.worker_made') IS NOT NULL`).Scan(&exists))
	assert.True(t, exists)
}
