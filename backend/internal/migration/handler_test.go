package migration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
)

type apiEnv struct {
	srv *httptest.Server
	pub *fakePub
	lc  migration.ExecutionLifecycle
}

// newAPI memasang Routes di belakang middleware yang membaca user dari header
// X-User (pengganti sesi). Tanpa header = tidak login.
func newAPI(t *testing.T) *apiEnv {
	t.Helper()
	st, pub := newFakeStore(), &fakePub{}
	au := migration.AuthorizerFuncs{
		View: func(context.Context, migration.Actor, int64) error { return nil },
		Edit: func(_ context.Context, a migration.Actor, _ int64) error {
			if a.UserID == editorID {
				return nil
			}
			return apperr.ErrForbidden
		},
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			var id int64
			if v := req.Header.Get("X-User"); v != "" {
				id, _ = strconv.ParseInt(v, 10, 64)
				req = req.WithContext(auth.WithPrincipal(req.Context(), authz.Principal{UserID: id}))
			}
			next.ServeHTTP(w, req)
		})
	})
	migration.Routes(r, migration.NewService(st, pub, au))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &apiEnv{srv: srv, pub: pub, lc: migration.NewLifecycle(st)}
}

func (a *apiEnv) do(t *testing.T, user, method, path, body string) (int, map[string]any, []any) {
	t.Helper()
	req, _ := http.NewRequest(method, a.srv.URL+path, strings.NewReader(body))
	if user != "" {
		req.Header.Set("X-User", user)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var raw json.RawMessage
	_ = json.NewDecoder(res.Body).Decode(&raw)
	var obj map[string]any
	var arr []any
	if json.Unmarshal(raw, &obj) != nil {
		_ = json.Unmarshal(raw, &arr)
	}
	return res.StatusCode, obj, arr
}

const goodBody = `{"title":"Buat t1","upSql":"CREATE TABLE app.t1 (id int)","downSql":"DROP TABLE app.t1"}`

func TestAPIRejectsUnauthenticatedViewerAndInvalid(t *testing.T) {
	a := newAPI(t)
	if code, _, _ := a.do(t, "", "GET", "/databases/1/migrations", ""); code != 401 {
		t.Fatalf("tanpa sesi harus 401, dapat %d", code)
	}
	if code, _, _ := a.do(t, "11", "POST", "/databases/1/migrations", goodBody); code != 403 {
		t.Fatalf("View Only tidak boleh membuat: %d", code)
	}
	if code, p, _ := a.do(t, "10", "POST", "/databases/1/migrations", `{"title":""}`); code != 422 || p["code"] != "VALIDATION_FAILED" {
		t.Fatalf("input kosong harus 422: %d %v", code, p)
	}
	if code, _, _ := a.do(t, "10", "POST", "/databases/1/migrations", `{"bogus":1}`); code != 422 {
		t.Fatalf("field tak dikenal harus 422: %d", code)
	}
	if code, _, _ := a.do(t, "10", "GET", "/migrations/999", ""); code != 404 {
		t.Fatalf("migration tak ada harus 404: %d", code)
	}
	if code, _, _ := a.do(t, "10", "GET", "/migrations/abc", ""); code != 404 {
		t.Fatalf("id tak sah harus 404: %d", code)
	}
}

func TestAPIFullFlow(t *testing.T) {
	a := newAPI(t)
	code, m, _ := a.do(t, "10", "POST", "/databases/1/migrations", goodBody)
	if code != 201 || m["status"] != "draft" {
		t.Fatalf("create: %d %v", code, m)
	}
	id := "/migrations/1"

	if code, m, _ = a.do(t, "10", "PUT", id, strings.Replace(goodBody, "Buat t1", "Buat t1 v2", 1)); code != 200 || m["revision"] != float64(2) {
		t.Fatalf("update: %d %v", code, m)
	}
	// rollback sebelum apply ditolak (409), bukan 500.
	if code, _, _ = a.do(t, "10", "POST", id+"/rollback", ""); code != 409 {
		t.Fatalf("rollback sebelum apply harus 409: %d", code)
	}
	if code, m, _ = a.do(t, "10", "POST", id+"/submit", ""); code != 200 || m["status"] != "pending_review" {
		t.Fatalf("submit: %d %v", code, m)
	}
	if code, _, _ = a.do(t, "10", "PUT", id, goodBody); code != 409 {
		t.Fatalf("ubah setelah submit harus 409: %d", code)
	}
	if code, v, _ := a.do(t, "11", "GET", id+"/approvals", ""); code != 200 || len(v["required"].([]any)) != 2 {
		t.Fatalf("approvals: %d %v", code, v)
	}

	approve := `{"kind":"apply","decision":"approved","comment":"ok"}`
	if code, _, _ = a.do(t, "99", "POST", id+"/approvals", approve); code != 403 {
		t.Fatalf("bukan leader harus 403: %d", code)
	}
	if code, _, _ = a.do(t, "1", "POST", id+"/approvals", `{"kind":"apply","decision":"pending"}`); code != 422 {
		t.Fatalf("decision pending harus 422: %d", code)
	}
	if code, _, _ = a.do(t, "1", "POST", id+"/approvals", approve); code != 200 || len(a.pub.reqs) != 0 {
		t.Fatalf("approval pertama: %d, job=%d", code, len(a.pub.reqs))
	}
	if code, _, _ = a.do(t, "1", "POST", id+"/approvals", approve); code != 409 {
		t.Fatalf("vote ganda harus 409: %d", code)
	}
	if code, m, _ = a.do(t, "2", "POST", id+"/approvals", approve); code != 200 || m["status"] != "pending_queue" || len(a.pub.reqs) != 1 {
		t.Fatalf("approval terakhir: %d %v job=%d", code, m, len(a.pub.reqs))
	}

	// Worker menjalankan apply, lalu rollback diajukan dan disetujui.
	ctx := context.Background()
	req := a.pub.reqs[0]
	attempt, err := a.lc.MarkRunning(ctx, req)
	must(t, err)
	must(t, a.lc.RecordResult(ctx, attempt, migration.ExecutionResult{Success: true}))

	if code, _, _ = a.do(t, "11", "POST", id+"/rollback", ""); code != 403 {
		t.Fatalf("View Only tidak boleh mengajukan rollback: %d", code)
	}
	if code, _, _ = a.do(t, "10", "POST", id+"/rollback", ""); code != 200 {
		t.Fatalf("rollback: %d", code)
	}
	for _, leader := range []string{"1", "2"} {
		if code, _, _ = a.do(t, leader, "POST", id+"/approvals", `{"kind":"rollback","decision":"approved"}`); code != 200 {
			t.Fatalf("approve rollback: %d", code)
		}
	}
	if len(a.pub.reqs) != 2 || a.pub.reqs[1].Kind != migration.KindRollback {
		t.Fatalf("job rollback tidak terbit: %+v", a.pub.reqs)
	}
	rb, err := a.lc.MarkRunning(ctx, a.pub.reqs[1])
	must(t, err)
	must(t, a.lc.RecordResult(ctx, rb, migration.ExecutionResult{Success: true}))

	if _, _, h := a.do(t, "11", "GET", "/databases/1/migration-history", ""); len(h) != 2 {
		t.Fatalf("riwayat harus 2 baris: %v", h)
	}
	if _, _, h := a.do(t, "11", "GET", "/databases/1/migration-history?hideRollback=true", ""); len(h) != 0 {
		t.Fatalf("hideRollback harus menyembunyikan pasangan: %v", h)
	}
	if code, _, _ := a.do(t, "11", "GET", "/databases/1/migration-history?migrationId=x", ""); code != 422 {
		t.Fatalf("migrationId tak sah harus 422: %d", code)
	}
}
