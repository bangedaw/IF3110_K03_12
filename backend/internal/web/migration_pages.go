package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/explorer"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
)

const migrationPageSize = 50

type migrationDrafts struct{ svc *migration.Service }

func (d migrationDrafts) SaveDraft(ctx context.Context, p authz.Principal, databaseID int64, draft Draft) (int64, error) {
	m, err := d.svc.Create(ctx, p, databaseID, migration.CreateInput{Title: draft.Title, Description: draft.Description, SQL: draft.SQL})
	if err != nil {
		return 0, err
	}
	return m.ID, nil
}

type migrationListView struct {
	Header  dbHeader
	Items   []migration.Migration
	Filters []tabLink
	CanEdit bool
	Head    int
	NewURL  string
}

type migrationFormView struct {
	Header      dbHeader
	Action      string
	Title       string
	Description string
	SQL         string
	Revision    int
	IsMySQL     bool
	Errors      map[string]string
}

type migrationDetailView struct {
	Header  dbHeader
	M       *migration.Migration
	Runs    []migration.Run
	CanEdit bool
	BaseURL string
	Form    migrationFormView
}

type migrationExecuteView struct {
	Header      dbHeader
	M           *migration.Migration
	Head        int
	HeadChanged bool
	IsMySQL     bool
	BaseURL     string
}

type migrationHistoryView struct {
	Header  dbHeader
	Runs    []migration.Run
	PrevURL string
	NextURL string
}

type explainRow struct {
	Index       int
	SQL         string
	Explainable bool
	Notice      string
	Plan        string
}

type migrationExplainView struct {
	Header  dbHeader
	M       *migration.Migration
	BaseURL string
	Analyze bool
	Ran     bool
	Rows    []explainRow
	Error   string
}

func (s *Server) mountMigrations(r chi.Router) {
	r.Get("/databases/{databaseId}/migrations", s.migrationList)
	r.Get("/databases/{databaseId}/migrations/new", s.migrationNewForm)
	r.Post("/databases/{databaseId}/migrations/new", s.migrationCreate)
	r.Get("/databases/{databaseId}/migrations/{migrationId}", s.migrationDetail)
	r.Post("/databases/{databaseId}/migrations/{migrationId}", s.migrationUpdate)
	r.Get("/databases/{databaseId}/migrations/{migrationId}/delete", s.migrationDeleteConfirm)
	r.Post("/databases/{databaseId}/migrations/{migrationId}/delete", s.migrationDelete)
	r.Get("/databases/{databaseId}/migrations/{migrationId}/execute", s.migrationExecuteConfirm)
	r.Post("/databases/{databaseId}/migrations/{migrationId}/execute", s.migrationExecute)
	r.Get("/databases/{databaseId}/migrations/{migrationId}/explain", s.migrationExplainForm)
	r.Post("/databases/{databaseId}/migrations/{migrationId}/explain", s.migrationExplain)
	r.Get("/databases/{databaseId}/history", s.migrationHistory)
}

func migrationPath(databaseID, migrationID int64) string {
	return fmt.Sprintf("%s/migrations/%d", databasePath(databaseID), migrationID)
}

func (s *Server) migrationTarget(r *http.Request) (authz.Principal, int64, *explorer.DatabaseDetail, *migration.Migration, error) {
	p, id, db, err := s.diffContext(r)
	if err != nil {
		return p, 0, nil, nil, err
	}
	mid, err := idParam(r, "migrationId")
	if err != nil {
		return p, 0, nil, nil, err
	}
	m, err := s.migrations.Get(r.Context(), p, id, mid)
	return p, id, db, m, err
}

func requireEdit(db *explorer.DatabaseDetail) error {
	if db.Level != authz.LevelEdit {
		return authz.ErrForbidden
	}
	return nil
}

func (s *Server) migrationList(w http.ResponseWriter, r *http.Request) {
	p, id, db, err := s.diffContext(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	status := r.URL.Query().Get("status")
	items, _, err := s.migrations.List(r.Context(), p, id, status, 200, 0)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	head, err := s.migrations.Head(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	base := databasePath(id) + "/migrations"
	view := migrationListView{
		Header:  s.header(db, "/migrations"),
		Items:   items,
		CanEdit: db.Level == authz.LevelEdit,
		Head:    head,
		NewURL:  base + "/new",
		Filters: []tabLink{
			{Label: "Semua", URL: base, Current: status != "draft" && status != "applied"},
			{Label: "Draft", URL: base + "?status=draft", Current: status == "draft"},
			{Label: "Applied", URL: base + "?status=applied", Current: status == "applied"},
		},
	}
	s.Render(w, r, http.StatusOK, "migrations/list", view)
}

func (s *Server) migrationNewForm(w http.ResponseWriter, r *http.Request) {
	_, id, db, err := s.diffContext(r)
	if err == nil {
		err = requireEdit(db)
	}
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "migrations/form", migrationFormView{
		Header:  s.header(db, "/migrations"),
		Action:  databasePath(id) + "/migrations/new",
		IsMySQL: db.Kind == "mysql",
	})
}

func (s *Server) migrationCreate(w http.ResponseWriter, r *http.Request) {
	p, id, db, err := s.diffContext(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	in := migration.CreateInput{Title: r.PostFormValue("title"), Description: r.PostFormValue("description"), SQL: r.PostFormValue("sql")}
	m, err := s.migrations.Create(r.Context(), p, id, in)
	if err != nil {
		if errs, status, ok := formErrors(err, "form"); ok {
			s.Render(w, r, status, "migrations/form", migrationFormView{
				Header: s.header(db, "/migrations"), Action: databasePath(id) + "/migrations/new",
				Title: in.Title, Description: in.Description, SQL: in.SQL, IsMySQL: db.Kind == "mysql", Errors: errs,
			})
			return
		}
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, migrationPath(id, m.ID), &Flash{Kind: "success", Message: fmt.Sprintf("Draft migration #%d tersimpan.", m.ID)})
}

func (s *Server) renderDetail(w http.ResponseWriter, r *http.Request, p authz.Principal, db *explorer.DatabaseDetail, m *migration.Migration, form *migrationFormView, status int) {
	runs, err := s.migrations.Runs(r.Context(), p, db.ID, m.ID)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	view := migrationDetailView{
		Header:  s.header(db, "/migrations"),
		M:       m,
		Runs:    runs,
		CanEdit: db.Level == authz.LevelEdit && m.Status == migration.Draft,
		BaseURL: migrationPath(db.ID, m.ID),
	}
	if form != nil {
		view.Form = *form
	} else {
		view.Form = migrationFormView{Title: m.Title, Description: m.Description, SQL: m.SQL, Revision: m.Revision}
	}
	view.Form.Action = view.BaseURL
	view.Form.IsMySQL = db.Kind == "mysql"
	s.Render(w, r, status, "migrations/detail", view)
}

func (s *Server) migrationDetail(w http.ResponseWriter, r *http.Request) {
	p, _, db, m, err := s.migrationTarget(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.renderDetail(w, r, p, db, m, nil, http.StatusOK)
}

func (s *Server) migrationUpdate(w http.ResponseWriter, r *http.Request) {
	p, id, db, m, err := s.migrationTarget(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	revision, _ := strconv.Atoi(r.PostFormValue("revision"))
	in := migration.UpdateInput{Title: r.PostFormValue("title"), Description: r.PostFormValue("description"), SQL: r.PostFormValue("sql"), Revision: revision}
	if _, err := s.migrations.Update(r.Context(), p, id, m.ID, in); err != nil {
		if errs, status, ok := formErrors(err, "form"); ok {
			s.renderDetail(w, r, p, db, m, &migrationFormView{Title: in.Title, Description: in.Description, SQL: in.SQL, Revision: m.Revision, Errors: errs}, status)
			return
		}
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, migrationPath(id, m.ID), &Flash{Kind: "success", Message: "Draft migration diperbarui."})
}

func (s *Server) migrationDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	_, id, db, m, err := s.migrationTarget(r)
	if err == nil {
		err = requireEdit(db)
	}
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "migrations/delete", migrationExecuteView{Header: s.header(db, "/migrations"), M: m, BaseURL: migrationPath(id, m.ID)})
}

func (s *Server) migrationDelete(w http.ResponseWriter, r *http.Request) {
	p, id, _, m, err := s.migrationTarget(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	revision, _ := strconv.Atoi(r.PostFormValue("revision"))
	if err := s.migrations.Delete(r.Context(), p, id, m.ID, revision); err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, databasePath(id)+"/migrations", &Flash{Kind: "success", Message: fmt.Sprintf("Draft migration #%d dihapus.", m.ID)})
}

func (s *Server) migrationExecuteConfirm(w http.ResponseWriter, r *http.Request) {
	p, id, db, m, err := s.migrationTarget(r)
	if err == nil {
		err = requireEdit(db)
	}
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	head, err := s.migrations.Head(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "migrations/execute", migrationExecuteView{
		Header:      s.header(db, "/migrations"),
		M:           m,
		Head:        head,
		HeadChanged: head != m.BaseVersion,
		IsMySQL:     db.Kind == "mysql",
		BaseURL:     migrationPath(id, m.ID),
	})
}

func (s *Server) migrationExecute(w http.ResponseWriter, r *http.Request) {
	p, id, _, m, err := s.migrationTarget(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	revision, _ := strconv.Atoi(r.PostFormValue("revision"))
	run, err := s.migrations.Execute(r.Context(), p, id, m.ID, migration.ExecuteInput{
		ExpectedRevision:       revision,
		AcknowledgeHeadChanged: r.PostFormValue("acknowledgeHeadChanged") != "",
	})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	if run.Status != migration.RunSucceeded {
		msg := "Eksekusi gagal: " + run.ErrorMessage
		if run.FailedStatementIndex != nil {
			msg = fmt.Sprintf("Eksekusi gagal pada statement %d: %s", *run.FailedStatementIndex, run.ErrorMessage)
		}
		s.Redirect(w, r, migrationPath(id, m.ID), &Flash{Kind: "error", Message: msg + " Semua perubahan di-rollback."})
		return
	}
	s.Redirect(w, r, migrationPath(id, m.ID), &Flash{Kind: "success", Message: fmt.Sprintf("Migration berhasil diterapkan sebagai versi %d.", *run.Version)})
}

func (s *Server) migrationExplainForm(w http.ResponseWriter, r *http.Request) {
	_, id, db, m, err := s.migrationTarget(r)
	if err == nil {
		err = requireEdit(db)
	}
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "migrations/explain", migrationExplainView{Header: s.header(db, "/migrations"), M: m, BaseURL: migrationPath(id, m.ID)})
}

func (s *Server) migrationExplain(w http.ResponseWriter, r *http.Request) {
	p, id, db, m, err := s.migrationTarget(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	analyze := r.PostFormValue("analyze") != ""
	view := migrationExplainView{Header: s.header(db, "/migrations"), M: m, BaseURL: migrationPath(id, m.ID), Analyze: analyze, Ran: true}
	res, err := s.migrations.Explain(r.Context(), p, id, m.ID, analyze)
	var ae *apperr.Error
	if errors.As(err, &ae) && ae.Code == apperr.CodeMigrationFailed {
		view.Error = messageOr(ae, "EXPLAIN gagal dijalankan")
		s.Render(w, r, http.StatusUnprocessableEntity, "migrations/explain", view)
		return
	}
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	if er, ok := res.(*dbms.ExplainResult); ok {
		for _, st := range er.Statements {
			row := explainRow{Index: st.Index, SQL: st.SQL, Explainable: st.Explainable, Notice: st.Notice}
			if st.Plan != nil {
				if raw, err := json.MarshalIndent(st.Plan, "", "  "); err == nil {
					row.Plan = string(raw)
				}
			}
			view.Rows = append(view.Rows, row)
		}
	}
	s.Render(w, r, http.StatusOK, "migrations/explain", view)
}

func (s *Server) migrationHistory(w http.ResponseWriter, r *http.Request) {
	p, id, db, err := s.diffContext(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	offset := max(queryInt(r, "offset", 0), 0)
	runs, total, err := s.migrations.History(r.Context(), p, id, migrationPageSize, offset)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	view := migrationHistoryView{Header: s.header(db, "/history"), Runs: runs}
	base := databasePath(id) + "/history"
	if offset > 0 {
		view.PrevURL = fmt.Sprintf("%s?offset=%d", base, max(offset-migrationPageSize, 0))
	}
	if offset+migrationPageSize < total {
		view.NextURL = fmt.Sprintf("%s?offset=%d", base, offset+migrationPageSize)
	}
	s.Render(w, r, http.StatusOK, "migrations/history", view)
}
