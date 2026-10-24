package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
)

const connectionListLimit = 200

var sslModes = []string{"disable", "require", "verify-ca", "verify-full"}

type connectionFormView struct {
	ID           int64
	Editing      bool
	Name         string
	Kind         string
	Host         string
	Port         string
	DatabaseName string
	Username     string
	SSLMode      string
	IsActive     bool
	HasPassword  bool
	Errors       map[string]string
	TestResult   *dbconn.TestResult
	TestError    string
	SSLModes     []string
}

type connectionsListView struct {
	Items []dbconn.Connection
}

type connectionDeleteView struct {
	Connection dbconn.Connection
}

func (s *Server) mountConnections(r chi.Router) {
	r.Get("/admin/connections", s.connectionList)
	r.Get("/admin/connections/new", s.connectionNewForm)
	r.Post("/admin/connections/new", s.connectionCreate)
	r.Post("/admin/connections/test", s.connectionTestForm)
	r.Get("/admin/connections/{databaseId}", s.connectionEditForm)
	r.Post("/admin/connections/{databaseId}", s.connectionUpdate)
	r.Get("/admin/connections/{databaseId}/delete", s.connectionDeleteConfirm)
	r.Post("/admin/connections/{databaseId}/delete", s.connectionDelete)
	r.Post("/admin/connections/{databaseId}/test", s.connectionTestSaved)
}

func (s *Server) authorizeAction(r *http.Request, action authz.Action) (authz.Principal, error) {
	p, err := s.principal(r)
	if err != nil {
		return p, err
	}
	if s.az != nil {
		return p, s.az.Authorize(r.Context(), p, action, authz.Resource{})
	}
	if !p.IsPlatformAdmin {
		return p, authz.ErrForbidden
	}
	return p, nil
}

func (s *Server) adminPrincipal(r *http.Request) (authz.Principal, error) {
	return s.authorizeAction(r, authz.ActionManageConnections)
}

func (s *Server) connectionList(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminPrincipal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	page, err := s.conns.List(r.Context(), p, connectionListLimit, 0)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "connections/list", connectionsListView{Items: page.Items})
}

func newConnectionForm() connectionFormView {
	return connectionFormView{Kind: "postgres", Port: "5432", SSLMode: "disable", IsActive: true, SSLModes: sslModes}
}

func (s *Server) connectionNewForm(w http.ResponseWriter, r *http.Request) {
	if _, err := s.adminPrincipal(r); err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "connections/form", newConnectionForm())
}

func formFromConnection(c *dbconn.Connection) connectionFormView {
	return connectionFormView{
		ID:           c.ID,
		Editing:      true,
		Name:         c.Name,
		Kind:         c.Kind,
		Host:         c.Host,
		Port:         strconv.Itoa(c.Port),
		DatabaseName: c.DatabaseName,
		Username:     c.Username,
		SSLMode:      c.SSLMode,
		IsActive:     c.IsActive,
		HasPassword:  c.HasPassword,
		SSLModes:     sslModes,
	}
}

func (s *Server) connectionEditForm(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminPrincipal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "databaseId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	conn, err := s.conns.Get(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "connections/form", formFromConnection(conn))
}

func readConnectionForm(r *http.Request, id int64) connectionFormView {
	return connectionFormView{
		ID:           id,
		Editing:      id > 0,
		Name:         strings.TrimSpace(r.PostFormValue("name")),
		Kind:         r.PostFormValue("kind"),
		Host:         strings.TrimSpace(r.PostFormValue("host")),
		Port:         strings.TrimSpace(r.PostFormValue("port")),
		DatabaseName: strings.TrimSpace(r.PostFormValue("databaseName")),
		Username:     strings.TrimSpace(r.PostFormValue("username")),
		SSLMode:      r.PostFormValue("sslMode"),
		IsActive:     r.PostFormValue("isActive") != "",
		SSLModes:     sslModes,
	}
}

func (f connectionFormView) port() (int, bool) {
	n, err := strconv.Atoi(f.Port)
	return n, err == nil
}

func (f connectionFormView) input(password string) (dbconn.Input, bool) {
	port, ok := f.port()
	active := f.IsActive
	return dbconn.Input{
		Name:         f.Name,
		Kind:         f.Kind,
		Host:         f.Host,
		Port:         port,
		DatabaseName: f.DatabaseName,
		Username:     f.Username,
		Password:     password,
		SSLMode:      f.SSLMode,
		IsActive:     &active,
	}, ok
}

func (s *Server) renderConnectionForm(w http.ResponseWriter, r *http.Request, status int, f connectionFormView) {
	s.Render(w, r, status, "connections/form", f)
}

func (s *Server) connectionFormError(w http.ResponseWriter, r *http.Request, f connectionFormView, err error) {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		s.Fail(w, r, err)
		return
	}
	switch ae.Code {
	case apperr.CodeValidation:
		f.Errors = ae.Fields
		if len(f.Errors) == 0 {
			f.Errors = map[string]string{"form": messageOr(ae, "input tidak valid")}
		}
		s.renderConnectionForm(w, r, http.StatusUnprocessableEntity, f)
	case apperr.CodeConflict:
		f.Errors = map[string]string{"name": "nama koneksi sudah dipakai"}
		s.renderConnectionForm(w, r, http.StatusConflict, f)
	default:
		s.Fail(w, r, err)
	}
}

func (s *Server) connectionCreate(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminPrincipal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	f := readConnectionForm(r, 0)
	in, portOK := f.input(r.PostFormValue("password"))
	if !portOK {
		f.Errors = map[string]string{"port": "harus berupa angka"}
		s.renderConnectionForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	conn, err := s.conns.Create(r.Context(), p, in)
	if err != nil {
		s.connectionFormError(w, r, f, err)
		return
	}
	s.Redirect(w, r, "/admin/connections", &Flash{Kind: "success", Message: fmt.Sprintf("Koneksi %q ditambahkan.", conn.Name)})
}

func (s *Server) connectionUpdate(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminPrincipal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "databaseId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	f := readConnectionForm(r, id)
	f.HasPassword = true
	port, portOK := f.port()
	if !portOK {
		f.Errors = map[string]string{"port": "harus berupa angka"}
		s.renderConnectionForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	active := f.IsActive
	conn, err := s.conns.Update(r.Context(), p, id, dbconn.Patch{
		Name:         &f.Name,
		Kind:         &f.Kind,
		Host:         &f.Host,
		Port:         &port,
		DatabaseName: &f.DatabaseName,
		Username:     &f.Username,
		Password:     r.PostFormValue("password"),
		SSLMode:      &f.SSLMode,
		IsActive:     &active,
	})
	if err != nil {
		s.connectionFormError(w, r, f, err)
		return
	}
	s.Redirect(w, r, "/admin/connections", &Flash{Kind: "success", Message: fmt.Sprintf("Koneksi %q diperbarui.", conn.Name)})
}

func (s *Server) connectionDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminPrincipal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "databaseId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	conn, err := s.conns.Get(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "connections/delete", connectionDeleteView{Connection: *conn})
}

func (s *Server) connectionDelete(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminPrincipal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "databaseId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	if err := s.conns.Delete(r.Context(), p, id); err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, "/admin/connections", &Flash{Kind: "success", Message: "Koneksi dihapus."})
}

func testFlash(name string, res *dbconn.TestResult, err error) *Flash {
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return &Flash{Kind: "error", Message: fmt.Sprintf("Koneksi %q gagal: %s", name, messageOr(ae, "tidak dapat terhubung"))}
		}
		return nil
	}
	return &Flash{Kind: "success", Message: fmt.Sprintf("Koneksi %q berhasil (%d ms).", name, res.LatencyMs)}
}

func (s *Server) connectionTestSaved(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminPrincipal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "databaseId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	conn, err := s.conns.Get(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	res, err := s.conns.Test(r.Context(), p, dbconn.Input{}, &id)
	flash := testFlash(conn.Name, res, err)
	if flash == nil {
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, "/admin/connections", flash)
}

func (s *Server) connectionTestForm(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminPrincipal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	f := readConnectionForm(r, id)
	f.HasPassword = id > 0
	password := r.PostFormValue("password")

	var res *dbconn.TestResult
	if id > 0 && password == "" {
		res, err = s.conns.Test(r.Context(), p, dbconn.Input{}, &id)
	} else {
		in, portOK := f.input(password)
		if !portOK {
			f.Errors = map[string]string{"port": "harus berupa angka"}
			s.renderConnectionForm(w, r, http.StatusUnprocessableEntity, f)
			return
		}
		res, err = s.conns.Test(r.Context(), p, in, nil)
	}
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Code == apperr.CodeDBUnreachable {
			f.TestError = messageOr(ae, "tidak dapat terhubung")
			s.renderConnectionForm(w, r, http.StatusOK, f)
			return
		}
		s.connectionFormError(w, r, f, err)
		return
	}
	f.TestResult = res
	s.renderConnectionForm(w, r, http.StatusOK, f)
}
