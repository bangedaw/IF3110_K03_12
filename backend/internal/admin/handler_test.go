package admin_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/admin"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

type env struct {
	t       *testing.T
	pool    *pgxpool.Pool
	h       http.Handler
	adminID int64
	userID  int64
	dbID    int64
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)

	e := &env{t: t, pool: pool}
	e.adminID = e.id(`INSERT INTO users (username, password_hash, is_platform_admin) VALUES ('admin', 'x', TRUE) RETURNING id`)
	e.userID = e.id(`INSERT INTO users (username, password_hash) VALUES ('alice', 'x') RETURNING id`)
	e.dbID = e.id(`INSERT INTO database_connections (name, kind, host, port, database_name, username, password_ciphertext)
		VALUES ('inventory', 'postgres', 'localhost', 5432, 'inventory', 'u', '\x00') RETURNING id`)

	l := audit.New(pool)
	svc := admin.NewService(pool, audit.RecordDenied(authz.NewDBAuthorizer(pool), l), l)
	r := httpx.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			id, _ := strconv.ParseInt(req.Header.Get("X-Test-User"), 10, 64)
			p := authz.Principal{UserID: id, IsPlatformAdmin: id == e.adminID}
			next.ServeHTTP(w, req.WithContext(auth.WithPrincipal(req.Context(), p)))
		})
	})
	r.Route("/api", func(api chi.Router) { admin.Routes(api, svc) })
	e.h = r
	return e
}

func (e *env) id(sql string, args ...any) int64 {
	e.t.Helper()
	var id int64
	require.NoError(e.t, e.pool.QueryRow(context.Background(), sql, args...).Scan(&id))
	return id
}

func (e *env) scalar(sql string, args ...any) any {
	e.t.Helper()
	var v any
	require.NoError(e.t, e.pool.QueryRow(context.Background(), sql, args...).Scan(&v))
	return v
}

func (e *env) do(as int64, method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Test-User", strconv.FormatInt(as, 10))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) session(userID int64) string {
	e.t.Helper()
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(),
		`INSERT INTO session_families (id, user_id, absolute_expires_at) VALUES (gen_random_uuid(), $1, now() + interval '1 hour') RETURNING id::text`,
		userID).Scan(&id))
	return id
}

func (e *env) family(id string) (rotate, revoked bool) {
	e.t.Helper()
	require.NoError(e.t, e.pool.QueryRow(context.Background(),
		`SELECT rotate_required, revoked_at IS NOT NULL FROM session_families WHERE id = $1::uuid`, id).Scan(&rotate, &revoked))
	return rotate, revoked
}

func path(format string, ids ...int64) string {
	for _, id := range ids {
		format = strings.Replace(format, "%d", strconv.FormatInt(id, 10), 1)
	}
	return format
}

func TestNonAdminIsForbiddenOnEveryEndpoint(t *testing.T) {
	e := setup(t)
	g := e.id(`INSERT INTO groups (name) VALUES ('g') RETURNING id`)

	endpoints := []struct{ method, path, body string }{
		{"GET", "/api/admin/users", ""},
		{"POST", "/api/admin/users", `{"username":"bob","password":"rahasia123"}`},
		{"GET", path("/api/admin/users/%d", e.userID), ""},
		{"PATCH", path("/api/admin/users/%d", e.userID), `{"isPlatformAdmin":true}`},
		{"DELETE", path("/api/admin/users/%d", e.adminID), ""},
		{"POST", path("/api/admin/users/%d/password", e.adminID), `{"password":"rahasia123"}`},
		{"POST", path("/api/admin/users/%d/sessions/revoke", e.adminID), ""},
		{"GET", "/api/admin/groups", ""},
		{"POST", "/api/admin/groups", `{"name":"x"}`},
		{"GET", path("/api/admin/groups/%d", g), ""},
		{"PATCH", path("/api/admin/groups/%d", g), `{"name":"y"}`},
		{"DELETE", path("/api/admin/groups/%d", g), ""},
		{"PUT", path("/api/admin/groups/%d/members/%d", g, e.userID), ""},
		{"DELETE", path("/api/admin/groups/%d/members/%d", g, e.userID), ""},
		{"GET", path("/api/admin/groups/%d/grants", g), ""},
		{"PUT", path("/api/admin/groups/%d/grants/%d", g, e.dbID), `{"level":"edit"}`},
		{"DELETE", path("/api/admin/groups/%d/grants/%d", g, e.dbID), ""},
	}
	for _, ep := range endpoints {
		rec := e.do(e.userID, ep.method, ep.path, ep.body)
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s", ep.method, ep.path)
	}

	assert.EqualValues(t, len(endpoints), e.scalar(`SELECT count(*) FROM audit_logs WHERE outcome = 'denied' AND actor_user_id = $1`, e.userID))
	assert.EqualValues(t, 0, e.scalar(`SELECT count(*) FROM group_members`))
	assert.EqualValues(t, false, e.scalar(`SELECT is_platform_admin FROM users WHERE id = $1`, e.userID))
}

func TestUserLifecycle(t *testing.T) {
	e := setup(t)
	a := e.adminID

	rec := e.do(a, "POST", "/api/admin/users", `{"username":"bob","displayName":"Bob","password":"rahasia123"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "rahasia123")
	assert.NotContains(t, rec.Body.String(), "argon2id")
	assert.NotContains(t, rec.Body.String(), "passwordHash")
	bob := e.id(`SELECT id FROM users WHERE username = 'bob'`)

	assert.Equal(t, http.StatusConflict, e.do(a, "POST", "/api/admin/users", `{"username":"BOB","password":"rahasia123"}`).Code)
	rec = e.do(a, "POST", "/api/admin/users", `{"username":"x","password":"pendek"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), `"username"`)
	assert.Contains(t, rec.Body.String(), `"password"`)

	rec = e.do(a, "GET", "/api/admin/users?limit=1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"total":3`)
	assert.Contains(t, rec.Body.String(), `"limit":1`)
	assert.Equal(t, http.StatusNotFound, e.do(a, "GET", "/api/admin/users/9999", "").Code)
	assert.Equal(t, http.StatusNotFound, e.do(a, "GET", "/api/admin/users/abc", "").Code)

	rec = e.do(a, "PATCH", path("/api/admin/users/%d", bob), `{"displayName":"Bobby"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"displayName":"Bobby"`)
	assert.Contains(t, rec.Body.String(), `"username":"bob"`)

	s := e.session(bob)
	require.Equal(t, http.StatusOK, e.do(a, "PATCH", path("/api/admin/users/%d", bob), `{"isPlatformAdmin":true}`).Code)
	rotate, revoked := e.family(s)
	assert.True(t, rotate, "dijadikan admin harus memaksa rotasi session")
	assert.False(t, revoked)

	require.Equal(t, http.StatusOK, e.do(a, "PATCH", path("/api/admin/users/%d", bob), `{"isActive":false}`).Code)
	_, revoked = e.family(s)
	assert.True(t, revoked, "user dinonaktifkan harus me-revoke session")

	s = e.session(bob)
	require.Equal(t, http.StatusNoContent, e.do(a, "POST", path("/api/admin/users/%d/password", bob), `{"password":"passwordbaru"}`).Code)
	_, revoked = e.family(s)
	assert.True(t, revoked, "reset password harus me-revoke session")
	ok, err := auth.VerifyPassword(e.scalar(`SELECT password_hash FROM users WHERE id = $1`, bob).(string), "passwordbaru")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, http.StatusUnprocessableEntity, e.do(a, "POST", path("/api/admin/users/%d/password", bob), `{"password":"x"}`).Code)

	s = e.session(bob)
	require.Equal(t, http.StatusNoContent, e.do(a, "POST", path("/api/admin/users/%d/sessions/revoke", bob), "").Code)
	_, revoked = e.family(s)
	assert.True(t, revoked)

	assert.Equal(t, http.StatusConflict, e.do(a, "PATCH", path("/api/admin/users/%d", a), `{"isActive":false}`).Code)
	assert.Equal(t, http.StatusConflict, e.do(a, "PATCH", path("/api/admin/users/%d", a), `{"isPlatformAdmin":false}`).Code)
	assert.Equal(t, http.StatusConflict, e.do(a, "DELETE", path("/api/admin/users/%d", a), "").Code)

	require.Equal(t, http.StatusNoContent, e.do(a, "DELETE", path("/api/admin/users/%d", bob), "").Code)
	assert.Equal(t, http.StatusNotFound, e.do(a, "DELETE", path("/api/admin/users/%d", bob), "").Code)
}

func TestGroupsMembersAndGrants(t *testing.T) {
	e := setup(t)
	a := e.adminID
	ctx := context.Background()
	az := authz.NewDBAuthorizer(e.pool)

	require.Equal(t, http.StatusCreated, e.do(a, "POST", "/api/admin/groups", `{"name":"backend-team","description":"tim backend"}`).Code)
	g := e.id(`SELECT id FROM groups WHERE name = 'backend-team'`)
	assert.Equal(t, http.StatusConflict, e.do(a, "POST", "/api/admin/groups", `{"name":"Backend-Team"}`).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, e.do(a, "POST", "/api/admin/groups", `{"name":"  "}`).Code)

	s := e.session(e.userID)
	require.Equal(t, http.StatusNoContent, e.do(a, "PUT", path("/api/admin/groups/%d/members/%d", g, e.userID), "").Code)
	require.Equal(t, http.StatusNoContent, e.do(a, "PUT", path("/api/admin/groups/%d/members/%d", g, e.userID), "").Code)
	rotate, _ := e.family(s)
	assert.True(t, rotate, "ditambah ke group harus memaksa rotasi")
	assert.Equal(t, http.StatusNotFound, e.do(a, "PUT", path("/api/admin/groups/%d/members/9999", g), "").Code)
	assert.Equal(t, http.StatusNotFound, e.do(a, "PUT", path("/api/admin/groups/9999/members/%d", e.userID), "").Code)

	rec := e.do(a, "GET", path("/api/admin/groups/%d", g), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"username":"alice"`)
	assert.Contains(t, rec.Body.String(), `"description":"tim backend"`)

	resetRotate := func() {
		_, err := e.pool.Exec(ctx, `UPDATE session_families SET rotate_required = FALSE`)
		require.NoError(t, err)
	}
	grant := path("/api/admin/groups/%d/grants/%d", g, e.dbID)
	p := authz.Principal{UserID: e.userID}
	res := authz.Resource{DatabaseID: e.dbID}

	resetRotate()
	rec = e.do(a, "PUT", grant, `{"level":"view"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"databaseName":"inventory"`)
	rotate, _ = e.family(s)
	assert.True(t, rotate, "grant baru harus memaksa rotasi")
	assert.NoError(t, az.Authorize(ctx, p, authz.ActionDBView, res))
	assert.ErrorIs(t, az.Authorize(ctx, p, authz.ActionMigrationWrite, res), authz.ErrForbidden)

	resetRotate()
	require.Equal(t, http.StatusOK, e.do(a, "PUT", grant, `{"level":"edit"}`).Code)
	rotate, _ = e.family(s)
	assert.True(t, rotate, "view -> edit harus memaksa rotasi")
	assert.NoError(t, az.Authorize(ctx, p, authz.ActionMigrationWrite, res))

	resetRotate()
	require.Equal(t, http.StatusOK, e.do(a, "PUT", grant, `{"level":"view"}`).Code)
	rotate, _ = e.family(s)
	assert.False(t, rotate, "turun level tidak perlu rotasi")

	assert.Equal(t, http.StatusUnprocessableEntity, e.do(a, "PUT", grant, `{"level":"owner"}`).Code)
	assert.Equal(t, http.StatusNotFound, e.do(a, "PUT", path("/api/admin/groups/%d/grants/9999", g), `{"level":"view"}`).Code)

	rec = e.do(a, "GET", path("/api/admin/groups/%d/grants", g), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"items":[{"databaseId":`+strconv.FormatInt(e.dbID, 10)+`,"databaseName":"inventory","level":"view"}]}`, rec.Body.String())

	require.Equal(t, http.StatusNoContent, e.do(a, "DELETE", grant, "").Code)
	assert.Equal(t, http.StatusNotFound, e.do(a, "DELETE", grant, "").Code)
	assert.ErrorIs(t, az.Authorize(ctx, p, authz.ActionDBView, res), authz.ErrForbidden)

	require.Equal(t, http.StatusNoContent, e.do(a, "DELETE", path("/api/admin/groups/%d/members/%d", g, e.userID), "").Code)
	assert.Equal(t, http.StatusNotFound, e.do(a, "DELETE", path("/api/admin/groups/%d/members/%d", g, e.userID), "").Code)

	rec = e.do(a, "PATCH", path("/api/admin/groups/%d", g), `{"name":"platform"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"name":"platform"`)
	assert.Contains(t, rec.Body.String(), `"description":"tim backend"`)

	require.Equal(t, http.StatusNoContent, e.do(a, "DELETE", path("/api/admin/groups/%d", g), "").Code)
	assert.Equal(t, http.StatusNotFound, e.do(a, "GET", path("/api/admin/groups/%d", g), "").Code)
}

func TestAuditLogHasNoSecrets(t *testing.T) {
	e := setup(t)
	require.Equal(t, http.StatusCreated, e.do(e.adminID, "POST", "/api/admin/users", `{"username":"bob","password":"rahasiaPertama"}`).Code)
	bob := e.id(`SELECT id FROM users WHERE username = 'bob'`)
	require.Equal(t, http.StatusNoContent, e.do(e.adminID, "POST", path("/api/admin/users/%d/password", bob), `{"password":"rahasiaKedua"}`).Code)

	assert.EqualValues(t, 2, e.scalar(`SELECT count(*) FROM audit_logs WHERE actor_user_id = $1 AND outcome = 'success'`, e.adminID))
	assert.EqualValues(t, 0, e.scalar(`SELECT count(*) FROM audit_logs
		WHERE metadata::text ILIKE '%rahasia%' OR metadata::text ILIKE '%argon2%'`))
}
