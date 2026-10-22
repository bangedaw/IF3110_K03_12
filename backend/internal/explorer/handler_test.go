package explorer_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/explorer"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"

	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres" // daftarkan driver
)

const cipherKey = "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8="

type fixture struct {
	srv        *httptest.Server
	liveID     int64
	inactiveID int64
	deadID     int64
}

// setup menyiapkan platform DB dengan 3 koneksi:
//   - live:    aktif, menunjuk target PG hidup (schema seed app.*)
//   - inactive: dinonaktifkan (harus tersembunyi dari semua endpoint)
//   - dead:    aktif tapi host mati (harus 503 DB_UNREACHABLE)
//
// user 42 diberi view pada ketiganya; user 43 tanpa akses apa pun.
func setup(t *testing.T) fixture {
	t.Helper()

	platform := testutil.NewPlatformDB(t)
	t.Cleanup(platform.Close)
	target := testutil.NewPostgresTarget(t)
	t.Cleanup(target.Close)

	cipher, err := secret.New(cipherKey)
	require.NoError(t, err)

	_, err = platform.Exec(context.Background(),
		`INSERT INTO users (id, username, password_hash) VALUES (42, 'viewer', 'x'), (43, 'none', 'x') ON CONFLICT DO NOTHING`)
	require.NoError(t, err)

	cc := target.Config().ConnConfig
	insert := func(name, host string, port int, active bool, password string) int64 {
		var id int64
		err := platform.QueryRow(context.Background(), `
			INSERT INTO database_connections
				(name, kind, host, port, database_name, username, password_ciphertext, ssl_mode, is_active, created_by)
			VALUES ($1, 'postgres', $2, $3, $4, $5, $6, 'disable', $7, 42)
			RETURNING id`,
			name, host, int32(port), cc.Database, cc.User, cipher.Encrypt([]byte(password)), active,
		).Scan(&id)
		require.NoError(t, err)
		return id
	}
	f := fixture{
		srv:        nil,
		liveID:     insert("live", cc.Host, int(cc.Port), true, cc.Password),
		inactiveID: insert("inactive", cc.Host, int(cc.Port), false, cc.Password),
		// Port 1 tidak menerima koneksi apa pun (koneksi ditolak segera).
		deadID: insert("dead", "127.0.0.1", 1, true, "apa-saja"),
	}

	q := gen.New(platform)
	store := &dbconn.SQLStore{Q: q}
	reg := dbconn.NewRegistry(store, cipher)
	t.Cleanup(reg.Close)

	az := authz.NewFake().
		Grant(42, f.liveID, authz.LevelView).
		Grant(42, f.inactiveID, authz.LevelView).
		Grant(42, f.deadID, authz.LevelView)

	svc := explorer.NewService(store, reg, az)

	r := httpx.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Middleware uji: memasang principal seperti session middleware Miguel
	// nanti (integrasi I-2).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if u := req.Header.Get("X-Test-User"); u != "" {
				id, err := strconv.ParseInt(u, 10, 64)
				require.NoError(t, err)
				p := authz.Principal{UserID: id}
				req = req.WithContext(auth.WithPrincipal(req.Context(), p))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Route("/api", func(api chi.Router) {
		explorer.Routes(api, svc)
	})

	f.srv = httptest.NewServer(r)
	t.Cleanup(f.srv.Close)
	return f
}

// get melakukan GET; user "" berarti tanpa sesi (harus 401).
func get(t *testing.T, srv *httptest.Server, path, user string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, string(raw)
}

func getJSON(t *testing.T, srv *httptest.Server, path, user string, out any) int {
	t.Helper()
	code, body := get(t, srv, path, user)
	require.Equal(t, http.StatusOK, code, "body: %s", body)
	require.NoError(t, json.Unmarshal([]byte(body), out), "body: %s", body)
	return code
}

func problemCode(t *testing.T, body string) string {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &p), "body: %s", body)
	return p.Code
}

func TestListDatabasesFiltersAccessAndActive(t *testing.T) {
	f := setup(t)

	var dbs []explorer.Database
	getJSON(t, f.srv, "/api/databases", "42", &dbs)

	byID := map[int64]explorer.Database{}
	for _, d := range dbs {
		byID[d.ID] = d
	}
	require.Len(t, dbs, 2, "hanya koneksi aktif ber-grant yang tampil")
	require.Contains(t, byID, f.liveID)
	require.Contains(t, byID, f.deadID)
	require.NotContains(t, byID, f.inactiveID, "koneksi non-aktif tersembunyi")
	require.Equal(t, authz.LevelView, byID[f.liveID].Level)

	// Tanpa akses apa pun: daftar kosong (200, bukan 403).
	code, body := get(t, f.srv, "/api/databases", "43")
	require.Equal(t, http.StatusOK, code, "body: %s", body)
	require.Equal(t, "[]", strings.TrimSpace(body))

	// Tanpa sesi: 401.
	code, _ = get(t, f.srv, "/api/databases", "")
	require.Equal(t, http.StatusUnauthorized, code)
}

func TestGetDatabaseAccess(t *testing.T) {
	f := setup(t)

	var d explorer.DatabaseDetail
	getJSON(t, f.srv, "/api/databases/"+strconv.FormatInt(f.liveID, 10), "42", &d)
	require.Equal(t, f.liveID, d.ID)
	require.Equal(t, "live", d.Name)
	require.Equal(t, authz.LevelView, d.Level)
	require.NotEmpty(t, d.Host)

	// Tanpa akses -> 403.
	code, _ := get(t, f.srv, "/api/databases/"+strconv.FormatInt(f.liveID, 10), "43")
	require.Equal(t, http.StatusForbidden, code)

	// Koneksi non-aktif -> 404 meski ber-grant.
	code, _ = get(t, f.srv, "/api/databases/"+strconv.FormatInt(f.inactiveID, 10), "42")
	require.Equal(t, http.StatusNotFound, code)

	// Tidak ada & tidak ber-grant -> 403 (keberadaan tidak dibocorkan).
	code, _ = get(t, f.srv, "/api/databases/999999", "42")
	require.Equal(t, http.StatusForbidden, code)

	// Tanpa sesi -> 401.
	code, _ = get(t, f.srv, "/api/databases/"+strconv.FormatInt(f.liveID, 10), "")
	require.Equal(t, http.StatusUnauthorized, code)
}

func TestSchemasRequiresView(t *testing.T) {
	f := setup(t)
	base := "/api/databases/" + strconv.FormatInt(f.liveID, 10)

	var schemas []string
	getJSON(t, f.srv, base+"/schemas", "42", &schemas)
	require.Contains(t, schemas, "app")
	require.Contains(t, schemas, "public")

	code, _ := get(t, f.srv, base+"/schemas", "43")
	require.Equal(t, http.StatusForbidden, code)
}

func TestTablesList(t *testing.T) {
	f := setup(t)
	base := "/api/databases/" + strconv.FormatInt(f.liveID, 10)

	var tables []struct {
		Schema      string `json:"schema"`
		Name        string `json:"name"`
		RowEstimate int64  `json:"rowEstimate"`
	}
	getJSON(t, f.srv, base+"/schemas/app/tables", "42", &tables)
	require.Len(t, tables, 3)
	byName := map[string]int64{}
	for _, tb := range tables {
		require.Equal(t, "app", tb.Schema)
		byName[tb.Name] = tb.RowEstimate
	}
	require.EqualValues(t, 5, byName["authors"])
	require.EqualValues(t, 12, byName["books"])
	require.EqualValues(t, 3, byName["attachments"])

	// Schema kosong -> [] 200.
	code, body := get(t, f.srv, base+"/schemas/tidak_ada/tables", "42")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "[]", strings.TrimSpace(body))

	code, _ = get(t, f.srv, base+"/schemas/app/tables", "43")
	require.Equal(t, http.StatusForbidden, code)
}

func TestTableDetail(t *testing.T) {
	f := setup(t)
	base := "/api/databases/" + strconv.FormatInt(f.liveID, 10)

	var tb struct {
		Name    string `json:"name"`
		Columns []struct {
			Name string `json:"name"`
		} `json:"columns"`
		Constraints []any `json:"constraints"`
		Indexes     []any `json:"indexes"`
	}
	getJSON(t, f.srv, base+"/schemas/app/tables/books", "42", &tb)
	require.Equal(t, "books", tb.Name)
	require.Len(t, tb.Columns, 6)
	require.NotEmpty(t, tb.Constraints)
	require.NotEmpty(t, tb.Indexes)

	code, _ := get(t, f.srv, base+"/schemas/app/tables/tidak_ada", "42")
	require.Equal(t, http.StatusNotFound, code)
}

func TestRowsPagination(t *testing.T) {
	f := setup(t)
	base := "/api/databases/" + strconv.FormatInt(f.liveID, 10) + "/schemas/app/tables/books/rows"

	var page struct {
		Columns []string `json:"columns"`
		Rows    [][]struct {
			Value  string `json:"value"`
			IsNull bool   `json:"isNull"`
		} `json:"rows"`
		TotalEstimate int64 `json:"totalEstimate"`
		Limit         int   `json:"limit"`
		Offset        int   `json:"offset"`
	}
	getJSON(t, f.srv, base+"?limit=5&offset=5", "42", &page)
	require.Equal(t, []string{"id", "author_id", "title", "price", "published_on", "tags"}, page.Columns)
	require.Len(t, page.Rows, 5)
	require.EqualValues(t, 12, page.TotalEstimate)
	require.Equal(t, 5, page.Limit)
	require.Equal(t, 5, page.Offset)
	require.Equal(t, "6", page.Rows[0][0].Value, "halaman kedua dimulai id ke-6")

	code, _ := get(t, f.srv,
		"/api/databases/"+strconv.FormatInt(f.liveID, 10)+"/schemas/app/tables/tidak_ada/rows", "42")
	require.Equal(t, http.StatusNotFound, code)

	code, _ = get(t, f.srv, base+"?limit=5", "43")
	require.Equal(t, http.StatusForbidden, code)
}

func TestDeadTargetReturns503(t *testing.T) {
	f := setup(t)
	base := "/api/databases/" + strconv.FormatInt(f.deadID, 10)

	code, body := get(t, f.srv, base+"/schemas", "42")
	require.Equal(t, http.StatusServiceUnavailable, code, "body: %s", body)
	require.Equal(t, "DB_UNREACHABLE", problemCode(t, body))
	require.NotContains(t, body, "apa-saja", "password koneksi tidak boleh bocor")
}
