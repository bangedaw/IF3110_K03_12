package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
)

type demoConnection struct {
	Name     string
	Kind     string
	Port     int
	Database string
}

type demoGroup struct {
	Name        string
	Description string
	Members     []string
	Grants      map[string]string
}

var demoConnections = []demoConnection{
	{Name: "inventory", Kind: "postgres", Port: 5433, Database: "inventory"},
	{Name: "staging", Kind: "postgres", Port: 5434, Database: "inventory"},
	{Name: "sales", Kind: "mysql", Port: 3307, Database: "sales"},
}

var demoUsers = []struct{ Username, DisplayName string }{
	{"alice", "Alice (backend)"},
	{"bob", "Bob (analyst)"},
	{"carol", "Carol (backend dan analyst)"},
}

var demoGroups = []demoGroup{
	{
		Name:        "backend-team",
		Description: "Edit pada inventory, lihat staging",
		Members:     []string{"alice", "carol"},
		Grants:      map[string]string{"inventory": "edit", "staging": "view"},
	},
	{
		Name:        "analyst",
		Description: "Lihat inventory dan sales",
		Members:     []string{"bob", "carol"},
		Grants:      map[string]string{"inventory": "view", "sales": "view"},
	},
}

type demoSummary struct {
	Users       int
	Groups      int
	Connections int
	Grants      int
}

func runSeedDemo(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("seed-demo", flag.ContinueOnError)
	password := fs.String("password", "demo12345", "password semua akun demo (hanya untuk development)")
	host := fs.String("host", "localhost", "host database target")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProduction() {
		return errors.New("seed-demo ditolak saat APP_ENV=production")
	}
	cipher, err := secret.New(cfg.SecretKey)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := platformdb.Migrate(ctx, pool); err != nil {
		return err
	}

	summary, err := seedDemo(ctx, pool, cipher, *password, *host)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Data demo siap: %d user, %d group, %d koneksi, %d grant\n"+
		"Akun: admin, alice, bob, carol (password sama: %q)\n", summary.Users, summary.Groups, summary.Connections, summary.Grants, *password)
	return err
}

func seedDemo(ctx context.Context, pool *pgxpool.Pool, cipher *secret.Cipher, password, host string) (demoSummary, error) {
	var summary demoSummary
	host = strings.TrimSpace(host)
	if host == "" {
		return summary, errors.New("host wajib diisi")
	}
	adminID, err := createAdmin(ctx, pool, "admin", password)
	if err != nil {
		return summary, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return summary, err
	}

	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		userIDs := map[string]int64{}
		for _, u := range demoUsers {
			var id int64
			if err := tx.QueryRow(ctx, `
				INSERT INTO users (username, display_name, password_hash, is_platform_admin, is_active)
				VALUES ($1, $2, $3, FALSE, TRUE)
				ON CONFLICT (username) DO UPDATE
				SET display_name = EXCLUDED.display_name, password_hash = EXCLUDED.password_hash, is_active = TRUE, updated_at = now()
				RETURNING id`, u.Username, u.DisplayName, hash).Scan(&id); err != nil {
				return fmt.Errorf("user %s: %w", u.Username, err)
			}
			userIDs[u.Username] = id
			summary.Users++
		}

		databaseIDs := map[string]int64{}
		for _, c := range demoConnections {
			var id int64
			if err := tx.QueryRow(ctx, `
				INSERT INTO database_connections (name, kind, host, port, database_name, username, password_ciphertext, ssl_mode, is_active, created_by)
				VALUES ($1, $2::dbms_kind, $3, $4, $5, 'dbmp', $6, 'disable', TRUE, $7)
				ON CONFLICT (name) DO UPDATE
				SET kind = EXCLUDED.kind, host = EXCLUDED.host, port = EXCLUDED.port, database_name = EXCLUDED.database_name,
				    username = EXCLUDED.username, password_ciphertext = EXCLUDED.password_ciphertext, is_active = TRUE, updated_at = now()
				RETURNING id`, c.Name, c.Kind, host, int32(c.Port), c.Database, cipher.Encrypt([]byte("dbmp")), adminID).Scan(&id); err != nil {
				return fmt.Errorf("koneksi %s: %w", c.Name, err)
			}
			databaseIDs[c.Name] = id
			summary.Connections++
		}

		for _, g := range demoGroups {
			var groupID int64
			if err := tx.QueryRow(ctx, `
				INSERT INTO groups (name, description) VALUES ($1, $2)
				ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description
				RETURNING id`, g.Name, g.Description).Scan(&groupID); err != nil {
				return fmt.Errorf("group %s: %w", g.Name, err)
			}
			summary.Groups++
			for _, member := range g.Members {
				if _, err := tx.Exec(ctx, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, groupID, userIDs[member]); err != nil {
					return fmt.Errorf("member %s: %w", member, err)
				}
			}
			for database, level := range g.Grants {
				if _, err := tx.Exec(ctx, `
					INSERT INTO group_database_grants (group_id, database_id, level) VALUES ($1, $2, $3::access_level)
					ON CONFLICT (group_id, database_id) DO UPDATE SET level = EXCLUDED.level`, groupID, databaseIDs[database], level); err != nil {
					return fmt.Errorf("grant %s/%s: %w", g.Name, database, err)
				}
				summary.Grants++
			}
		}
		return nil
	})
	summary.Users++
	return summary, err
}
