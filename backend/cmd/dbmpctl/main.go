package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

const usage = `usage: dbmpctl create-admin [-username admin] < password

Membuat Platform Admin, atau mereset password + mengaktifkan kembali admin yang sudah ada.
Password dibaca dari stdin (tersembunyi jika dari terminal), minimal 8 karakter.`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdin *os.File, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "create-admin" {
		fmt.Fprintln(os.Stderr, usage)
		return errors.New("perintah tidak dikenal")
	}
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	username := fs.String("username", "admin", "username admin")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	password, err := readPassword(stdin, stdout)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := platformdb.Migrate(ctx, pool); err != nil {
		return err
	}

	id, err := createAdmin(ctx, pool, *username, password)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Platform Admin %q siap (id %d)\n", *username, id)
	return err
}

func readPassword(stdin *os.File, stdout io.Writer) (string, error) {
	if term.IsTerminal(int(stdin.Fd())) {
		_, _ = fmt.Fprint(stdout, "Password: ")
		b, err := term.ReadPassword(int(stdin.Fd()))
		_, _ = fmt.Fprintln(stdout)
		return string(b), err
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func createAdmin(ctx context.Context, pool *pgxpool.Pool, username, password string) (int64, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, errors.New("username wajib diisi")
	}
	if len(password) < 8 {
		return 0, errors.New("password minimal 8 karakter")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	var id int64
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		id, err = q.UpsertPlatformAdmin(ctx, gen.UpsertPlatformAdminParams{Username: username, PasswordHash: hash})
		if err != nil {
			return err
		}
		return q.RevokeUserSessions(ctx, gen.RevokeUserSessionsParams{UserID: id, Now: pgtype.Timestamptz{Time: time.Now(), Valid: true}})
	})
	return id, err
}
