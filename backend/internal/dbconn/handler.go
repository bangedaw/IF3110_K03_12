package dbconn

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
)

// Routes memasang endpoint koneksi di bawah /api/admin/databases.
func Routes(r chi.Router, s *Service) {
	r.Route("/admin/databases", func(r chi.Router) {
		r.Get("/", s.handleList)
		r.Post("/", s.handleCreate)
		r.Post("/test", s.handleTestUnsaved)
		r.Get("/{databaseId}", s.handleGet)
		r.Patch("/{databaseId}", s.handleUpdate)
		r.Delete("/{databaseId}", s.handleDelete)
		r.Post("/{databaseId}/test", s.handleTestSaved)
	})
}

const (
	defaultLimit = 50
	maxLimit     = 200
)

func (s *Service) principal(w http.ResponseWriter, r *http.Request) (authz.Principal, bool) {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, r, apperr.ErrUnauthenticated)
	}
	return p, ok
}

func pagination(r *http.Request) (int32, int32) {
	limit := defaultLimit
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return int32(limit), int32(offset)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	limit, offset := pagination(r)
	page, err := s.List(r.Context(), p, limit, offset)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, page)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	var in Input
	if err := httpx.Decode(w, r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.Create(r.Context(), p, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	out, err := s.Get(r.Context(), p, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	var in Patch
	if err := httpx.Decode(w, r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.Update(r.Context(), p, id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	if err := s.Delete(r.Context(), p, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testRequest menerima dua bentuk: kredensial penuh (sebelum simpan) atau
// { databaseId } untuk koneksi yang sudah tersimpan.
type testRequest struct {
	DatabaseID *int64 `json:"databaseId"`
	Input
}

func (s *Service) handleTestUnsaved(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	var in testRequest
	if err := httpx.Decode(w, r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.Test(r.Context(), p, in.Input, in.DatabaseID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Service) handleTestSaved(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	out, err := s.Test(r.Context(), p, Input{}, &id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func idFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "databaseId"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Error(w, r, apperr.New(apperr.CodeNotFound, "database tidak ditemukan"))
		return 0, false
	}
	return id, true
}
