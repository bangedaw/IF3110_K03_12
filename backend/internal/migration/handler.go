package migration

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
)

func Routes(r chi.Router, s *Service) {
	r.Get("/databases/{databaseId}/migrations", s.handleList)
	r.Post("/databases/{databaseId}/migrations", s.handleCreate)
	r.Get("/databases/{databaseId}/migration-history", s.handleHistory)
	r.Get("/migrations/{migrationId}", s.handleGet)
	r.Put("/migrations/{migrationId}", s.handleUpdate)
	r.Delete("/migrations/{migrationId}", s.handleDelete)
	r.Post("/migrations/{migrationId}/submit", s.handleRequest(apigen.Apply))
	r.Post("/migrations/{migrationId}/rollback", s.handleRequest(apigen.Rollback))
	r.Post("/migrations/{migrationId}/forward", s.handleRequest(apigen.Forward))
	r.Get("/migrations/{migrationId}/approvals", s.handleApprovals)
	r.Post("/migrations/{migrationId}/approvals", s.handleDecide)
	r.Post("/migrations/{migrationId}/execute", s.handleExecute)
	r.Post("/migrations/{migrationId}/explain", s.handleExplain)
}

func target(w http.ResponseWriter, r *http.Request, param string) (authz.Principal, int64, bool) {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, r, apperr.ErrUnauthenticated)
		return p, 0, false
	}
	id, err := strconv.ParseInt(chi.URLParam(r, param), 10, 64)
	if err != nil || id <= 0 {
		httpx.Error(w, r, apperr.ErrNotFound)
		return p, 0, false
	}
	return p, id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := httpx.Decode(w, r, v); err != nil {
		httpx.Error(w, r, err)
		return false
	}
	return true
}

func respond(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, status, v)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	p, id, ok := target(w, r, "databaseId")
	if !ok {
		return
	}
	out, err := s.List(r.Context(), p, id)
	respond(w, r, http.StatusOK, out, err)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	p, id, ok := target(w, r, "databaseId")
	if !ok {
		return
	}
	var d apigen.MigrationDefinition
	if !decode(w, r, &d) {
		return
	}
	out, err := s.Create(r.Context(), p, id, d)
	respond(w, r, http.StatusCreated, out, err)
}

func (s *Service) handleHistory(w http.ResponseWriter, r *http.Request) {
	p, id, ok := target(w, r, "databaseId")
	if !ok {
		return
	}
	query := r.URL.Query()
	var migrationID *int64
	if v := query.Get("migrationId"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			httpx.Error(w, r, apperr.Validation(map[string]string{"migrationId": "harus bilangan bulat"}))
			return
		}
		migrationID = &n
	}
	out, err := s.History(r.Context(), p, id, migrationID, query.Get("hideRollback") == "true")
	respond(w, r, http.StatusOK, out, err)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	p, id, ok := target(w, r, "migrationId")
	if !ok {
		return
	}
	out, err := s.Get(r.Context(), p, id)
	respond(w, r, http.StatusOK, out, err)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	p, id, ok := target(w, r, "migrationId")
	if !ok {
		return
	}
	var in apigen.MigrationUpdate
	if !decode(w, r, &in) {
		return
	}
	out, err := s.Update(r.Context(), p, id, apigen.MigrationDefinition{
		Title: in.Title, Description: in.Description, UpSql: in.UpSql, DownSql: in.DownSql,
		Irreversible: in.Irreversible, IrreversibleReason: in.IrreversibleReason, Batch: in.Batch,
	}, in.Revision)
	respond(w, r, http.StatusOK, out, err)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	p, id, ok := target(w, r, "migrationId")
	if !ok {
		return
	}
	if err := s.Delete(r.Context(), p, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleRequest(kind apigen.MigrationKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, id, ok := target(w, r, "migrationId")
		if !ok {
			return
		}
		out, err := s.Request(r.Context(), p, id, kind)
		respond(w, r, http.StatusCreated, out, err)
	}
}

func (s *Service) handleApprovals(w http.ResponseWriter, r *http.Request) {
	p, id, ok := target(w, r, "migrationId")
	if !ok {
		return
	}
	out, err := s.Approvals(r.Context(), p, id)
	respond(w, r, http.StatusOK, out, err)
}

func (s *Service) handleDecide(w http.ResponseWriter, r *http.Request) {
	p, id, ok := target(w, r, "migrationId")
	if !ok {
		return
	}
	var in apigen.MigrationDecision
	if !decode(w, r, &in) {
		return
	}
	comment := ""
	if in.Comment != nil {
		comment = *in.Comment
	}
	fields := map[string]string{}
	if in.Decision != apigen.MigrationDecisionDecisionApproved && in.Decision != apigen.MigrationDecisionDecisionRejected {
		fields["decision"] = "harus approved atau rejected"
	}
	if len(comment) > maxTextLen {
		fields["comment"] = "maksimal 2000 karakter"
	}
	if len(fields) > 0 {
		httpx.Error(w, r, apperr.Validation(fields))
		return
	}
	out, err := s.Decide(r.Context(), p, id, in.RequestId, in.Decision == apigen.MigrationDecisionDecisionApproved, comment)
	respond(w, r, http.StatusOK, out, err)
}

func (s *Service) handleExecute(w http.ResponseWriter, r *http.Request) {
	p, id, ok := target(w, r, "migrationId")
	if !ok {
		return
	}
	var in apigen.MigrationExecute
	if !decode(w, r, &in) {
		return
	}
	jobID, err := s.Execute(r.Context(), p, id, in.RequestId)
	respond(w, r, http.StatusAccepted, apigen.MigrationJob{JobId: jobID}, err)
}

func (s *Service) handleExplain(w http.ResponseWriter, r *http.Request) {
	p, id, ok := target(w, r, "migrationId")
	if !ok {
		return
	}
	var in apigen.MigrationExplain
	if !decode(w, r, &in) {
		return
	}
	out, err := s.Explain(r.Context(), p, id, in.Analyze)
	respond(w, r, http.StatusOK, out, err)
}
