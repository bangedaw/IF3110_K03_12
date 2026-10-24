package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
)

const flashCookie = "dbmp_flash"

const maxFlashLen = 300

type Flash struct {
	Kind    string `json:"k"`
	Message string `json:"m"`
}

type UserView struct {
	Username string
	IsAdmin  bool
}

type NavItem struct {
	Label     string
	Path      string
	AdminOnly bool
}

type Page struct {
	Path  string
	User  *UserView
	CSRF  string
	Flash *Flash
	Nav   []NavItem
	Data  any
}

type errorView struct {
	Status    int
	Title     string
	Message   string
	RequestID string
}

func (s *Server) page(w http.ResponseWriter, r *http.Request, data any) Page {
	p := Page{Path: r.URL.Path, Data: data, Flash: takeFlash(w, r)}
	id, ok := identityFrom(r.Context())
	if !ok {
		return p
	}
	p.User = &UserView{Username: id.Username, IsAdmin: id.Principal.IsPlatformAdmin}
	p.CSRF = id.CSRF
	for _, item := range s.nav {
		if item.AdminOnly && !id.Principal.IsPlatformAdmin {
			continue
		}
		p.Nav = append(p.Nav, item)
	}
	return p
}

func (s *Server) Render(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	tpl, err := s.templates.lookup(name)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "template tidak ditemukan", "name", name, "err", err)
		http.Error(w, "terjadi kesalahan internal", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", s.page(w, r, data)); err != nil {
		s.logger.ErrorContext(r.Context(), "render template gagal", "name", name, "err", err)
		http.Error(w, "terjadi kesalahan internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) Redirect(w http.ResponseWriter, r *http.Request, to string, f *Flash) {
	if f != nil {
		setFlash(w, *f)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) Fail(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		s.logger.ErrorContext(r.Context(), "kesalahan tidak tertangani", "err", err, "path", r.URL.Path)
		s.renderError(w, r, http.StatusInternalServerError, "Terjadi kesalahan internal", "Permintaan tidak dapat diproses. Coba lagi beberapa saat.")
		return
	}
	if ae.Err != nil {
		s.logger.WarnContext(r.Context(), "kesalahan permintaan", "code", ae.Code, "err", ae.Err, "path", r.URL.Path)
	}
	switch ae.Code {
	case apperr.CodeUnauthenticated, apperr.CodeSessionExpired:
		s.redirectToLogin(w, r, ae.Code == apperr.CodeSessionExpired)
	case apperr.CodeForbidden:
		s.renderError(w, r, http.StatusForbidden, "Akses ditolak", "Anda tidak punya akses ke halaman atau aksi ini.")
	case apperr.CodeCSRFInvalid:
		s.renderError(w, r, http.StatusForbidden, "Permintaan ditolak", "Token keamanan form tidak valid atau kedaluwarsa. Muat ulang halaman lalu coba lagi.")
	case apperr.CodeNotFound:
		s.renderError(w, r, http.StatusNotFound, "Tidak ditemukan", "Halaman atau data yang diminta tidak ditemukan.")
	case apperr.CodeConflict:
		s.renderError(w, r, http.StatusConflict, "Konflik data", messageOr(ae, "Data bertabrakan dengan data lain."))
	case apperr.CodeValidation, apperr.CodeMigrationFailed:
		s.renderError(w, r, http.StatusUnprocessableEntity, "Permintaan tidak valid", messageOr(ae, "Input tidak dapat diproses."))
	case apperr.CodeDBUnreachable:
		s.renderError(w, r, http.StatusServiceUnavailable, "Database tidak terjangkau", "Koneksi ke database target gagal. Periksa konfigurasi koneksi atau coba lagi.")
	default:
		s.renderError(w, r, http.StatusInternalServerError, "Terjadi kesalahan internal", "Permintaan tidak dapat diproses. Coba lagi beberapa saat.")
	}
}

func messageOr(ae *apperr.Error, fallback string) string {
	if ae.Detail != "" {
		return ae.Detail
	}
	return fallback
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	s.Render(w, r, status, "errors/error", errorView{
		Status:    status,
		Title:     title,
		Message:   message,
		RequestID: middleware.GetReqID(r.Context()),
	})
}

func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request, expired bool) {
	q := url.Values{}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		q.Set("next", r.URL.RequestURI())
	}
	if expired {
		q.Set("reason", "expired")
	}
	target := "/login"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func setFlash(w http.ResponseWriter, f Flash) {
	if len(f.Message) > maxFlashLen {
		f.Message = f.Message[:maxFlashLen]
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookie,
		Value:    base64.RawURLEncoding.EncodeToString(raw),
		Path:     "/",
		MaxAge:   60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func takeFlash(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil
	}
	var f Flash
	if json.Unmarshal(raw, &f) != nil || f.Message == "" {
		return nil
	}
	if f.Kind != "success" && f.Kind != "error" {
		f.Kind = "info"
	}
	return &f
}
