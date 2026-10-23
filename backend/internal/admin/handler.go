package admin

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
)

type handlerFunc func(w http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error)

func Routes(r chi.Router, s *Service) {
	r.Get("/admin/users", handle(s.handleListUsers))
	r.Post("/admin/users", handle(s.handleCreateUser))
	r.Get("/admin/users/{userId}", handle(s.handleGetUser))
	r.Patch("/admin/users/{userId}", handle(s.handleUpdateUser))
	r.Delete("/admin/users/{userId}", handle(s.handleDeleteUser))
	r.Post("/admin/users/{userId}/password", handle(s.handleResetPassword))
	r.Post("/admin/users/{userId}/sessions/revoke", handle(s.handleRevokeSessions))

	r.Get("/admin/groups", handle(s.handleListGroups))
	r.Post("/admin/groups", handle(s.handleCreateGroup))
	r.Get("/admin/groups/{groupId}", handle(s.handleGetGroup))
	r.Patch("/admin/groups/{groupId}", handle(s.handleUpdateGroup))
	r.Delete("/admin/groups/{groupId}", handle(s.handleDeleteGroup))
	r.Put("/admin/groups/{groupId}/members/{userId}", handle(s.handleAddMember))
	r.Delete("/admin/groups/{groupId}/members/{userId}", handle(s.handleRemoveMember))
	r.Get("/admin/groups/{groupId}/grants", handle(s.handleListGrants))
	r.Put("/admin/groups/{groupId}/grants/{databaseId}", handle(s.handlePutGrant))
	r.Delete("/admin/groups/{groupId}/grants/{databaseId}", handle(s.handleDeleteGrant))
}

func handle(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.PrincipalFrom(r.Context())
		if !ok {
			httpx.Error(w, r, apperr.ErrUnauthenticated)
			return
		}
		status, body, err := fn(w, r, p)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		if body == nil {
			w.WriteHeader(status)
			return
		}
		httpx.JSON(w, status, body)
	}
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil {
		return 0, apperr.ErrNotFound
	}
	return id, nil
}

func pathIDs(r *http.Request, a, b string) (int64, int64, error) {
	x, err := pathID(r, a)
	if err != nil {
		return 0, 0, err
	}
	y, err := pathID(r, b)
	return x, y, err
}

func (s *Service) handleListUsers(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	limit, offset := httpx.Pagination(r)
	page, err := s.ListUsers(r.Context(), p, limit, offset)
	return http.StatusOK, page, err
}

func (s *Service) handleCreateUser(w http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	var in apigen.UserCreate
	if err := httpx.Decode(w, r, &in); err != nil {
		return 0, nil, err
	}
	u, err := s.CreateUser(r.Context(), p, in)
	return http.StatusCreated, u, err
}

func (s *Service) handleGetUser(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	id, err := pathID(r, "userId")
	if err != nil {
		return 0, nil, err
	}
	u, err := s.GetUser(r.Context(), p, id)
	return http.StatusOK, u, err
}

func (s *Service) handleUpdateUser(w http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	id, err := pathID(r, "userId")
	if err != nil {
		return 0, nil, err
	}
	var in apigen.UserUpdate
	if err := httpx.Decode(w, r, &in); err != nil {
		return 0, nil, err
	}
	u, err := s.UpdateUser(r.Context(), p, id, in)
	return http.StatusOK, u, err
}

func (s *Service) handleDeleteUser(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	id, err := pathID(r, "userId")
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, s.DeleteUser(r.Context(), p, id)
}

func (s *Service) handleResetPassword(w http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	id, err := pathID(r, "userId")
	if err != nil {
		return 0, nil, err
	}
	var in apigen.PasswordReset
	if err := httpx.Decode(w, r, &in); err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, s.ResetPassword(r.Context(), p, id, in.Password)
}

func (s *Service) handleRevokeSessions(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	id, err := pathID(r, "userId")
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, s.RevokeSessions(r.Context(), p, id)
}

func (s *Service) handleListGroups(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	limit, offset := httpx.Pagination(r)
	page, err := s.ListGroups(r.Context(), p, limit, offset)
	return http.StatusOK, page, err
}

func (s *Service) handleCreateGroup(w http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	var in apigen.GroupCreate
	if err := httpx.Decode(w, r, &in); err != nil {
		return 0, nil, err
	}
	g, err := s.CreateGroup(r.Context(), p, in)
	return http.StatusCreated, g, err
}

func (s *Service) handleGetGroup(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	id, err := pathID(r, "groupId")
	if err != nil {
		return 0, nil, err
	}
	g, err := s.GetGroup(r.Context(), p, id)
	return http.StatusOK, g, err
}

func (s *Service) handleUpdateGroup(w http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	id, err := pathID(r, "groupId")
	if err != nil {
		return 0, nil, err
	}
	var in apigen.GroupUpdate
	if err := httpx.Decode(w, r, &in); err != nil {
		return 0, nil, err
	}
	g, err := s.UpdateGroup(r.Context(), p, id, in)
	return http.StatusOK, g, err
}

func (s *Service) handleDeleteGroup(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	id, err := pathID(r, "groupId")
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, s.DeleteGroup(r.Context(), p, id)
}

func (s *Service) handleAddMember(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	groupID, userID, err := pathIDs(r, "groupId", "userId")
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, s.AddMember(r.Context(), p, groupID, userID)
}

func (s *Service) handleRemoveMember(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	groupID, userID, err := pathIDs(r, "groupId", "userId")
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, s.RemoveMember(r.Context(), p, groupID, userID)
}

func (s *Service) handleListGrants(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	id, err := pathID(r, "groupId")
	if err != nil {
		return 0, nil, err
	}
	items, err := s.ListGrants(r.Context(), p, id)
	return http.StatusOK, map[string]any{"items": items}, err
}

func (s *Service) handlePutGrant(w http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	groupID, databaseID, err := pathIDs(r, "groupId", "databaseId")
	if err != nil {
		return 0, nil, err
	}
	var in apigen.GrantUpsert
	if err := httpx.Decode(w, r, &in); err != nil {
		return 0, nil, err
	}
	g, err := s.PutGrant(r.Context(), p, groupID, databaseID, in.Level)
	return http.StatusOK, g, err
}

func (s *Service) handleDeleteGrant(_ http.ResponseWriter, r *http.Request, p authz.Principal) (int, any, error) {
	groupID, databaseID, err := pathIDs(r, "groupId", "databaseId")
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, s.DeleteGrant(r.Context(), p, groupID, databaseID)
}
