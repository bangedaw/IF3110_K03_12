package queue_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
)

type oneJob struct{ job apigen.Job }

func (o oneJob) Get(_ context.Context, databaseID, jobID int64) (apigen.Job, error) {
	if databaseID != o.job.DatabaseId || jobID != o.job.Id {
		return apigen.Job{}, apperr.ErrNotFound
	}
	return o.job, nil
}

const (
	viewer   = int64(1)
	outsider = int64(2)
)

func jobRouter() http.Handler {
	job := apigen.Job{Id: 7, RequestId: 3, DatabaseId: 1, Kind: apigen.JobKindRollback, Status: apigen.JobStatusPartial, Attempts: 1, MaxAttempts: 3,
		BatchesCommitted: 2, RowsCommitted: 2000, CreatedAt: time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC)}
	az := authz.NewFake().Grant(viewer, 1, authz.LevelView)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			id := outsider
			if req.Header.Get("X-User") == "viewer" {
				id = viewer
			}
			next.ServeHTTP(w, req.WithContext(auth.WithPrincipal(req.Context(), authz.Principal{UserID: id})))
		})
	})
	r.Route("/databases", func(r chi.Router) {
		r.Get("/{databaseId}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	})
	queue.Routes(r, queue.Handler{Jobs: oneJob{job}, Authz: az})
	return r
}

func get(h http.Handler, path, user string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-User", user)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGetJobReturnsStatusForViewer(t *testing.T) {
	rec := get(jobRouter(), "/databases/1/jobs/7", "viewer")
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "partial", body["status"])
	assert.Equal(t, "rollback", body["kind"], "SPA membedakan partial dan partially reverted dari kind")
	assert.EqualValues(t, 2000, body["rowsCommitted"])
	assert.EqualValues(t, 2, body["batchesCommitted"])
}

func TestGetJobRejectsUserWithoutAccess(t *testing.T) {
	assert.Equal(t, http.StatusForbidden, get(jobRouter(), "/databases/1/jobs/7", "outsider").Code)
}

func TestGetJobIsScopedToTheDatabase(t *testing.T) {
	h := jobRouter()
	assert.Equal(t, http.StatusNotFound, get(h, "/databases/1/jobs/8", "viewer").Code)
	assert.Equal(t, http.StatusNotFound, get(h, "/databases/1/jobs/abc", "viewer").Code)
	assert.Equal(t, http.StatusForbidden, get(h, "/databases/2/jobs/7", "viewer").Code, "job database lain tetap diperiksa aksesnya lebih dulu")
}

func TestJobRouteCoexistsWithDatabaseRoutes(t *testing.T) {
	assert.Equal(t, http.StatusTeapot, get(jobRouter(), "/databases/1", "viewer").Code, "rute explorer tetap terjangkau")
}
