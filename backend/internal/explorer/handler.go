package explorer

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

// Routes memasang endpoint explorer di bawah /api/databases.
func Routes(r chi.Router, s *Service) {
	r.Route("/databases", func(r chi.Router) {
		r.Get("/", s.handleList)
		r.Get("/{databaseId}", s.handleGet)
		r.Get("/{databaseId}/schemas", s.handleSchemas)
		r.Get("/{databaseId}/schemas/{schemaName}/tables", s.handleTables)
		r.Get("/{databaseId}/schemas/{schemaName}/tables/{tableName}", s.handleTable)
		r.Get("/{databaseId}/schemas/{schemaName}/tables/{tableName}/rows", s.handleRows)
		r.Post("/{databaseId}/snapshots", s.handleCreateSnapshot)
		r.Get("/{databaseId}/snapshots", s.handleListSnapshots)
		r.Get("/{databaseId}/snapshots/{snapshotId}", s.handleGetSnapshot)
		r.Post("/{databaseId}/schema-diff", s.handleSchemaDiff)
	})
}

func (s *Service) principal(w http.ResponseWriter, r *http.Request) (authz.Principal, bool) {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, r, apperr.ErrUnauthenticated)
	}
	return p, ok
}

func idFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "databaseId"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Error(w, r, apperr.ErrNotFound)
		return 0, false
	}
	return id, true
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	out, err := s.Databases(r.Context(), p)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
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
	out, err := s.Database(r.Context(), p, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Service) handleSchemas(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	out, err := s.Schemas(r.Context(), p, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if out == nil {
		out = []string{}
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Service) handleTables(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	out, err := s.Tables(r.Context(), p, id, chi.URLParam(r, "schemaName"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if out == nil {
		out = []schema.TableSummary{}
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Service) handleTable(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	out, err := s.Table(r.Context(), p, id, chi.URLParam(r, "schemaName"), chi.URLParam(r, "tableName"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Service) handleRows(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	// limit/offset salah format diabaikan (browser memberi default + clamp).
	limit, _ := strconv.Atoi(query.Get("limit"))
	offset, _ := strconv.Atoi(query.Get("offset"))
	q := dbms.RowsQuery{
		Schema:   chi.URLParam(r, "schemaName"),
		Table:    chi.URLParam(r, "tableName"),
		Limit:    limit,
		Offset:   offset,
		OrderBy:  query.Get("orderBy"),
		OrderDir: query.Get("orderDir"),
	}
	page, err := s.Rows(r.Context(), p, id, q)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, page)
}

func (s *Service) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	out, err := s.CreateSnapshot(r.Context(), p, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Service) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	out, err := s.Snapshots(r.Context(), p, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Service) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	snapshotID, err := strconv.ParseInt(chi.URLParam(r, "snapshotId"), 10, 64)
	if err != nil || snapshotID <= 0 {
		httpx.Error(w, r, apperr.ErrNotFound)
		return
	}
	out, err := s.Snapshot(r.Context(), p, id, snapshotID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Service) handleSchemaDiff(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	id, ok := idFromPath(w, r)
	if !ok {
		return
	}
	var req SchemaDiffRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.SchemaDiff(r.Context(), p, id, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
