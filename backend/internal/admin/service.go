package admin

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)

type Service struct {
	pool  *pgxpool.Pool
	q     *gen.Queries
	az    authz.Authorizer
	audit *audit.Logger
}

func NewService(pool *pgxpool.Pool, az authz.Authorizer, l *audit.Logger) *Service {
	return &Service{pool: pool, q: gen.New(pool), az: az, audit: l}
}

func (s *Service) ListUsers(ctx context.Context, p authz.Principal, limit, offset int32) (apigen.UserPage, error) {
	if err := s.authorize(ctx, p, authz.ActionManageUsers); err != nil {
		return apigen.UserPage{}, err
	}
	rows, err := s.q.ListUsers(ctx, gen.ListUsersParams{Lim: limit, Off: offset})
	if err != nil {
		return apigen.UserPage{}, err
	}
	total, err := s.q.CountUsers(ctx)
	if err != nil {
		return apigen.UserPage{}, err
	}
	return apigen.UserPage{Items: toUsers(rows), Limit: int(limit), Offset: int(offset), Total: total}, nil
}

func (s *Service) CreateUser(ctx context.Context, p authz.Principal, in apigen.UserCreate) (apigen.User, error) {
	if err := s.authorize(ctx, p, authz.ActionManageUsers); err != nil {
		return apigen.User{}, err
	}
	in.Username = strings.TrimSpace(in.Username)
	fields := map[string]string{}
	if !usernamePattern.MatchString(in.Username) {
		fields["username"] = "3-64 karakter: huruf, angka, _ . -"
	}
	checkPassword(fields, in.Password)
	checkLen(fields, "displayName", deref(in.DisplayName), 128)
	if len(fields) > 0 {
		return apigen.User{}, apperr.Validation(fields)
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return apigen.User{}, err
	}
	u, err := s.q.CreateUser(ctx, gen.CreateUserParams{
		Username:        in.Username,
		DisplayName:     deref(in.DisplayName),
		PasswordHash:    hash,
		IsPlatformAdmin: in.IsPlatformAdmin != nil && *in.IsPlatformAdmin,
	})
	if err != nil {
		return apigen.User{}, mapErr(err, "username sudah dipakai")
	}
	s.record(ctx, p, "admin.user.create", "user", u.ID, map[string]any{"username": u.Username, "isPlatformAdmin": u.IsPlatformAdmin})
	return auth.ToAPIUser(u), nil
}

func (s *Service) GetUser(ctx context.Context, p authz.Principal, id int64) (apigen.User, error) {
	if err := s.authorize(ctx, p, authz.ActionManageUsers); err != nil {
		return apigen.User{}, err
	}
	u, err := s.q.GetUserByID(ctx, id)
	if err != nil {
		return apigen.User{}, mapErr(err, "")
	}
	return auth.ToAPIUser(u), nil
}

func (s *Service) UpdateUser(ctx context.Context, p authz.Principal, id int64, in apigen.UserUpdate) (apigen.User, error) {
	if err := s.authorize(ctx, p, authz.ActionManageUsers); err != nil {
		return apigen.User{}, err
	}
	fields := map[string]string{}
	checkLen(fields, "displayName", deref(in.DisplayName), 128)
	if len(fields) > 0 {
		return apigen.User{}, apperr.Validation(fields)
	}
	if id == p.UserID && (isFalse(in.IsActive) || isFalse(in.IsPlatformAdmin)) {
		return apigen.User{}, apperr.New(apperr.CodeConflict, "tidak dapat menonaktifkan atau mencabut status admin akun sendiri")
	}

	var updated gen.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		before, err := q.GetUserByID(ctx, id)
		if err != nil {
			return err
		}
		updated, err = q.UpdateUser(ctx, gen.UpdateUserParams{
			ID:              id,
			DisplayName:     pgtype.Text{String: deref(in.DisplayName), Valid: in.DisplayName != nil},
			IsPlatformAdmin: pgtype.Bool{Bool: in.IsPlatformAdmin != nil && *in.IsPlatformAdmin, Valid: in.IsPlatformAdmin != nil},
			IsActive:        pgtype.Bool{Bool: in.IsActive != nil && *in.IsActive, Valid: in.IsActive != nil},
		})
		if err != nil {
			return err
		}
		if before.IsActive && !updated.IsActive {
			return q.RevokeUserSessions(ctx, gen.RevokeUserSessionsParams{UserID: id, Now: now()})
		}
		if !before.IsPlatformAdmin && updated.IsPlatformAdmin {
			return q.RequireRotationForUser(ctx, id)
		}
		return nil
	})
	if err != nil {
		return apigen.User{}, mapErr(err, "")
	}
	s.record(ctx, p, "admin.user.update", "user", id, map[string]any{"isActive": updated.IsActive, "isPlatformAdmin": updated.IsPlatformAdmin})
	return auth.ToAPIUser(updated), nil
}

func (s *Service) DeleteUser(ctx context.Context, p authz.Principal, id int64) error {
	if err := s.authorize(ctx, p, authz.ActionManageUsers); err != nil {
		return err
	}
	if id == p.UserID {
		return apperr.New(apperr.CodeConflict, "tidak dapat menghapus akun sendiri")
	}
	n, err := s.q.DeleteUser(ctx, id)
	if err != nil {
		return mapErr(err, "")
	}
	if n == 0 {
		return apperr.ErrNotFound
	}
	s.record(ctx, p, "admin.user.delete", "user", id, nil)
	return nil
}

func (s *Service) ResetPassword(ctx context.Context, p authz.Principal, id int64, password string) error {
	if err := s.authorize(ctx, p, authz.ActionManageUsers); err != nil {
		return err
	}
	fields := map[string]string{}
	checkPassword(fields, password)
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		n, err := q.UpdateUserPassword(ctx, gen.UpdateUserPasswordParams{ID: id, PasswordHash: hash})
		if err != nil {
			return err
		}
		if n == 0 {
			return apperr.ErrNotFound
		}
		return q.RevokeUserSessions(ctx, gen.RevokeUserSessionsParams{UserID: id, Now: now()})
	})
	if err != nil {
		return err
	}
	s.record(ctx, p, "admin.user.reset_password", "user", id, nil)
	return nil
}

func (s *Service) RevokeSessions(ctx context.Context, p authz.Principal, id int64) error {
	if err := s.authorize(ctx, p, authz.ActionManageUsers); err != nil {
		return err
	}
	if _, err := s.q.GetUserByID(ctx, id); err != nil {
		return mapErr(err, "")
	}
	if err := s.q.RevokeUserSessions(ctx, gen.RevokeUserSessionsParams{UserID: id, Now: now()}); err != nil {
		return err
	}
	s.record(ctx, p, "admin.user.revoke_sessions", "user", id, nil)
	return nil
}

func (s *Service) ListGroups(ctx context.Context, p authz.Principal, limit, offset int32) (apigen.GroupPage, error) {
	if err := s.authorize(ctx, p, authz.ActionManageGroups); err != nil {
		return apigen.GroupPage{}, err
	}
	rows, err := s.q.ListGroups(ctx, gen.ListGroupsParams{Lim: limit, Off: offset})
	if err != nil {
		return apigen.GroupPage{}, err
	}
	total, err := s.q.CountGroups(ctx)
	if err != nil {
		return apigen.GroupPage{}, err
	}
	items := make([]apigen.Group, len(rows))
	for i, g := range rows {
		items[i] = toGroup(g)
	}
	return apigen.GroupPage{Items: items, Limit: int(limit), Offset: int(offset), Total: total}, nil
}

func (s *Service) CreateGroup(ctx context.Context, p authz.Principal, in apigen.GroupCreate) (apigen.Group, error) {
	if err := s.authorize(ctx, p, authz.ActionManageGroups); err != nil {
		return apigen.Group{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := validateGroup(&in.Name, in.Description); err != nil {
		return apigen.Group{}, err
	}
	g, err := s.q.CreateGroup(ctx, gen.CreateGroupParams{Name: in.Name, Description: deref(in.Description)})
	if err != nil {
		return apigen.Group{}, mapErr(err, "nama group sudah dipakai")
	}
	s.record(ctx, p, "admin.group.create", "group", g.ID, map[string]any{"name": g.Name})
	return toGroup(g), nil
}

func (s *Service) GetGroup(ctx context.Context, p authz.Principal, id int64) (apigen.GroupDetail, error) {
	if err := s.authorize(ctx, p, authz.ActionManageGroups); err != nil {
		return apigen.GroupDetail{}, err
	}
	g, err := s.q.GetGroupByID(ctx, id)
	if err != nil {
		return apigen.GroupDetail{}, mapErr(err, "")
	}
	members, err := s.q.ListGroupMembers(ctx, id)
	if err != nil {
		return apigen.GroupDetail{}, err
	}
	return apigen.GroupDetail{Id: g.ID, Name: g.Name, Description: g.Description, CreatedAt: g.CreatedAt.Time.UTC(), Members: toUsers(members)}, nil
}

func (s *Service) UpdateGroup(ctx context.Context, p authz.Principal, id int64, in apigen.GroupUpdate) (apigen.Group, error) {
	if err := s.authorize(ctx, p, authz.ActionManageGroups); err != nil {
		return apigen.Group{}, err
	}
	if in.Name != nil {
		*in.Name = strings.TrimSpace(*in.Name)
	}
	if err := validateGroup(in.Name, in.Description); err != nil {
		return apigen.Group{}, err
	}
	g, err := s.q.UpdateGroup(ctx, gen.UpdateGroupParams{
		ID:          id,
		Name:        pgtype.Text{String: deref(in.Name), Valid: in.Name != nil},
		Description: pgtype.Text{String: deref(in.Description), Valid: in.Description != nil},
	})
	if err != nil {
		return apigen.Group{}, mapErr(err, "nama group sudah dipakai")
	}
	s.record(ctx, p, "admin.group.update", "group", id, map[string]any{"name": g.Name})
	return toGroup(g), nil
}

func (s *Service) DeleteGroup(ctx context.Context, p authz.Principal, id int64) error {
	if err := s.authorize(ctx, p, authz.ActionManageGroups); err != nil {
		return err
	}
	n, err := s.q.DeleteGroup(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.ErrNotFound
	}
	s.record(ctx, p, "admin.group.delete", "group", id, nil)
	return nil
}

func (s *Service) AddMember(ctx context.Context, p authz.Principal, groupID, userID int64) error {
	if err := s.authorize(ctx, p, authz.ActionManageGroups); err != nil {
		return err
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.AddGroupMember(ctx, gen.AddGroupMemberParams{GroupID: groupID, UserID: userID}); err != nil {
			return err
		}
		return q.RequireRotationForUser(ctx, userID)
	})
	if err != nil {
		return mapFKNotFound(err)
	}
	s.record(ctx, p, "admin.group.add_member", "group", groupID, map[string]any{"userId": userID})
	return nil
}

func (s *Service) RemoveMember(ctx context.Context, p authz.Principal, groupID, userID int64) error {
	if err := s.authorize(ctx, p, authz.ActionManageGroups); err != nil {
		return err
	}
	n, err := s.q.RemoveGroupMember(ctx, gen.RemoveGroupMemberParams{GroupID: groupID, UserID: userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.ErrNotFound
	}
	s.record(ctx, p, "admin.group.remove_member", "group", groupID, map[string]any{"userId": userID})
	return nil
}

func (s *Service) ListGrants(ctx context.Context, p authz.Principal, groupID int64) ([]apigen.Grant, error) {
	if err := s.authorize(ctx, p, authz.ActionManageGrants); err != nil {
		return nil, err
	}
	if _, err := s.q.GetGroupByID(ctx, groupID); err != nil {
		return nil, mapErr(err, "")
	}
	rows, err := s.q.ListGroupGrants(ctx, groupID)
	if err != nil {
		return nil, err
	}
	out := make([]apigen.Grant, len(rows))
	for i, g := range rows {
		out[i] = apigen.Grant{DatabaseId: g.DatabaseID, DatabaseName: g.DatabaseName, Level: apigen.AccessLevel(g.Level)}
	}
	return out, nil
}

func (s *Service) PutGrant(ctx context.Context, p authz.Principal, groupID, databaseID int64, level apigen.AccessLevel) (apigen.Grant, error) {
	if err := s.authorize(ctx, p, authz.ActionManageGrants); err != nil {
		return apigen.Grant{}, err
	}
	newLevel := gen.AccessLevel(level)
	if newLevel != gen.AccessLevelView && newLevel != gen.AccessLevelEdit {
		return apigen.Grant{}, apperr.Validation(map[string]string{"level": "harus view atau edit"})
	}
	var row gen.UpsertGroupGrantRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		old, err := q.GetGroupGrantLevel(ctx, gen.GetGroupGrantLevelParams{GroupID: groupID, DatabaseID: databaseID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		row, err = q.UpsertGroupGrant(ctx, gen.UpsertGroupGrantParams{GroupID: groupID, DatabaseID: databaseID, Level: newLevel})
		if err != nil {
			return err
		}
		if old != newLevel && (old == "" || newLevel == gen.AccessLevelEdit) {
			return q.RequireRotationForGroup(ctx, groupID)
		}
		return nil
	})
	if err != nil {
		return apigen.Grant{}, mapFKNotFound(err)
	}
	s.record(ctx, p, "admin.grant.put", "group", groupID, map[string]any{"databaseId": databaseID, "level": string(newLevel)})
	return apigen.Grant{DatabaseId: row.DatabaseID, DatabaseName: row.DatabaseName, Level: apigen.AccessLevel(row.Level)}, nil
}

func (s *Service) DeleteGrant(ctx context.Context, p authz.Principal, groupID, databaseID int64) error {
	if err := s.authorize(ctx, p, authz.ActionManageGrants); err != nil {
		return err
	}
	n, err := s.q.DeleteGroupGrant(ctx, gen.DeleteGroupGrantParams{GroupID: groupID, DatabaseID: databaseID})
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.ErrNotFound
	}
	s.record(ctx, p, "admin.grant.delete", "group", groupID, map[string]any{"databaseId": databaseID})
	return nil
}

func (s *Service) authorize(ctx context.Context, p authz.Principal, a authz.Action) error {
	return s.az.Authorize(ctx, p, a, authz.Resource{})
}

func (s *Service) record(ctx context.Context, p authz.Principal, action, resourceType string, id int64, meta map[string]any) {
	s.audit.Record(ctx, audit.Entry{
		ActorID:      p.UserID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   strconv.FormatInt(id, 10),
		Outcome:      audit.OutcomeSuccess,
		Metadata:     meta,
	})
}

func validateGroup(name, description *string) error {
	fields := map[string]string{}
	if name != nil && (*name == "" || len(*name) > 64) {
		fields["name"] = "1-64 karakter"
	}
	checkLen(fields, "description", deref(description), 512)
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}
	return nil
}

func checkPassword(fields map[string]string, pw string) {
	if len(pw) < 8 || len(pw) > 256 {
		fields["password"] = "8-256 karakter"
	}
}

func checkLen(fields map[string]string, name, v string, maxLen int) {
	if len(v) > maxLen {
		fields[name] = "maksimal " + strconv.Itoa(maxLen) + " karakter"
	}
}

func mapErr(err error, conflict string) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return apperr.ErrNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return apperr.Wrap(apperr.CodeConflict, conflict, err)
	case errors.As(err, &pgErr) && pgErr.Code == "23503":
		return apperr.Wrap(apperr.CodeConflict, "masih direferensikan data lain; nonaktifkan saja", err)
	}
	return err
}

func mapFKNotFound(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return apperr.Wrap(apperr.CodeNotFound, "group, user, atau database tidak ditemukan", err)
	}
	return err
}

func toUsers(rows []gen.User) []apigen.User {
	out := make([]apigen.User, len(rows))
	for i, u := range rows {
		out[i] = auth.ToAPIUser(u)
	}
	return out
}

func toGroup(g gen.Group) apigen.Group {
	return apigen.Group{Id: g.ID, Name: g.Name, Description: g.Description, CreatedAt: g.CreatedAt.Time.UTC()}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func isFalse(b *bool) bool { return b != nil && !*b }

func now() pgtype.Timestamptz { return pgtype.Timestamptz{Time: time.Now(), Valid: true} }
