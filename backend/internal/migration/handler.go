package migration

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
)

// Routes memasang JSON API migration (R-7). Semua rute berada di balik
// middleware sesi.
func Routes(r chi.Router, s *Service) {
	h := handler{s}
	r.Get("/databases/{databaseId}/migrations", h.list)
	r.Post("/databases/{databaseId}/migrations", h.create)
	r.Get("/databases/{databaseId}/migration-history", h.history)
	r.Get("/migrations/{migrationId}", h.get)
	r.Put("/migrations/{migrationId}", h.update)
	r.Post("/migrations/{migrationId}/submit", h.submit)
	r.Get("/migrations/{migrationId}/approvals", h.approvals)
	r.Post("/migrations/{migrationId}/approvals", h.decide)
	r.Post("/migrations/{migrationId}/rollback", h.rollback)
	r.Post("/migrations/{migrationId}/forward", h.forward)
}

type handler struct{ s *Service }

// DecisionInput adalah body POST /migrations/{id}/approvals.
type DecisionInput struct {
	Kind     Kind             `json:"kind"`
	Decision ApprovalDecision `json:"decision"`
	Comment  string           `json:"comment"`
}

// ApprovalsView adalah isi halaman review: siapa yang wajib dan keputusannya.
type ApprovalsView struct {
	Required  []Approver `json:"required"`
	Approvals []Approval `json:"approvals"`
}

func actorFrom(w http.ResponseWriter, r *http.Request) (Actor, bool) {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, r, apperr.ErrUnauthenticated)
		return Actor{}, false
	}
	return Actor{UserID: p.UserID}, true
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		httpx.Error(w, r, apperr.New(apperr.CodeNotFound, "tidak ditemukan"))
		return 0, false
	}
	return id, true
}

// toAPIError memetakan error domain ke apperr agar httpx menjawab 4xx, bukan 500.
func toAPIError(err error) error {
	var ae *apperr.Error
	switch {
	case err == nil, errors.As(err, &ae):
		return err
	case errors.Is(err, ErrNotApprover):
		return apperr.Wrap(apperr.CodeForbidden, "hanya leader group yang wajib menyetujui yang boleh memutuskan", err)
	case errors.Is(err, ErrInvalidDefinition), errors.Is(err, ErrInvalidBatchConfig), errors.Is(err, ErrInvalidBatchSQL):
		return apperr.Wrap(apperr.CodeValidation, err.Error(), err)
	case errors.Is(err, ErrInvalidTransition), errors.Is(err, ErrStatusConflict), errors.Is(err, ErrSQLNotEditable),
		errors.Is(err, ErrIrreversible), errors.Is(err, ErrApprovalNotPending), errors.Is(err, ErrAlreadyDecided),
		errors.Is(err, ErrApprovalIncomplete), errors.Is(err, ErrApplyBlocked), errors.Is(err, ErrRollbackBlocked),
		errors.Is(err, ErrForwardBlocked), errors.Is(err, ErrNoPendingMigration):
		return apperr.Wrap(apperr.CodeConflict, err.Error(), err)
	}
	return err
}

func (h handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpx.Error(w, r, toAPIError(err))
}

func (h handler) list(w http.ResponseWriter, r *http.Request) {
	a, ok := actorFrom(w, r)
	dbID, ok2 := pathID(w, r, "databaseId")
	if !ok || !ok2 {
		return
	}
	items, err := h.s.List(r.Context(), a, dbID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if items == nil {
		items = []Migration{}
	}
	httpx.JSON(w, http.StatusOK, items)
}

func (h handler) create(w http.ResponseWriter, r *http.Request) {
	a, ok := actorFrom(w, r)
	dbID, ok2 := pathID(w, r, "databaseId")
	if !ok || !ok2 {
		return
	}
	var d Definition
	if err := httpx.Decode(w, r, &d); err != nil {
		httpx.Error(w, r, err)
		return
	}
	m, err := h.s.Create(r.Context(), a, dbID, d)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, m)
}

func (h handler) get(w http.ResponseWriter, r *http.Request) {
	a, ok := actorFrom(w, r)
	id, ok2 := pathID(w, r, "migrationId")
	if !ok || !ok2 {
		return
	}
	m, err := h.s.Get(r.Context(), a, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m)
}

func (h handler) update(w http.ResponseWriter, r *http.Request) {
	a, ok := actorFrom(w, r)
	id, ok2 := pathID(w, r, "migrationId")
	if !ok || !ok2 {
		return
	}
	var d Definition
	if err := httpx.Decode(w, r, &d); err != nil {
		httpx.Error(w, r, err)
		return
	}
	m, err := h.s.UpdateDraft(r.Context(), a, id, d)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m)
}

func (h handler) submit(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, h.s.SubmitForReview)
}

func (h handler) rollback(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, h.s.RequestRollback)
}

func (h handler) forward(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, h.s.RequestForward)
}

// act menjalankan aksi tanpa body dan menjawab migration terkini.
func (h handler) act(w http.ResponseWriter, r *http.Request, do func(context.Context, Actor, int64) error) {
	a, ok := actorFrom(w, r)
	id, ok2 := pathID(w, r, "migrationId")
	if !ok || !ok2 {
		return
	}
	if err := do(r.Context(), a, id); err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.s.Get(r.Context(), a, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m)
}

func (h handler) approvals(w http.ResponseWriter, r *http.Request) {
	a, ok := actorFrom(w, r)
	id, ok2 := pathID(w, r, "migrationId")
	if !ok || !ok2 {
		return
	}
	kind := Kind(r.URL.Query().Get("kind"))
	if kind == "" {
		kind = KindApply
	}
	if !kind.IsValid() {
		httpx.Error(w, r, apperr.Validation(map[string]string{"kind": "harus apply, rollback, atau forward"}))
		return
	}
	got, err := h.s.ListApprovals(r.Context(), a, id, kind)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.s.Get(r.Context(), a, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	req, err := h.s.RequiredApprovers(r.Context(), m.DatabaseID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if got == nil {
		got = []Approval{}
	}
	if req == nil {
		req = []Approver{}
	}
	httpx.JSON(w, http.StatusOK, ApprovalsView{Required: req, Approvals: got})
}

func (h handler) decide(w http.ResponseWriter, r *http.Request) {
	a, ok := actorFrom(w, r)
	id, ok2 := pathID(w, r, "migrationId")
	if !ok || !ok2 {
		return
	}
	var in DecisionInput
	if err := httpx.Decode(w, r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	fields := map[string]string{}
	if !in.Kind.IsValid() {
		fields["kind"] = "harus apply, rollback, atau forward"
	}
	if !in.Decision.IsDecided() {
		fields["decision"] = "harus approved atau rejected"
	}
	if len(in.Comment) > maxDescriptionLen {
		fields["comment"] = "terlalu panjang"
	}
	if len(fields) > 0 {
		httpx.Error(w, r, apperr.Validation(fields))
		return
	}
	if err := h.s.Decide(r.Context(), a, id, in.Kind, in.Decision, in.Comment); err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.s.Get(r.Context(), a, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m)
}

func (h handler) history(w http.ResponseWriter, r *http.Request) {
	a, ok := actorFrom(w, r)
	dbID, ok2 := pathID(w, r, "databaseId")
	if !ok || !ok2 {
		return
	}
	q := r.URL.Query()
	f := HistoryFilter{HideRollback: q.Get("hideRollback") == "true"}
	if v := q.Get("migrationId"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			httpx.Error(w, r, apperr.Validation(map[string]string{"migrationId": "harus bilangan bulat positif"}))
			return
		}
		f.MigrationID = &id
	}
	items, err := h.s.ListHistory(r.Context(), a, dbID, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if items == nil {
		items = []Execution{}
	}
	httpx.JSON(w, http.StatusOK, items)
}
