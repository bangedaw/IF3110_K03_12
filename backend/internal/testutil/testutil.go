// Package testutil menyediakan container PostgreSQL/MySQL sungguhan untuk
// test integration, satu container per package (dibuat lazy lewat sync.Once)
// dan satu database terisolasi per panggilan helper.
//
// Fallback bila Docker tidak tersedia (lihat docs/risiko): set TEST_PG_URL
// atau TEST_MYSQL_URL ke database dari `make dev`; helper memakai database
// tersebut apa adanya sehingga isolasi antar test melemah.
package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	_ "github.com/go-sql-driver/mysql"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb"
)

const (
	postgresImage = "postgres:17"
	mysqlImage    = "mysql:8.4"
	pgUser        = "dbmp"
	pgPassword    = "dbmp"
	mysqlRootPass = "root"
	startTimeout  = 120 * time.Second
)

var (
	postgresOnce   sync.Once
	postgresErr    error
	postgresHostPG string

	mysqlOnce sync.Once
	mysqlErr  error
	mysqlHost string
	mysqlPort string
)

// NewPlatformDB mengembalikan pool ke platform DB segar yang sudah di-migrate.
func NewPlatformDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newPostgresDatabase(t, "")
	if err := platformdb.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate platform db: %v", err)
	}
	return pool
}

// NewPostgresTarget mengembalikan pool ke database PostgreSQL target segar
// dengan schema seed yang diketahui test (schema `app`).
func NewPostgresTarget(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newPostgresDatabase(t, pgTargetSchema)
	seedPostgres(t, pool)
	return pool
}

// NewMySQLTarget mengembalikan koneksi ke database MySQL target segar dengan
// schema seed yang diketahui test (tabel customers dan orders).
func NewMySQLTarget(t *testing.T) *sql.DB {
	t.Helper()
	if dsn := os.Getenv("TEST_MYSQL_URL"); dsn != "" {
		return openMySQL(t, dsn)
	}

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	host, port := mysqlCluster(t, ctx)

	name := uniqueName("t")
	// multiStatements: seed dieksekusi sebagai satu blob SQL berisi banyak statement.
	root := openMySQL(t, fmt.Sprintf("%s:%s@tcp(%s:%s)/mysql?parseTime=true&multiStatements=true", "root", mysqlRootPass, host, port))
	defer func() { _ = root.Close() }()
	if _, err := root.ExecContext(ctx, "CREATE DATABASE `"+name+"`"); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}

	db := openMySQL(t, fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&multiStatements=true", "root", mysqlRootPass, host, port, name))
	if mysqlTargetSchema != "" {
		if _, err := db.ExecContext(ctx, mysqlTargetSchema); err != nil {
			t.Fatalf("seed schema mysql: %v", err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newPostgresDatabase(t *testing.T, schema string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	if url := os.Getenv("TEST_PG_URL"); url != "" {
		pool := openPostgres(t, url)
		if schema != "" {
			applyPostgresSchema(t, pool, schema)
		}
		return pool
	}

	dsn := postgresCluster(t, ctx)
	name := uniqueName("t")

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect ke cluster postgres: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}

	pool := openPostgres(t, replaceDatabase(dsn, name))
	if schema != "" {
		applyPostgresSchema(t, pool, schema)
	}
	return pool
}

func applyPostgresSchema(t *testing.T, pool *pgxpool.Pool, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	if _, err := pool.Exec(ctx, schema); err != nil {
		t.Fatalf("terapkan schema seed postgres: %v", err)
	}
}

func seedPostgres(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	if _, err := pool.Exec(ctx, pgTargetSeed); err != nil {
		t.Fatalf("seed data postgres: %v", err)
	}
}

func openPostgres(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool postgres: %v", err)
	}
	pingCtx, pingCancel := context.WithTimeout(ctx, 30*time.Second)
	defer pingCancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		t.Fatalf("ping postgres (%s): %v", redact(dsn), err)
	}
	return pool
}

func openMySQL(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("buka mysql: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("ping mysql (%s): %v", redact(dsn), err)
	}
	return db
}

// postgresCluster mengembalikan DSN cluster PostgreSQL (dari container atau
// TEST_PG_URL). Satu container dibagikan seluruh test di package ini.
func postgresCluster(t *testing.T, ctx context.Context) string {
	t.Helper()
	postgresOnce.Do(func() {
		req := testcontainers.ContainerRequest{
			Image:        postgresImage,
			Env:          map[string]string{"POSTGRES_USER": pgUser, "POSTGRES_PASSWORD": pgPassword, "POSTGRES_DB": "postgres"},
			ExposedPorts: []string{"5432/tcp"},
			WaitingFor: wait.ForListeningPort("5432/tcp").
				WithStartupTimeout(startTimeout),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
		if err != nil {
			postgresErr = fmt.Errorf("jalankan container %s (Docker jalan?): %w", postgresImage, err)
			return
		}
		host, err := c.Host(ctx)
		if err != nil {
			postgresErr = err
			return
		}
		port, err := c.MappedPort(ctx, "5432/tcp")
		if err != nil {
			postgresErr = err
			return
		}
		postgresHostPG = fmt.Sprintf("postgres://%s:%s@%s:%s/postgres?sslmode=disable", pgUser, pgPassword, host, port.Port())
	})
	if postgresErr != nil {
		t.Skipf("test integration butuh Docker: %v", postgresErr)
	}
	return postgresHostPG
}

func mysqlCluster(t *testing.T, ctx context.Context) (string, string) {
	t.Helper()
	mysqlOnce.Do(func() {
		req := testcontainers.ContainerRequest{
			Image:        mysqlImage,
			Env:          map[string]string{"MYSQL_ROOT_PASSWORD": mysqlRootPass, "MYSQL_DATABASE": "mysql"},
			ExposedPorts: []string{"3306/tcp"},
			WaitingFor: wait.ForListeningPort("3306/tcp").
				WithStartupTimeout(startTimeout),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
		if err != nil {
			mysqlErr = fmt.Errorf("jalankan container %s (Docker jalan?): %w", mysqlImage, err)
			return
		}
		host, err := c.Host(ctx)
		if err != nil {
			mysqlErr = err
			return
		}
		port, err := c.MappedPort(ctx, "3306/tcp")
		if err != nil {
			mysqlErr = err
			return
		}
		mysqlHost, mysqlPort = host, port.Port()
	})
	if mysqlErr != nil {
		t.Skipf("test integration butuh Docker: %v", mysqlErr)
	}
	return mysqlHost, mysqlPort
}

func uniqueName(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func replaceDatabase(dsn, name string) string {
	// DSN selalu kita yang bentuk, jadi pergantian database cukup mengganti
	// path setelah host:port.
	for i := len(dsn) - 1; i >= 0; i-- {
		if dsn[i] == '/' {
			return dsn[:i+1] + name
		}
	}
	return dsn
}

func redact(dsn string) string {
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == '@' {
			return "postgres://***@" + dsn[i+1:]
		}
		if dsn[i] == ':' && i > 0 && dsn[i-1] != '/' {
			return dsn[:i] + ":***" + dsn[i:]
		}
	}
	return dsn
}
