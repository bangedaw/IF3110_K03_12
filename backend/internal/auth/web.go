package auth

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
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

func (s *Service) WebLogin(w http.ResponseWriter, r *http.Request, username, password string) error {
	username = strings.TrimSpace(username)
	fields := map[string]string{}
	if username == "" {
		fields["username"] = "wajib diisi"
	}
	if password == "" {
		fields["password"] = "wajib diisi"
	}
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}
	user, sess, err := s.Login(r.Context(), LoginInput{
		Username:  username,
		Password:  password,
		UserAgent: r.UserAgent(),
		IP:        clientIP(r),
	})
	entry := audit.Entry{Action: "auth.login", ResourceType: "user", Outcome: audit.OutcomeSuccess, Metadata: map[string]any{"username": username}}
	if err != nil {
		if errors.Is(err, errInvalidCredentials) {
			entry.Outcome = audit.OutcomeFailure
			s.audit.Record(r.Context(), entry)
		}
		return err
	}
	entry.ActorID, entry.ResourceID = user.ID, strconv.FormatInt(user.ID, 10)
	s.audit.Record(r.Context(), entry)
	s.setCookie(w, sess.NewToken, sess.ExpiresAt)
	return nil
}
