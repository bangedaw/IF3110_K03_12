package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
)

const adminListLimit = 200

type pagerView struct {
	From     int
	To       int
	Estimate string
	PrevURL  string
	NextURL  string
	Sizes    []sizeLink
}

type usersListView struct {
	Items []apigen.User
	Pager pagerView
}

type userFormView struct {
	ID             int64
	Editing        bool
	Self           bool
	Username       string
	DisplayName    string
	IsAdmin        bool
	IsActive       bool
	CreatedAt      time.Time
	Errors         map[string]string
	PasswordErrors map[string]string
}

type userDeleteView struct {
	User apigen.User
}

type groupsListView struct {
	Items []apigen.Group
	Pager pagerView
}

type grantRow struct {
	DatabaseID   int64
	DatabaseName string
	Level        string
	Label        string
}

type groupView struct {
	ID           int64
	Editing      bool
	Name         string
	Description  string
	CreatedAt    time.Time
	Errors       map[string]string
	Members      []apigen.User
	Candidates   []apigen.User
	Grants       []grantRow
	AvailableDBs []dbconn.Connection
	Levels       []grantRow
}

type groupDeleteView struct {
	Group apigen.GroupDetail
}

func (s *Server) mountAdmin(r chi.Router) {
	r.Get("/admin/users", s.userList)
	r.Get("/admin/users/new", s.userNewForm)
	r.Post("/admin/users/new", s.userCreate)
	r.Get("/admin/users/{userId}", s.userDetail)
	r.Post("/admin/users/{userId}", s.userUpdate)
	r.Post("/admin/users/{userId}/password", s.userResetPassword)
	r.Post("/admin/users/{userId}/revoke-sessions", s.userRevokeSessions)
	r.Get("/admin/users/{userId}/delete", s.userDeleteConfirm)
	r.Post("/admin/users/{userId}/delete", s.userDelete)

	r.Get("/admin/groups", s.groupList)
	r.Get("/admin/groups/new", s.groupNewForm)
	r.Post("/admin/groups/new", s.groupCreate)
	r.Get("/admin/groups/{groupId}", s.groupDetail)
	r.Post("/admin/groups/{groupId}", s.groupUpdate)
	r.Post("/admin/groups/{groupId}/members", s.groupAddMember)
	r.Post("/admin/groups/{groupId}/members/{userId}/remove", s.groupRemoveMember)
	r.Post("/admin/groups/{groupId}/grants", s.groupPutGrant)
	r.Post("/admin/groups/{groupId}/grants/{databaseId}/remove", s.groupRemoveGrant)
	r.Get("/admin/groups/{groupId}/delete", s.groupDeleteConfirm)
	r.Post("/admin/groups/{groupId}/delete", s.groupDelete)
}

func buildPager(base string, limit, offset int32, shown int, total int64) pagerView {
	v := pagerView{To: int(offset) + shown, Estimate: fmt.Sprintf("%d total", total)}
	if shown > 0 {
		v.From = int(offset) + 1
	}
	if offset > 0 {
		prev := max(offset-limit, 0)
		v.PrevURL = fmt.Sprintf("%s?limit=%d&offset=%d", base, limit, prev)
	}
	if int64(int(offset)+shown) < total {
		v.NextURL = fmt.Sprintf("%s?limit=%d&offset=%d", base, limit, int(offset)+shown)
	}
	return v
}

func formErrors(err error, conflictField string) (map[string]string, int, bool) {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return nil, 0, false
	}
	switch ae.Code {
	case apperr.CodeValidation:
		if len(ae.Fields) == 0 {
			return map[string]string{"form": messageOr(ae, "input tidak valid")}, http.StatusUnprocessableEntity, true
		}
		return ae.Fields, http.StatusUnprocessableEntity, true
	case apperr.CodeConflict:
		return map[string]string{conflictField: messageOr(ae, "data bertabrakan dengan data lain")}, http.StatusConflict, true
	}
	return nil, 0, false
}

func (s *Server) actionResult(w http.ResponseWriter, r *http.Request, back string, success string, err error) {
	if err == nil {
		s.Redirect(w, r, back, &Flash{Kind: "success", Message: success})
		return
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case apperr.CodeValidation, apperr.CodeConflict, apperr.CodeNotFound:
			s.Redirect(w, r, back, &Flash{Kind: "error", Message: messageOr(ae, "aksi tidak dapat diproses")})
			return
		}
	}
	s.Fail(w, r, err)
}

func (s *Server) usersAdmin(r *http.Request) (authz.Principal, error) {
	return s.authorizeAction(r, authz.ActionManageUsers)
}

func (s *Server) groupsAdmin(r *http.Request) (authz.Principal, error) {
	return s.authorizeAction(r, authz.ActionManageGroups)
}

func (s *Server) userList(w http.ResponseWriter, r *http.Request) {
	p, err := s.usersAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	limit, offset := httpx.Pagination(r)
	page, err := s.admin.ListUsers(r.Context(), p, limit, offset)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "admin/users", usersListView{
		Items: page.Items,
		Pager: buildPager("/admin/users", limit, offset, len(page.Items), page.Total),
	})
}

func (s *Server) userNewForm(w http.ResponseWriter, r *http.Request) {
	if _, err := s.usersAdmin(r); err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "admin/user", userFormView{IsActive: true})
}

func (s *Server) userCreate(w http.ResponseWriter, r *http.Request) {
	p, err := s.usersAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	form := userFormView{
		Username:    strings.TrimSpace(r.PostFormValue("username")),
		DisplayName: strings.TrimSpace(r.PostFormValue("displayName")),
		IsAdmin:     r.PostFormValue("isAdmin") != "",
		IsActive:    true,
	}
	isAdmin := form.IsAdmin
	user, err := s.admin.CreateUser(r.Context(), p, apigen.UserCreate{
		Username:        form.Username,
		DisplayName:     &form.DisplayName,
		IsPlatformAdmin: &isAdmin,
		Password:        r.PostFormValue("password"),
	})
	if err != nil {
		if fields, status, ok := formErrors(err, "username"); ok {
			form.Errors = fields
			s.Render(w, r, status, "admin/user", form)
			return
		}
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, fmt.Sprintf("/admin/users/%d", user.Id), &Flash{Kind: "success", Message: fmt.Sprintf("Pengguna %q dibuat.", user.Username)})
}

func userForm(p authz.Principal, u apigen.User) userFormView {
	return userFormView{
		ID:          u.Id,
		Editing:     true,
		Self:        u.Id == p.UserID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		IsAdmin:     u.IsPlatformAdmin,
		IsActive:    u.IsActive,
		CreatedAt:   u.CreatedAt,
	}
}

func (s *Server) userDetail(w http.ResponseWriter, r *http.Request) {
	p, err := s.usersAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "userId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	user, err := s.admin.GetUser(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "admin/user", userForm(p, user))
}

func (s *Server) userUpdate(w http.ResponseWriter, r *http.Request) {
	p, err := s.usersAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "userId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	current, err := s.admin.GetUser(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	displayName := strings.TrimSpace(r.PostFormValue("displayName"))
	active := r.PostFormValue("isActive") != ""
	isAdmin := r.PostFormValue("isAdmin") != ""
	updated, err := s.admin.UpdateUser(r.Context(), p, id, apigen.UserUpdate{
		DisplayName:     &displayName,
		IsActive:        &active,
		IsPlatformAdmin: &isAdmin,
	})
	if err != nil {
		form := userForm(p, current)
		form.DisplayName, form.IsActive, form.IsAdmin = displayName, active, isAdmin
		if fields, status, ok := formErrors(err, "form"); ok {
			form.Errors = fields
			s.Render(w, r, status, "admin/user", form)
			return
		}
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, fmt.Sprintf("/admin/users/%d", id), &Flash{Kind: "success", Message: fmt.Sprintf("Pengguna %q diperbarui.", updated.Username)})
}

func (s *Server) userResetPassword(w http.ResponseWriter, r *http.Request) {
	p, err := s.usersAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "userId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	user, err := s.admin.GetUser(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	password := r.PostFormValue("password")
	form := userForm(p, user)
	if password != r.PostFormValue("passwordConfirm") {
		form.PasswordErrors = map[string]string{"passwordConfirm": "konfirmasi password tidak sama"}
		s.Render(w, r, http.StatusUnprocessableEntity, "admin/user", form)
		return
	}
	if err := s.admin.ResetPassword(r.Context(), p, id, password); err != nil {
		if fields, status, ok := formErrors(err, "password"); ok {
			form.PasswordErrors = fields
			s.Render(w, r, status, "admin/user", form)
			return
		}
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, fmt.Sprintf("/admin/users/%d", id), &Flash{Kind: "success", Message: "Password direset dan semua session pengguna dicabut."})
}

func (s *Server) userRevokeSessions(w http.ResponseWriter, r *http.Request) {
	p, err := s.usersAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "userId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	err = s.admin.RevokeSessions(r.Context(), p, id)
	s.actionResult(w, r, fmt.Sprintf("/admin/users/%d", id), "Semua session pengguna dicabut.", err)
}

func (s *Server) userDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	p, err := s.usersAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "userId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	user, err := s.admin.GetUser(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "admin/user_delete", userDeleteView{User: user})
}

func (s *Server) userDelete(w http.ResponseWriter, r *http.Request) {
	p, err := s.usersAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "userId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	err = s.admin.DeleteUser(r.Context(), p, id)
	if err != nil {
		s.actionResult(w, r, fmt.Sprintf("/admin/users/%d", id), "", err)
		return
	}
	s.Redirect(w, r, "/admin/users", &Flash{Kind: "success", Message: "Pengguna dihapus."})
}

func (s *Server) groupList(w http.ResponseWriter, r *http.Request) {
	p, err := s.groupsAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	limit, offset := httpx.Pagination(r)
	page, err := s.admin.ListGroups(r.Context(), p, limit, offset)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "admin/groups", groupsListView{
		Items: page.Items,
		Pager: buildPager("/admin/groups", limit, offset, len(page.Items), page.Total),
	})
}

func (s *Server) groupNewForm(w http.ResponseWriter, r *http.Request) {
	if _, err := s.groupsAdmin(r); err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "admin/group", groupView{})
}

func (s *Server) groupCreate(w http.ResponseWriter, r *http.Request) {
	p, err := s.groupsAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	form := groupView{
		Name:        strings.TrimSpace(r.PostFormValue("name")),
		Description: strings.TrimSpace(r.PostFormValue("description")),
	}
	group, err := s.admin.CreateGroup(r.Context(), p, apigen.GroupCreate{Name: form.Name, Description: &form.Description})
	if err != nil {
		if fields, status, ok := formErrors(err, "name"); ok {
			form.Errors = fields
			s.Render(w, r, status, "admin/group", form)
			return
		}
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, fmt.Sprintf("/admin/groups/%d", group.Id), &Flash{Kind: "success", Message: fmt.Sprintf("Group %q dibuat.", group.Name)})
}

var grantLevels = []grantRow{
	{Level: string(apigen.AccessLevelView), Label: "View Only"},
	{Level: string(apigen.AccessLevelEdit), Label: "View + Edit"},
}

func levelText(level string) string {
	if level == string(apigen.AccessLevelEdit) {
		return "View + Edit"
	}
	return "View Only"
}

func (s *Server) buildGroupView(r *http.Request, p authz.Principal, id int64) (groupView, error) {
	group, err := s.admin.GetGroup(r.Context(), p, id)
	if err != nil {
		return groupView{}, err
	}
	grants, err := s.admin.ListGrants(r.Context(), p, id)
	if err != nil {
		return groupView{}, err
	}
	users, err := s.admin.ListUsers(r.Context(), p, adminListLimit, 0)
	if err != nil {
		return groupView{}, err
	}
	member := map[int64]bool{}
	for _, m := range group.Members {
		member[m.Id] = true
	}
	view := groupView{
		ID:          group.Id,
		Editing:     true,
		Name:        group.Name,
		Description: group.Description,
		CreatedAt:   group.CreatedAt,
		Members:     group.Members,
		Levels:      grantLevels,
	}
	for _, u := range users.Items {
		if !member[u.Id] && u.IsActive {
			view.Candidates = append(view.Candidates, u)
		}
	}
	granted := map[int64]bool{}
	for _, g := range grants {
		granted[g.DatabaseId] = true
		view.Grants = append(view.Grants, grantRow{DatabaseID: g.DatabaseId, DatabaseName: g.DatabaseName, Level: string(g.Level), Label: levelText(string(g.Level))})
	}
	if s.conns != nil {
		conns, err := s.conns.List(r.Context(), p, connectionListLimit, 0)
		if err != nil {
			return groupView{}, err
		}
		for _, c := range conns.Items {
			if !granted[c.ID] {
				view.AvailableDBs = append(view.AvailableDBs, c)
			}
		}
	}
	return view, nil
}

func (s *Server) groupDetail(w http.ResponseWriter, r *http.Request) {
	p, err := s.groupsAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "groupId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	view, err := s.buildGroupView(r, p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "admin/group", view)
}

func (s *Server) groupUpdate(w http.ResponseWriter, r *http.Request) {
	p, err := s.groupsAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "groupId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	description := strings.TrimSpace(r.PostFormValue("description"))
	group, err := s.admin.UpdateGroup(r.Context(), p, id, apigen.GroupUpdate{Name: &name, Description: &description})
	if err != nil {
		fields, status, ok := formErrors(err, "name")
		if !ok {
			s.Fail(w, r, err)
			return
		}
		view, viewErr := s.buildGroupView(r, p, id)
		if viewErr != nil {
			s.Fail(w, r, viewErr)
			return
		}
		view.Name, view.Description, view.Errors = name, description, fields
		s.Render(w, r, status, "admin/group", view)
		return
	}
	s.Redirect(w, r, fmt.Sprintf("/admin/groups/%d", id), &Flash{Kind: "success", Message: fmt.Sprintf("Group %q diperbarui.", group.Name)})
}

func (s *Server) groupAddMember(w http.ResponseWriter, r *http.Request) {
	p, err := s.groupsAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "groupId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	back := fmt.Sprintf("/admin/groups/%d", id)
	userID, parseErr := strconv.ParseInt(r.PostFormValue("userId"), 10, 64)
	if parseErr != nil || userID <= 0 {
		s.Redirect(w, r, back, &Flash{Kind: "error", Message: "Pilih pengguna terlebih dahulu."})
		return
	}
	s.actionResult(w, r, back, "Member ditambahkan.", s.admin.AddMember(r.Context(), p, id, userID))
}

func (s *Server) groupRemoveMember(w http.ResponseWriter, r *http.Request) {
	p, err := s.groupsAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "groupId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	userID, err := idParam(r, "userId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.actionResult(w, r, fmt.Sprintf("/admin/groups/%d", id), "Member dikeluarkan.", s.admin.RemoveMember(r.Context(), p, id, userID))
}

func (s *Server) groupPutGrant(w http.ResponseWriter, r *http.Request) {
	p, err := s.groupsAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "groupId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	back := fmt.Sprintf("/admin/groups/%d", id)
	databaseID, parseErr := strconv.ParseInt(r.PostFormValue("databaseId"), 10, 64)
	if parseErr != nil || databaseID <= 0 {
		s.Redirect(w, r, back, &Flash{Kind: "error", Message: "Pilih database terlebih dahulu."})
		return
	}
	_, err = s.admin.PutGrant(r.Context(), p, id, databaseID, apigen.AccessLevel(r.PostFormValue("level")))
	s.actionResult(w, r, back, "Grant disimpan.", err)
}

func (s *Server) groupRemoveGrant(w http.ResponseWriter, r *http.Request) {
	p, err := s.groupsAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "groupId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	databaseID, err := idParam(r, "databaseId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.actionResult(w, r, fmt.Sprintf("/admin/groups/%d", id), "Grant dicabut.", s.admin.DeleteGrant(r.Context(), p, id, databaseID))
}

func (s *Server) groupDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	p, err := s.groupsAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "groupId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	group, err := s.admin.GetGroup(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "admin/group_delete", groupDeleteView{Group: group})
}

func (s *Server) groupDelete(w http.ResponseWriter, r *http.Request) {
	p, err := s.groupsAdmin(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "groupId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	if err := s.admin.DeleteGroup(r.Context(), p, id); err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Redirect(w, r, "/admin/groups", &Flash{Kind: "success", Message: "Group dihapus."})
}
