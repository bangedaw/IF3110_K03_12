package web

import (
	"net/http"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
)

type Identity struct {
	Principal authz.Principal
	Username  string
	CSRF      string
	handle    any
}

type Guard interface {
	Authenticate(w http.ResponseWriter, r *http.Request) (Identity, error)
	Logout(w http.ResponseWriter, r *http.Request, id Identity) error
}

type AuthGuard struct{ svc *auth.Service }

func NewAuthGuard(svc *auth.Service) *AuthGuard { return &AuthGuard{svc: svc} }

func (g *AuthGuard) Authenticate(w http.ResponseWriter, r *http.Request) (Identity, error) {
	id, err := g.svc.WebAuthenticate(w, r)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Principal: id.Principal, Username: id.Username, CSRF: id.CSRF, handle: id}, nil
}

func (g *AuthGuard) Logout(w http.ResponseWriter, r *http.Request, id Identity) error {
	handle, ok := id.handle.(auth.WebIdentity)
	if !ok {
		return nil
	}
	return g.svc.WebLogout(w, r, handle)
}
