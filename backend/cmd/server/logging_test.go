package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/telemetry"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func tracedPlatformDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := testutil.NewPlatformDB(t).Config()
	cfg.ConnConfig.Tracer = sqltrace.Tracer{Target: "platform"}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func insertUser(t *testing.T, pool *pgxpool.Pool, username, password string, admin bool) int64 {
	t.Helper()
	hash, err := auth.HashPassword(password)
	require.NoError(t, err)
	var id int64
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, is_platform_admin) VALUES ($1, $2, $3) RETURNING id`,
		username, hash, admin).Scan(&id))
	return id
}

func loginAs(t *testing.T, h http.Handler, username, password string) *client {
	t.Helper()
	c := &client{t: t, h: h}
	rec := c.do(http.MethodPost, "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, username, password))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var s apigen.AuthSession
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &s))
	c.csrf = s.CsrfToken
	return c
}

func createdID(t *testing.T, rec interface{ Result() *http.Response }) int64 {
	t.Helper()
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.StatusCode, string(body))
	var out struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	return out.ID
}

func connectionBody(name, host string, port int, database, username, password string) string {
	return fmt.Sprintf(`{"name":%q,"kind":"postgres","host":%q,"port":%d,"databaseName":%q,"username":%q,"password":%q,"sslMode":"disable"}`,
		name, host, port, database, username, password)
}

// TestLogsAndSpansNeverContainSecrets memasang log dan tracing persis seperti main: handler log telemetry dan
// exporter console OpenTelemetry. Keduanya diperiksa setelah alur yang menyentuh password, cookie session,
// token CSRF, DSN, dan query string.
func TestLogsAndSpansNeverContainSecrets(t *testing.T) {
	logs := &syncBuffer{}
	logger := slog.New(telemetry.NewHandler(slog.NewJSONHandler(logs, nil)))
	prev := slog.Default()
	slog.SetDefault(logger)
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		slog.SetDefault(prev)
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	spans := &syncBuffer{}
	flushSpans, err := telemetry.Setup("dbmp-test", telemetry.ExporterConsole, spans)
	require.NoError(t, err)

	pool := tracedPlatformDB(t)
	live := testutil.NewPostgresTarget(t).Config().ConnConfig
	insertUser(t, pool, "admin", "LEAK-admin-pw", true)
	aliceID := insertUser(t, pool, "alice", "LEAK-alice-pw", false)

	cfg := config.Config{
		SecretKey: "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8=",
		CSRFKey:   "test-csrf-key",
		Session: config.Session{
			IdleTimeout: 30 * time.Minute, AbsoluteTimeout: 8 * time.Hour,
			RotationInterval: 15 * time.Minute, GracePeriod: 30 * time.Second, TouchInterval: time.Minute,
		},
	}
	h, cleanup, err := newHandler(cfg, pool, logger, fstest.MapFS{"index.html": {Data: []byte("x")}})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	anon := &client{t: t, h: h}
	assert.Equal(t, http.StatusUnauthorized, anon.do(http.MethodPost, "/api/auth/login", `{"username":"hantu","password":"LEAK-login-pw"}`).Code)
	assert.Equal(t, http.StatusUnauthorized, anon.do(http.MethodPost, "/api/auth/login", `{"username":"alice","password":"LEAK-wrong-pw"}`).Code)
	stale := &client{t: t, h: h, cookie: &http.Cookie{Name: auth.CookieName, Value: "LEAK-cookie"}}
	assert.Equal(t, http.StatusUnauthorized, stale.do(http.MethodGet, "/api/auth/me?next=LEAK-query", "").Code)

	admin := loginAs(t, h, "admin", "LEAK-admin-pw")
	alice := loginAs(t, h, "alice", "LEAK-alice-pw")

	csrf := admin.csrf
	admin.csrf = "LEAK-bad-csrf"
	assert.Equal(t, http.StatusForbidden, admin.do(http.MethodPost, "/api/admin/databases", connectionBody("x", "127.0.0.1", 1, "x", "x", "LEAK-csrf-body-pw")).Code)
	admin.csrf = csrf

	admin.do(http.MethodPost, "/api/admin/databases/test", connectionBody("mati", "127.0.0.1", 1, "app", "dbmp", "LEAK-conn-pw"))
	deadID := createdID(t, admin.do(http.MethodPost, "/api/admin/databases", connectionBody("mati", "127.0.0.1", 1, "app", "dbmp", "LEAK-stored-pw")))
	liveID := createdID(t, admin.do(http.MethodPost, "/api/admin/databases",
		connectionBody("hidup", live.Host, int(live.Port), live.Database, live.User, live.Password)))

	userID := createdID(t, admin.do(http.MethodPost, "/api/admin/users", `{"username":"loguser","password":"LEAK-user-pw"}`))
	admin.do(http.MethodPost, "/api/admin/users/"+strconv.FormatInt(userID, 10)+"/password", `{"password":"LEAK-reset-pw"}`)

	groupID := createdID(t, admin.do(http.MethodPost, "/api/admin/groups", fmt.Sprintf(`{"name":"tim","leaderId":%d}`, aliceID)))
	group := "/api/admin/groups/" + strconv.FormatInt(groupID, 10)
	require.Equal(t, http.StatusNoContent, admin.do(http.MethodPut, group+"/members/"+strconv.FormatInt(aliceID, 10), "").Code)
	for _, id := range []int64{deadID, liveID} {
		rec := admin.do(http.MethodPut, group+"/grants/"+strconv.FormatInt(id, 10), `{"level":"view"}`)
		require.Less(t, rec.Code, 300, rec.Body.String())
	}

	dead := "/api/databases/" + strconv.FormatInt(deadID, 10)
	alive := "/api/databases/" + strconv.FormatInt(liveID, 10)
	alice.do(http.MethodGet, "/api/databases?filter=LEAK-query", "")
	alice.do(http.MethodGet, dead+"/schemas", "")
	require.Equal(t, http.StatusOK, alice.do(http.MethodGet, alive+"/schemas/app/tables/books/rows?q=LEAK-query", "").Code)
	alice.do(http.MethodPost, dead+"/schema-diff", fmt.Sprintf(`{"against":{"type":"database","id":%d},"direction":"toTarget"}`, liveID))

	require.NoError(t, flushSpans(context.Background()))
	out := logs.String()
	require.NotEmpty(t, out)
	assert.Contains(t, out, "/api/databases")
	assert.Contains(t, out, `"target":"platform"`)
	assert.Contains(t, out, `"target":"target"`)
	assert.Contains(t, out, `"trace_id"`, "log membawa trace_id")
	traced := spans.String()
	assert.Contains(t, traced, `"Name":"POST /api/auth/login"`, "span HTTP ikut diperiksa")
	assert.Contains(t, traced, `"db.query.text"`, "span SQL ikut diperiksa")
	secrets := []string{
		"LEAK-login-pw", "LEAK-wrong-pw", "LEAK-cookie", "LEAK-query", "LEAK-admin-pw", "LEAK-alice-pw",
		"LEAK-bad-csrf", "LEAK-csrf-body-pw", "LEAK-conn-pw", "LEAK-stored-pw", "LEAK-user-pw", "LEAK-reset-pw",
		admin.cookie.Value, alice.cookie.Value, admin.csrf, alice.csrf,
	}
	for _, s := range secrets {
		assert.NotContains(t, out, s, "log tidak boleh memuat %q", s)
		assert.NotContains(t, traced, s, "span tidak boleh memuat %q", s)
	}
}
