package mysql

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
)

const (
	defaultPort    = 3306
	poolMaxConns   = 5
	connectTimeout = 5 * time.Second
)

type Driver struct{}

func (Driver) Kind() string { return dbms.KindMySQL }

func (Driver) Open(ctx context.Context, cfg dbms.ConnConfig) (dbms.Conn, error) {
	mc := configFor(cfg)

	db, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		return nil, sanitize(cfg, err)
	}
	db.SetMaxOpenConns(poolMaxConns)
	db.SetMaxIdleConns(0)
	db.SetConnMaxLifetime(time.Hour)

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, sanitize(cfg, err)
	}
	return &Conn{db: db, database: cfg.Database}, nil
}

type Conn struct {
	db *sql.DB

	database string
}

func (c *Conn) Ping(ctx context.Context) error { return c.db.PingContext(ctx) }

func (c *Conn) Close() error { return c.db.Close() }

func (c *Conn) Inspector() dbms.Inspector { return &Inspector{db: c.db, database: c.database} }
func (c *Conn) Browser() dbms.DataBrowser { return &Browser{db: c.db, database: c.database} }

func (c *Conn) Migrator() dbms.Migrator { return Migrator{DB: c.db} }

func (c *Conn) Explainer() dbms.Explainer { return Explainer{DB: c.db} }

func configFor(cfg dbms.ConnConfig) *mysql.Config {
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	mc := mysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = cfg.Password
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	mc.DBName = cfg.Database
	mc.Timeout = connectTimeout
	mc.ReadTimeout = 30 * time.Second
	mc.WriteTimeout = 30 * time.Second
	mc.ParseTime = true

	if cfg.SSLMode != "" && cfg.SSLMode != "disable" {
		mc.TLSConfig = cfg.SSLMode
	}
	return mc
}

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
