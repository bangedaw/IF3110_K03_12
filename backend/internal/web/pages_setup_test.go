package web_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/admin"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/explorer"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/web"

	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
)

const cipherKey = "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8="

const (
	adminID    int64 = 1
	viewerID   int64 = 42
	nobodyID   int64 = 43
	editorID   int64 = 44
	liveOnlyID int64 = 45
	diffViewID int64 = 46
)

type fakeGuard struct {
	mu      sync.Mutex
	loggedO []int64
}

func csrfFor(id int64) string { return fmt.Sprintf("csrf-%d", id) }

const testSessionCookie = "test_session"

var loginUsers = map[string]int64{"alice": viewerID, "admin": adminID}

func (g *fakeGuard) Login(w http.ResponseWriter, _ *http.Request, username, password string) error {
	fields := map[string]string{}
	if username == "" {
		fields["username"] = "wajib diisi"
	}
	if password == "" {
		fields["password"] = "wajib diisi"
	}
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}
	id, ok := loginUsers[username]
	if !ok || password != "rahasia123" {
		return apperr.New(apperr.CodeUnauthenticated, "username atau password salah")
	}
	http.SetCookie(w, &http.Cookie{Name: testSessionCookie, Value: fmt.Sprint(id), Path: "/", HttpOnly: true})
	return nil
}

func (g *fakeGuard) Authenticate(_ http.ResponseWriter, r *http.Request) (web.Identity, error) {
	if r.Header.Get("X-Test-Session") == "expired" {
		return web.Identity{}, apperr.New(apperr.CodeSessionExpired, "sesi berakhir")
	}
	raw := r.Header.Get("X-Test-User")
	if raw == "" {
		if c, err := r.Cookie(testSessionCookie); err == nil {
			raw = c.Value
		}
	}
	if raw == "" {
		return web.Identity{}, apperr.ErrUnauthenticated
	}
	var id int64
	if _, err := fmt.Sscan(raw, &id); err != nil {
		return web.Identity{}, apperr.ErrUnauthenticated
	}
	return web.Identity{
		Principal: authz.Principal{UserID: id, IsPlatformAdmin: id == adminID},
		Username:  fmt.Sprintf("user%d", id),
		CSRF:      csrfFor(id),
	}, nil
}

func (g *fakeGuard) Logout(_ http.ResponseWriter, _ *http.Request, id web.Identity) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loggedO = append(g.loggedO, id.Principal.UserID)
	return nil
}

type savedDraft struct {
	Principal  authz.Principal
	DatabaseID int64
	Draft      web.Draft
}

type fakeSaver struct {
	mu    sync.Mutex
	saved []savedDraft
}

func (f *fakeSaver) SaveDraft(_ context.Context, p authz.Principal, databaseID int64, d web.Draft) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, savedDraft{Principal: p, DatabaseID: databaseID, Draft: d})
	return int64(len(f.saved)), nil
}

func (f *fakeSaver) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

type env struct {
	t        *testing.T
	handler  http.Handler
	router   chi.Router
	srv      *web.Server
	platform *pgxpool.Pool
	target   *pgxpool.Pool
	staging  *pgxpool.Pool
	guard    *fakeGuard
	saver    *fakeSaver
	cipher   *secret.Cipher

	liveID     int64
	stagingID  int64
	inactiveID int64
	deadID     int64
	nograntID  int64
	mysqlID    int64
}

func newEnv(t *testing.T, withSaver bool) *env {
	t.Helper()

	platform := testutil.NewPlatformDB(t)
	t.Cleanup(platform.Close)
	target := testutil.NewPostgresTarget(t)
	t.Cleanup(target.Close)
	staging := testutil.NewPostgresTarget(t)
	t.Cleanup(staging.Close)

	cipher, err := secret.New(cipherKey)
	require.NoError(t, err)

	for _, id := range []int64{adminID, viewerID, nobodyID, editorID, liveOnlyID, diffViewID} {
		_, err := platform.Exec(context.Background(),
			`INSERT INTO users (id, username, password_hash, is_platform_admin) VALUES ($1, $2, 'x', $3) ON CONFLICT DO NOTHING`, id, fmt.Sprintf("user%d", id), id == adminID)
		require.NoError(t, err)
	}

	_, err = staging.Exec(context.Background(), `ALTER TABLE app.books DROP COLUMN tags; ALTER TABLE app.books ADD COLUMN extra TEXT`)
	require.NoError(t, err)

	_, err = platform.Exec(context.Background(), `SELECT setval(pg_get_serial_sequence('users', 'id'), 1000)`)
	require.NoError(t, err)

	e := &env{t: t, platform: platform, target: target, staging: staging, guard: &fakeGuard{}, cipher: cipher}
	targetCfg, stagingCfg := target.Config().ConnConfig, staging.Config().ConnConfig
	e.liveID = e.insertConnection("live", "postgres", targetCfg.Host, int(targetCfg.Port), targetCfg.Database, targetCfg.User, targetCfg.Password, true)
	e.stagingID = e.insertConnection("staging", "postgres", stagingCfg.Host, int(stagingCfg.Port), stagingCfg.Database, stagingCfg.User, stagingCfg.Password, true)
	e.inactiveID = e.insertConnection("inactive", "postgres", targetCfg.Host, int(targetCfg.Port), targetCfg.Database, targetCfg.User, targetCfg.Password, false)
	e.deadID = e.insertConnection("dead", "postgres", "127.0.0.1", 1, "x", "x", "apa-saja", true)
	e.nograntID = e.insertConnection("nogrant", "postgres", targetCfg.Host, int(targetCfg.Port), targetCfg.Database, targetCfg.User, targetCfg.Password, true)
	e.mysqlID = e.insertConnection("sales", "mysql", "127.0.0.1", 1, "sales", "x", "x", true)

	az := authz.NewFake().
		Grant(viewerID, e.liveID, authz.LevelView).
		Grant(viewerID, e.inactiveID, authz.LevelView).
		Grant(viewerID, e.deadID, authz.LevelView).
		Grant(editorID, e.liveID, authz.LevelEdit).
		Grant(editorID, e.stagingID, authz.LevelView).
		Grant(editorID, e.mysqlID, authz.LevelView).
		Grant(liveOnlyID, e.liveID, authz.LevelEdit).
		Grant(diffViewID, e.liveID, authz.LevelView).
		Grant(diffViewID, e.stagingID, authz.LevelView)

	store := &dbconn.SQLStore{Q: gen.New(platform)}
	registry := dbconn.NewRegistry(store, cipher)
	t.Cleanup(registry.Close)

	deps := web.Deps{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Guard:       e.guard,
		Authz:       az,
		Connections: dbconn.NewService(store, registry, cipher, az),
		Explorer:    explorer.NewService(store, registry, az),
		Admin:       admin.NewService(platform, az, audit.New(platform)),
	}
	if withSaver {
		e.saver = &fakeSaver{}
		deps.Drafts = e.saver
	}
	srv, err := web.New(deps)
	require.NoError(t, err)

	r := httpx.NewRouter(deps.Logger)
	srv.Mount(r)
	e.handler = r
	e.router = r
	e.srv = srv
	return e
}

func (e *env) insertConnection(name, kind, host string, port int, database, user, password string, active bool) int64 {
	e.t.Helper()
	var id int64
	err := e.platform.QueryRow(context.Background(), `
		INSERT INTO database_connections
			(name, kind, host, port, database_name, username, password_ciphertext, ssl_mode, is_active, created_by)
		VALUES ($1, $2::dbms_kind, $3, $4, $5, $6, $7, 'disable', $8, $9)
		RETURNING id`,
		name, kind, host, int32(port), database, user, e.cipher.Encrypt([]byte(password)), active, adminID,
	).Scan(&id)
	require.NoError(e.t, err)
	return id
}

func (e *env) ciphertext(name string) []byte {
	e.t.Helper()
	var raw []byte
	require.NoError(e.t, e.platform.QueryRow(context.Background(),
		`SELECT password_ciphertext FROM database_connections WHERE name = $1`, name).Scan(&raw))
	return raw
}

func (e *env) connectionCount(name string) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.platform.QueryRow(context.Background(),
		`SELECT count(*) FROM database_connections WHERE name = $1`, name).Scan(&n))
	return n
}

func (e *env) do(method, path string, user int64, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if user != 0 {
		req.Header.Set("X-Test-User", fmt.Sprint(user))
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func (e *env) get(path string, user int64) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(http.MethodGet, path, user, nil, nil)
}

func (e *env) post(path string, user int64, form url.Values) *httptest.ResponseRecorder {
	e.t.Helper()
	f := url.Values{}
	for k, v := range form {
		f[k] = v
	}
	f.Set("_csrf", csrfFor(user))
	return e.do(http.MethodPost, path, user, f, nil)
}

func (e *env) postNoCSRF(path string, user int64, form url.Values) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(http.MethodPost, path, user, form, nil)
}

func (e *env) follow(rec *httptest.ResponseRecorder, user int64) *httptest.ResponseRecorder {
	e.t.Helper()
	require.Equal(e.t, http.StatusSeeOther, rec.Code, rec.Body.String())
	return e.do(http.MethodGet, rec.Header().Get("Location"), user, nil, rec.Result().Cookies())
}

func dbPath(id int64, rest string) string { return fmt.Sprintf("/databases/%d%s", id, rest) }

func body(rec *httptest.ResponseRecorder) string { return rec.Body.String() }

func serve(e *env, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func validConnectionForm(name string) url.Values {
	return url.Values{
		"name":         {name},
		"kind":         {"postgres"},
		"host":         {"db.internal"},
		"port":         {"5432"},
		"databaseName": {"appdb"},
		"username":     {"reader"},
		"password":     {"rahasia-banget"},
		"sslMode":      {"disable"},
		"isActive":     {"on"},
	}
}

func (e *env) insertGroup(name string) int64 {
	e.t.Helper()
	var id int64
	require.NoError(e.t, e.platform.QueryRow(context.Background(),
		`INSERT INTO groups (name) VALUES ($1) RETURNING id`, name).Scan(&id))
	return id
}

func (e *env) insertUser(username string, active bool) int64 {
	e.t.Helper()
	var id int64
	require.NoError(e.t, e.platform.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, is_active) VALUES ($1, 'x', $2) RETURNING id`, username, active).Scan(&id))
	return id
}

func (e *env) scalar(query string, args ...any) string {
	e.t.Helper()
	var out string
	require.NoError(e.t, e.platform.QueryRow(context.Background(), query, args...).Scan(&out))
	return out
}
