package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/admin"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/explorer"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
)

//go:embed templates static
var assets embed.FS

const (
	maxFormBytes = 1 << 20
	csrfField    = "_csrf"
)

type Draft struct {
	Title       string
	Description string
	SQL         string
}

type DraftSaver interface {
	SaveDraft(ctx context.Context, p authz.Principal, databaseID int64, d Draft) (int64, error)
}

type Deps struct {
	Logger      *slog.Logger
	Guard       Guard
	Authz       authz.Authorizer
	Connections *dbconn.Service
	Admin       *admin.Service
	Explorer    *explorer.Service
	Migrations  *migration.Service
	Drafts      DraftSaver
	Reload      bool
	AssetsDir   string
}

type Server struct {
	logger     *slog.Logger
	guard      Guard
	az         authz.Authorizer
	conns      *dbconn.Service
	admin      *admin.Service
	explorer   *explorer.Service
	migrations *migration.Service
	drafts     DraftSaver
	templates  *templateSet
	static     http.Handler
	nav        []NavItem
	dbTabs     []DatabaseTab
}

func New(d Deps) (*Server, error) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Migrations != nil && d.Drafts == nil {
		d.Drafts = migrationDrafts{svc: d.Migrations}
	}
	if d.Guard == nil {
		return nil, errors.New("web: Guard wajib diisi")
	}
	var fsys fs.FS = assets
	reload := false
	if d.Reload && d.AssetsDir != "" {
		if info, err := os.Stat(d.AssetsDir); err == nil && info.IsDir() {
			fsys = os.DirFS(d.AssetsDir)
			reload = true
		}
	}
	tpl, err := newTemplateSet(fsys, reload)
	if err != nil {
		return nil, err
	}
	staticFS, err := fs.Sub(fsys, "static")
	if err != nil {
		return nil, err
	}
	srv := &Server{
		logger:     d.Logger,
		guard:      d.Guard,
		az:         d.Authz,
		conns:      d.Connections,
		admin:      d.Admin,
		explorer:   d.Explorer,
		migrations: d.Migrations,
		drafts:     d.Drafts,
		templates:  tpl,
		static:     http.StripPrefix("/static/", http.FileServerFS(staticFS)),
		nav: []NavItem{
			{Label: "Database", Path: "/databases"},
			{Label: "Koneksi", Path: "/admin/connections", AdminOnly: true},
		},
	}
	if d.Admin != nil {
		srv.nav = append(srv.nav,
			NavItem{Label: "Pengguna", Path: "/admin/users", AdminOnly: true},
			NavItem{Label: "Group", Path: "/admin/groups", AdminOnly: true},
		)
	}
	if d.Migrations != nil {
		srv.dbTabs = append(srv.dbTabs, DatabaseTab{Label: "Migration", Suffix: "/migrations"}, DatabaseTab{Label: "Riwayat", Suffix: "/history"})
	}
	return srv, nil
}

type DatabaseTab struct {
	Label  string
	Suffix string
}

func (s *Server) AddNav(item NavItem) { s.nav = append(s.nav, item) }

func (s *Server) AddDatabaseTab(tab DatabaseTab) { s.dbTabs = append(s.dbTabs, tab) }

func (s *Server) Mount(r chi.Router) {
	r.Get("/static/*", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		s.static.ServeHTTP(w, req)
	})
	s.mountAuth(r)
	r.Group(func(g chi.Router) {
		g.Use(s.protect)
		g.Post("/logout", s.logout)
		s.mountConnections(g)
		if s.admin != nil {
			s.mountAdmin(g)
		}
		s.mountExplorer(g)
		s.mountSchemaDiff(g)
		if s.migrations != nil {
			s.mountMigrations(g)
		}
	})
}

type identityKey struct{}

func identityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func secureHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
}

func (s *Server) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w)
		id, err := s.guard.Authenticate(w, r)
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		ctx := auth.WithPrincipal(context.WithValue(r.Context(), identityKey{}, id), id.Principal)
		r = r.WithContext(ctx)
		if !safeMethod(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
			if err := r.ParseForm(); err != nil {
				s.Fail(w, r, apperr.New(apperr.CodeValidation, "isi form tidak valid atau terlalu besar"))
				return
			}
			if !validCSRF(r.PostFormValue(csrfField), id.CSRF) {
				s.Fail(w, r, apperr.New(apperr.CodeCSRFInvalid, "token CSRF tidak valid"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func validCSRF(got, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	id, _ := identityFrom(r.Context())
	if err := s.guard.Logout(w, r, id); err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, "/login", &Flash{Kind: "info", Message: "Anda telah keluar."})
}

func (s *Server) principal(r *http.Request) (authz.Principal, error) {
	id, ok := identityFrom(r.Context())
	if !ok {
		return authz.Principal{}, apperr.ErrUnauthenticated
	}
	return id.Principal, nil
}

func urlParam(r *http.Request, name string) string {
	v := chi.URLParam(r, name)
	if strings.Contains(v, "%") {
		if decoded, err := url.PathUnescape(v); err == nil {
			return decoded
		}
	}
	return v
}

func idParam(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.ErrNotFound
	}
	return id, nil
}

func queryInt(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

type templateSet struct {
	fsys   fs.FS
	reload bool
	pages  map[string]*template.Template
}

func newTemplateSet(fsys fs.FS, reload bool) (*templateSet, error) {
	t := &templateSet{fsys: fsys, reload: reload}
	pages, err := t.load()
	if err != nil {
		return nil, err
	}
	t.pages = pages
	return t, nil
}

func (t *templateSet) lookup(name string) (*template.Template, error) {
	if t.reload {
		pages, err := t.load()
		if err != nil {
			return nil, err
		}
		t.pages = pages
	}
	tpl, ok := t.pages[name]
	if !ok {
		return nil, fmt.Errorf("template %q tidak ada", name)
	}
	return tpl, nil
}

func (t *templateSet) load() (map[string]*template.Template, error) {
	pages := map[string]*template.Template{}
	err := fs.WalkDir(t.fsys, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		if rel == "layout" || strings.HasPrefix(rel, "partials/") {
			return nil
		}
		tpl, err := template.New("layout").Funcs(funcMap).ParseFS(t.fsys, "templates/layout.html", "templates/partials/*.html", p)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		pages[rel] = tpl
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pages, nil
}
