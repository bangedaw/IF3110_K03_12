package postgres

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
)

const (
	defaultPort     = 5432
	poolMaxConns    = 5
	connectTimeout  = 5 * time.Second
	applicationName = "dbmp"
)

type Driver struct{}

func (Driver) Kind() string { return dbms.KindPostgres }

func (Driver) Open(ctx context.Context, cfg dbms.ConnConfig) (dbms.Conn, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn(cfg))
	if err != nil {
		return nil, sanitize(cfg, errors.New("konfigurasi koneksi postgres tidak valid"))
	}
	poolCfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	poolCfg.ConnConfig.ConnectTimeout = connectTimeout
	poolCfg.MaxConns = poolMaxConns
	poolCfg.MinConns = 0

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, sanitize(cfg, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, sanitize(cfg, err)
	}
	return &Conn{pool: pool}, nil
}

// Conn memegang pool pgx ke satu database target.
type Conn struct {
	pool *pgxpool.Pool
}

func (c *Conn) Ping(ctx context.Context) error { return c.pool.Ping(ctx) }

func (c *Conn) Close() error {
	c.pool.Close()
	return nil
}

func (c *Conn) Inspector() dbms.Inspector { return &Inspector{pool: c.pool} }
func (c *Conn) Browser() dbms.DataBrowser { return &Browser{pool: c.pool} }

// TODO(rhino): ganti dengan Migrator asli saat migrator.go masuk.
func (c *Conn) Migrator() dbms.Migrator { return dbms.UnimplementedMigrator(true) }

// TODO(rhino): ganti dengan Explainer asli saat explain.go masuk.
func (c *Conn) Explainer() dbms.Explainer { return dbms.UnimplementedExplainer() }

func dsn(cfg dbms.ConnConfig) string {
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	ssl := cfg.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(port)),
		Path:   "/" + cfg.Database,
	}
	q := u.Query()
	q.Set("sslmode", ssl)
	u.RawQuery = q.Encode()
	return u.String()
}

// sanitize membuang DSN/password dari pesan error driver. Error asli tidak
// dibungkus supaya rantai error yang membawa DSN tidak bisa diakses oleh
// siapa pun (log, response, audit).
func sanitize(cfg dbms.ConnConfig, err error) error {
	msg := err.Error()
	for _, secret := range []string{cfg.Password, url.QueryEscape(cfg.Password), url.PathEscape(cfg.Password)} {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "***")
		}
	}
	return errors.New(msg)
}

func init() { dbms.Register(Driver{}) }
