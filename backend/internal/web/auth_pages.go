package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
)

const defaultLanding = "/databases"

type loginView struct {
	Username string
	Next     string
	Error    string
	Expired  bool
	Errors   map[string]string
}

func (s *Server) mountAuth(r chi.Router) {
	r.Get("/login", s.loginForm)
	r.Post("/login", s.loginSubmit)
}

func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return defaultLanding
	}
	if strings.ContainsAny(next, "\r\n\t") {
		return defaultLanding
	}
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path == "/login" || strings.HasPrefix(u.Path, "/login/") {
		return defaultLanding
	}
	return next
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	next := safeNext(r.URL.Query().Get("next"))
	if _, err := s.guard.Authenticate(w, r); err == nil {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	s.Render(w, r, http.StatusOK, "auth/login", loginView{
		Next:    r.URL.Query().Get("next"),
		Expired: r.URL.Query().Get("reason") == "expired",
	})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		s.Fail(w, r, apperr.New(apperr.CodeValidation, "isi form tidak valid"))
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	next := r.PostFormValue("next")
	view := loginView{Username: username, Next: next}

	err := s.guard.Login(w, r, username, r.PostFormValue("password"))
	if err == nil {
		http.Redirect(w, r, safeNext(next), http.StatusSeeOther)
		return
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		s.Fail(w, r, err)
		return
	}
	switch ae.Code {
	case apperr.CodeValidation:
		view.Errors = ae.Fields
		s.Render(w, r, http.StatusUnprocessableEntity, "auth/login", view)
	case apperr.CodeUnauthenticated:
		view.Error = "Username atau password salah."
		s.Render(w, r, http.StatusUnprocessableEntity, "auth/login", view)
	default:
		s.Fail(w, r, err)
	}
}
