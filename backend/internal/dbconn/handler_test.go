package dbconn_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"

	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres" // daftarkan driver
)

const cipherKey = "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8="

func setup(t *testing.T) (*httptest.Server, *gen.Queries, *secret.Cipher) {
	t.Helper()
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	q := gen.New(pool)

	// Buat principal uji; created_by pada database_connections adalah FK ke users.
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, username, password_hash) VALUES (42, 'tester', 'x') ON CONFLICT DO NOTHING`)
	require.NoError(t, err)

	cipher, err := secret.New(cipherKey)
	require.NoError(t, err)

	store := &dbconn.SQLStore{Q: q}
	reg := dbconn.NewRegistry(store, cipher)
	t.Cleanup(reg.Close)

	svc := dbconn.NewService(store, reg, cipher, authz.NewFake())
	r := httpx.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Middleware uji: memasang principal seperti yang akan dilakukan session
	// middleware milik Miguel (integrasi I-2).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if u := req.Header.Get("X-Test-User"); u != "" {
				id, err := strconv.ParseInt(u, 10, 64)
				require.NoError(t, err)
				p := authz.Principal{UserID: id, IsPlatformAdmin: req.Header.Get("X-Test-Admin") == "1"}
				req = req.WithContext(auth.WithPrincipal(req.Context(), p))
			}
			next.ServeHTTP(w, req)
		})
	})
	// Samakan dengan cmd/server: semua route di bawah /api.
	r.Route("/api", func(api chi.Router) {
		dbconn.Routes(api, svc)
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, q, cipher
}

type call struct {
	method string
	path   string
	body   any
	admin  bool
}

func do(t *testing.T, srv *httptest.Server, c call) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if c.body != nil {
		b, err := json.Marshal(c.body)
		require.NoError(t, err)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(c.method, srv.URL+c.path, body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", "42")
	if c.admin {
		req.Header.Set("X-Test-Admin", "1")
	}
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res, string(raw)
}

func validInput() map[string]any {
	return map[string]any{
		"name": "inventory", "kind": "postgres", "host": "127.0.0.1", "port": 5433,
		"databaseName": "inventory", "username": "dbmp", "password": "rahasia-123",
		"sslMode": "disable",
	}
}

func TestCreateConnectionHidesPassword(t *testing.T) {
	srv, _, _ := setup(t)

	res, body := do(t, srv, call{http.MethodPost, "/api/admin/databases", validInput(), true})
	require.Equal(t, http.StatusCreated, res.StatusCode, body)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	require.Equal(t, true, out["hasPassword"])
	require.NotContains(t, body, "rahasia-123", "password tidak boleh muncul di response")
	require.NotContains(t, body, "passwordCiphertext", "ciphertext tidak boleh muncul di response")
	require.EqualValues(t, 5433, out["port"])
	require.Equal(t, "postgres", out["kind"])
}

func TestCreateRejectsNonAdmin(t *testing.T) {
	srv, _, _ := setup(t)

	res, body := do(t, srv, call{http.MethodPost, "/api/admin/databases", validInput(), false})
	require.Equal(t, http.StatusForbidden, res.StatusCode, body)
	require.Contains(t, body, "FORBIDDEN")
}

func TestCreateDuplicateNameConflicts(t *testing.T) {
	srv, _, _ := setup(t)

	res, body := do(t, srv, call{http.MethodPost, "/api/admin/databases", validInput(), true})
	require.Equal(t, http.StatusCreated, res.StatusCode, body)

	res, body = do(t, srv, call{http.MethodPost, "/api/admin/databases", validInput(), true})
	require.Equal(t, http.StatusConflict, res.StatusCode, body)
	require.Contains(t, body, "CONFLICT")
}

func TestCreateValidatesInput(t *testing.T) {
	srv, _, _ := setup(t)

	input := validInput()
	input["port"] = 0
	input["kind"] = "oracle"

	res, body := do(t, srv, call{http.MethodPost, "/api/admin/databases", input, true})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, body)

	var problem struct {
		Code   string            `json:"code"`
		Errors map[string]string `json:"errors"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &problem))
	require.Equal(t, "VALIDATION_FAILED", problem.Code)
	require.NotEmpty(t, problem.Errors["port"])
	require.NotEmpty(t, problem.Errors["kind"])
}

func TestListAndGet(t *testing.T) {
	srv, _, _ := setup(t)

	_, body := do(t, srv, call{http.MethodPost, "/api/admin/databases", validInput(), true})
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	id := int64(created["id"].(float64))

	res, body := do(t, srv, call{http.MethodGet, "/api/admin/databases?limit=10&offset=0", nil, true})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	var page struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &page))
	require.Len(t, page.Items, 1)
	require.EqualValues(t, 1, page.Total)

	res, body = do(t, srv, call{http.MethodGet, "/api/admin/databases/" + strconv.FormatInt(id, 10), nil, true})
	require.Equal(t, http.StatusOK, res.StatusCode, body)

	res, body = do(t, srv, call{http.MethodGet, "/api/admin/databases/999999", nil, true})
	require.Equal(t, http.StatusNotFound, res.StatusCode, body)
}

func TestUpdateWithoutPasswordKeepsCiphertext(t *testing.T) {
	srv, q, _ := setup(t)

	_, body := do(t, srv, call{http.MethodPost, "/api/admin/databases", validInput(), true})
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	id := int64(created["id"].(float64))

	before, err := q.GetConnectionByID(context.Background(), id)
	require.NoError(t, err)

	res, body := do(t, srv, call{
		http.MethodPatch, "/api/admin/databases/" + strconv.FormatInt(id, 10),
		map[string]any{"host": "db-baru.internal", "password": ""}, true,
	})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Contains(t, body, "db-baru.internal")

	after, err := q.GetConnectionByID(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, before.PasswordCiphertext, after.PasswordCiphertext, "password kosong berarti tidak diubah")
	require.Equal(t, "db-baru.internal", after.Host)
}

func TestUpdateWithNewPasswordReplacesCiphertext(t *testing.T) {
	srv, q, _ := setup(t)

	_, body := do(t, srv, call{http.MethodPost, "/api/admin/databases", validInput(), true})
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	id := int64(created["id"].(float64))

	before, err := q.GetConnectionByID(context.Background(), id)
	require.NoError(t, err)

	res, body := do(t, srv, call{
		http.MethodPatch, "/api/admin/databases/" + strconv.FormatInt(id, 10),
		map[string]any{"password": "ganti-banget"}, true,
	})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.NotContains(t, body, "ganti-banget")

	after, err := q.GetConnectionByID(context.Background(), id)
	require.NoError(t, err)
	require.NotEqual(t, before.PasswordCiphertext, after.PasswordCiphertext)

	cipher, err := secret.New(cipherKey)
	require.NoError(t, err)
	plain, err := cipher.Decrypt(after.PasswordCiphertext)
	require.NoError(t, err)
	require.Equal(t, "ganti-banget", string(plain))
}

func TestDeleteRemovesConnection(t *testing.T) {
	srv, _, _ := setup(t)

	_, body := do(t, srv, call{http.MethodPost, "/api/admin/databases", validInput(), true})
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	id := strconv.FormatInt(int64(created["id"].(float64)), 10)

	res, body := do(t, srv, call{http.MethodDelete, "/api/admin/databases/" + id, nil, true})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)

	res, body = do(t, srv, call{http.MethodGet, "/api/admin/databases/" + id, nil, true})
	require.Equal(t, http.StatusNotFound, res.StatusCode, body)
}

func TestTestConnectionUnreachableIs503(t *testing.T) {
	srv, _, _ := setup(t)

	input := validInput()
	input["host"] = "127.0.0.1"
	input["port"] = 1

	res, body := do(t, srv, call{http.MethodPost, "/api/admin/databases/test", input, true})
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode, body)
	require.Contains(t, body, "DB_UNREACHABLE")
	require.NotContains(t, body, "rahasia-123")
}

func TestTestConnectionToLiveTarget(t *testing.T) {
	srv, _, _ := setup(t)
	target := testutil.NewPostgresTarget(t)
	cc := target.Config().ConnConfig

	input := map[string]any{
		"name": "uji", "kind": "postgres", "host": cc.Host, "port": int(cc.Port),
		"databaseName": cc.Database, "username": cc.User, "password": cc.Password,
		"sslMode": "disable",
	}
	res, body := do(t, srv, call{http.MethodPost, "/api/admin/databases/test", input, true})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.NotContains(t, body, cc.Password)

	var out struct {
		OK        bool  `json:"ok"`
		LatencyMs int64 `json:"latencyMs"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	require.True(t, out.OK)
	require.GreaterOrEqual(t, out.LatencyMs, int64(0))
}

func TestTestConnectionStoredUsesEncryptedPassword(t *testing.T) {
	srv, _, _ := setup(t)
	target := testutil.NewPostgresTarget(t)
	cc := target.Config().ConnConfig

	input := map[string]any{
		"name": "tersimpan", "kind": "postgres", "host": cc.Host, "port": int(cc.Port),
		"databaseName": cc.Database, "username": cc.User, "password": cc.Password,
		"sslMode": "disable",
	}
	_, body := do(t, srv, call{http.MethodPost, "/api/admin/databases", input, true})
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	id := strconv.FormatInt(int64(created["id"].(float64)), 10)

	res, body := do(t, srv, call{http.MethodPost, "/api/admin/databases/" + id + "/test", nil, true})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.NotContains(t, body, cc.Password)
}

func TestEveryEndpointRejectsNonAdmin(t *testing.T) {
	srv, _, _ := setup(t)

	for _, c := range []call{
		{http.MethodGet, "/api/admin/databases", nil, false},
		{http.MethodPost, "/api/admin/databases", validInput(), false},
		{http.MethodPatch, "/api/admin/databases/1", map[string]any{"host": "x"}, false},
		{http.MethodDelete, "/api/admin/databases/1", nil, false},
		{http.MethodPost, "/api/admin/databases/test", validInput(), false},
		{http.MethodPost, "/api/admin/databases/1/test", nil, false},
	} {
		res, body := do(t, srv, c)
		require.Equal(t, http.StatusForbidden, res.StatusCode, c.method+" "+c.path+": "+body)
	}
}

func TestUnauthenticatedWithoutPrincipal(t *testing.T) {
	srv, _, _ := setup(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/databases", nil)
	require.NoError(t, err)
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode, string(raw))
}

func TestLatencyIsSaneAndNotLeaky(t *testing.T) {
	// Jaminan kecil bahwa hasil test koneksi tidak membawa DSN apa pun.
	srv, _, _ := setup(t)
	input := validInput()
	input["host"] = "127.0.0.1"
	input["port"] = 1
	start := time.Now()
	res, body := do(t, srv, call{http.MethodPost, "/api/admin/databases/test", input, true})
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode, body)
	require.Less(t, time.Since(start), 30*time.Second, "test connection harus dibatasi timeout")
	require.False(t, strings.Contains(body, "postgres://"), "DSN tidak boleh muncul di pesan error")
}

func TestCipherKeyIsValid(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(cipherKey)
	require.NoError(t, err)
	require.Len(t, raw, 32)
}
