package auth

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
)

type WebIdentity struct {
	Principal authz.Principal
	Username  string
	CSRF      string
	FamilyID  uuid.UUID
}

func (s *Service) WebAuthenticate(w http.ResponseWriter, r *http.Request) (WebIdentity, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return WebIdentity{}, apperr.ErrUnauthenticated
	}
	sess, err := s.Authenticate(r.Context(), c.Value)
	if err != nil {
		s.clearCookie(w)
		return WebIdentity{}, err
	}
	if sess.NewToken != "" {
		s.setCookie(w, sess.NewToken, sess.ExpiresAt)
	}
	user, err := s.q.GetUserByID(r.Context(), sess.Principal.UserID)
	if err != nil {
		return WebIdentity{}, err
	}
	return WebIdentity{
		Principal: sess.Principal,
		Username:  user.Username,
		CSRF:      s.csrfToken(sess.FamilyID),
		FamilyID:  sess.FamilyID,
	}, nil
}

func (s *Service) WebLogout(w http.ResponseWriter, r *http.Request, id WebIdentity) error {
	if err := s.Logout(r.Context(), id.FamilyID); err != nil {
		return err
	}
	s.clearCookie(w)
	return nil
}
