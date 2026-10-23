package auth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

const CookieName = "dbmp_session"

func Routes(r chi.Router, s *Service) {
	r.Post("/auth/login", s.handleLogin)
	r.With(s.Middleware).Post("/auth/logout", s.handleLogout)
	r.With(s.Middleware).Get("/auth/me", s.handleMe)
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil || c.Value == "" {
			httpx.Error(w, r, apperr.ErrUnauthenticated)
			return
		}
		sess, err := s.Authenticate(r.Context(), c.Value)
		if err != nil {
			s.clearCookie(w)
			httpx.Error(w, r, err)
			return
		}
		if sess.NewToken != "" {
			s.setCookie(w, sess.NewToken, sess.ExpiresAt)
		}
		if !safeMethod(r.Method) && !s.validCSRF(sess.FamilyID, r.Header.Get(CSRFHeader)) {
			httpx.Error(w, r, apperr.New(apperr.CodeCSRFInvalid, "token CSRF tidak valid"))
			return
		}
		ctx := withFamily(WithPrincipal(r.Context(), sess.Principal), sess.FamilyID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in apigen.LoginRequest
	if err := httpx.Decode(w, r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || in.Password == "" {
		httpx.Error(w, r, apperr.Validation(map[string]string{"username": "wajib diisi", "password": "wajib diisi"}))
		return
	}
	user, sess, err := s.Login(r.Context(), LoginInput{
		Username:  in.Username,
		Password:  in.Password,
		UserAgent: r.UserAgent(),
		IP:        clientIP(r),
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	s.setCookie(w, sess.NewToken, sess.ExpiresAt)
	httpx.JSON(w, http.StatusOK, apigen.AuthSession{User: toAPIUser(user), CsrfToken: s.csrfToken(sess.FamilyID)})
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	id, _ := familyFrom(r.Context())
	if err := s.Logout(r.Context(), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	id, _ := familyFrom(r.Context())
	user, err := s.q.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, apigen.AuthSession{User: toAPIUser(user), CsrfToken: s.csrfToken(id)})
}

func (s *Service) setCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   max(int(expires.Sub(s.now()).Seconds()), 1),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clientIP(r *http.Request) *netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return nil
	}
	return &ip
}

func toAPIUser(u gen.User) apigen.User {
	return apigen.User{
		Id:              u.ID,
		Username:        u.Username,
		DisplayName:     u.DisplayName,
		IsPlatformAdmin: u.IsPlatformAdmin,
		IsActive:        u.IsActive,
		CreatedAt:       u.CreatedAt.Time,
		UpdatedAt:       u.UpdatedAt.Time,
	}
}
