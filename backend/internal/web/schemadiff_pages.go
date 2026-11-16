package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/explorer"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schemadiff"
)

const maxDraftTitle = 200

type snapshotRow struct {
	ID    int64
	Label string
}

type diffFormView struct {
	Header    dbHeader
	Databases []explorer.Database
	Snapshots []snapshotRow
	Error     string
}

type changeRow struct {
	Text        string
	Destructive bool
}

type diffResultView struct {
	Header      dbHeader
	Against     string
	Changes     []changeRow
	Warnings    []string
	SQL         string
	Title       string
	Destructive bool
	CanSave     bool
	SaveNotice  string
}

func (s *Server) mountSchemaDiff(r chi.Router) {
	r.Get("/databases/{databaseId}/diff", s.diffForm)
	r.Post("/databases/{databaseId}/diff", s.diffRun)
	r.Post("/databases/{databaseId}/diff/save", s.diffSave)
	r.Post("/databases/{databaseId}/snapshots", s.snapshotCreate)
}

var changeLabels = map[string]string{
	schemadiff.KindAddTable:       "Tambah tabel",
	schemadiff.KindDropTable:      "Hapus tabel",
	schemadiff.KindAddColumn:      "Tambah kolom",
	schemadiff.KindDropColumn:     "Hapus kolom",
	schemadiff.KindAlterColumn:    "Ubah kolom",
	schemadiff.KindAddConstraint:  "Tambah constraint",
	schemadiff.KindDropConstraint: "Hapus constraint",
	schemadiff.KindAddIndex:       "Tambah index",
	schemadiff.KindDropIndex:      "Hapus index",
}

func describeChange(c schemadiff.Change) string {
	label := changeLabels[c.Kind]
	if label == "" {
		label = c.Kind
	}
	table := c.Table
	if c.Schema != "" {
		table = c.Schema + "." + c.Table
	}
	if c.Kind == schemadiff.KindAddTable || c.Kind == schemadiff.KindDropTable {
		return label + " " + table
	}
	return fmt.Sprintf("%s %s pada %s", label, c.Name, table)
}

func (s *Server) diffContext(r *http.Request) (authz.Principal, int64, *explorer.DatabaseDetail, error) {
	p, err := s.principal(r)
	if err != nil {
		return p, 0, nil, err
	}
	id, err := idParam(r, "databaseId")
	if err != nil {
		return p, 0, nil, err
	}
	db, err := s.explorer.Database(r.Context(), p, id)
	return p, id, db, err
}

func (s *Server) diffFormView(r *http.Request, p authz.Principal, id int64, db *explorer.DatabaseDetail, message string) (diffFormView, error) {
	all, err := s.explorer.Databases(r.Context(), p)
	if err != nil {
		return diffFormView{}, err
	}
	view := diffFormView{Header: s.header(db, "diff"), Error: message}
	for _, d := range all {
		if d.ID != id && d.Kind == db.Kind {
			view.Databases = append(view.Databases, d)
		}
	}
	snaps, err := s.explorer.Snapshots(r.Context(), p, id)
	if err != nil {
		return diffFormView{}, err
	}
	for _, sn := range snaps {
		label := fmt.Sprintf("#%d · %s", sn.ID, sn.TakenAt.Local().Format(time.DateTime))
		if sn.MigrationRunID != nil {
			label += fmt.Sprintf(" · setelah run %d", *sn.MigrationRunID)
		}
		view.Snapshots = append(view.Snapshots, snapshotRow{ID: sn.ID, Label: label})
	}
	return view, nil
}

func (s *Server) diffForm(w http.ResponseWriter, r *http.Request) {
	p, id, db, err := s.diffContext(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	view, err := s.diffFormView(r, p, id, db, "")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "schemadiff/form", view)
}

func (s *Server) diffRun(w http.ResponseWriter, r *http.Request) {
	p, id, db, err := s.diffContext(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	source := r.PostFormValue("source")
	againstID, _ := strconv.ParseInt(r.PostFormValue("against"), 10, 64)

	req := explorer.SchemaDiffRequest{Direction: "toTarget"}
	req.Against.Type = source
	req.Against.ID = againstID
	result, err := s.explorer.SchemaDiff(r.Context(), p, id, req)
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Code == apperr.CodeValidation {
			view, viewErr := s.diffFormView(r, p, id, db, "Pilih pembanding terlebih dahulu.")
			if viewErr != nil {
				s.Fail(w, r, viewErr)
				return
			}
			s.Render(w, r, http.StatusUnprocessableEntity, "schemadiff/form", view)
			return
		}
		s.Fail(w, r, err)
		return
	}

	label := fmt.Sprintf("snapshot #%d", againstID)
	if source == "database" {
		if all, listErr := s.explorer.Databases(r.Context(), p); listErr == nil {
			for _, d := range all {
				if d.ID == againstID {
					label = d.Name
				}
			}
		}
	}
	view := diffResultView{
		Header:   s.header(db, "diff"),
		Against:  label,
		Warnings: result.Warnings,
		SQL:      result.SQL,
		Title:    fmt.Sprintf("Sinkronkan %s dengan %s", db.Name, label),
	}
	for _, c := range result.Changes {
		view.Changes = append(view.Changes, changeRow{Text: describeChange(c), Destructive: c.Destructive})
		view.Destructive = view.Destructive || c.Destructive
	}
	switch {
	case db.Level != authz.LevelEdit:
		view.SaveNotice = "Menyimpan draft butuh akses View + Edit pada " + db.Name + "."
	case s.drafts == nil:
		view.SaveNotice = "Penyimpanan draft migration belum tersedia."
	default:
		view.CanSave = true
	}
	s.Render(w, r, http.StatusOK, "schemadiff/result", view)
}

func (s *Server) hasDatabaseTab(suffix string) bool {
	for _, t := range s.dbTabs {
		if t.Suffix == suffix {
			return true
		}
	}
	return false
}

func (s *Server) diffSave(w http.ResponseWriter, r *http.Request) {
	p, id, db, err := s.diffContext(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	if db.Level != authz.LevelEdit {
		s.Fail(w, r, authz.ErrForbidden)
		return
	}
	if s.az != nil {
		if err := s.az.Authorize(r.Context(), p, authz.ActionMigrationWrite, authz.Resource{DatabaseID: id}); err != nil {
			s.Fail(w, r, err)
			return
		}
	}
	if s.drafts == nil {
		s.Redirect(w, r, databasePath(id)+"/diff", &Flash{Kind: "error", Message: "Penyimpanan draft migration belum tersedia."})
		return
	}
	title := strings.TrimSpace(r.PostFormValue("title"))
	script := strings.TrimSpace(r.PostFormValue("sql"))
	if title == "" || script == "" || len(title) > maxDraftTitle {
		s.Fail(w, r, apperr.New(apperr.CodeValidation, "judul (maksimal 200 karakter) dan SQL wajib diisi"))
		return
	}
	draftID, err := s.drafts.SaveDraft(r.Context(), p, id, Draft{Title: title, Description: "Dihasilkan dari schema diff", SQL: script})
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	flash := &Flash{Kind: "success", Message: fmt.Sprintf("Draft migration #%d tersimpan.", draftID)}
	if s.hasDatabaseTab("/migrations") {
		s.Redirect(w, r, fmt.Sprintf("%s/migrations/%d", databasePath(id), draftID), flash)
		return
	}
	s.Redirect(w, r, databasePath(id)+"/diff", flash)
}

func (s *Server) snapshotCreate(w http.ResponseWriter, r *http.Request) {
	p, id, _, err := s.diffContext(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	snap, err := s.explorer.CreateSnapshot(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, databasePath(id)+"/diff", &Flash{Kind: "success", Message: fmt.Sprintf("Snapshot #%d diambil.", snap.ID)})
}
