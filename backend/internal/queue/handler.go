package queue

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
)

type JobReader interface {
	Get(ctx context.Context, databaseID, jobID int64) (apigen.Job, error)
}

type Handler struct {
	Jobs  JobReader
	Authz authz.Authorizer
}

func Routes(r chi.Router, h Handler) {
	r.Get("/databases/{databaseId}/jobs/{jobId}", h.getJob)
}

func (h Handler) getJob(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, r, apperr.ErrUnauthenticated)
		return
	}
	databaseID, err1 := strconv.ParseInt(chi.URLParam(r, "databaseId"), 10, 64)
	jobID, err2 := strconv.ParseInt(chi.URLParam(r, "jobId"), 10, 64)
	if err1 != nil || err2 != nil || databaseID <= 0 || jobID <= 0 {
		httpx.Error(w, r, apperr.ErrNotFound)
		return
	}
	if err := h.Authz.Authorize(r.Context(), p, authz.ActionMigrationRead, authz.Resource{DatabaseID: databaseID}); err != nil {
		httpx.Error(w, r, err)
		return
	}
	job, err := h.Jobs.Get(r.Context(), databaseID, jobID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, job)
}
