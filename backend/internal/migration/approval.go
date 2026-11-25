package migration

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

func approved(groups []gen.ListRequestApprovalsRow) bool {
	for _, g := range groups {
		if !g.Approved.Valid || !g.Approved.Bool {
			return false
		}
	}
	return len(groups) > 0
}

func undecided(groups []gen.ListRequestApprovalsRow, userID int64) []int64 {
	var ids []int64
	for _, g := range groups {
		if g.LeaderUserID.Valid && g.LeaderUserID.Int64 == userID && !g.Approved.Valid {
			ids = append(ids, g.GroupID)
		}
	}
	return ids
}

func leads(groups []gen.ListRequestApprovalsRow, userID int64) bool {
	for _, g := range groups {
		if g.LeaderUserID.Valid && g.LeaderUserID.Int64 == userID {
			return true
		}
	}
	return false
}

func decidable(r request) bool {
	switch r.effective() {
	case gen.RequestStatusPendingReview, gen.RequestStatusApproved, gen.RequestStatusFailed, gen.RequestStatusPartial:
		return true
	}
	return false
}

func (s *Service) Approvals(ctx context.Context, p authz.Principal, id int64) (apigen.MigrationApprovals, error) {
	q := gen.New(s.Pool)
	row, err := s.load(ctx, q, p, authz.ActionMigrationRead, id)
	if err != nil {
		return apigen.MigrationApprovals{}, err
	}
	st, err := loadState(ctx, q, row.Migration.DatabaseID)
	if err != nil {
		return apigen.MigrationApprovals{}, err
	}
	latest := st.latest(id)
	if latest == nil {
		return apigen.MigrationApprovals{}, apperr.ErrNotFound
	}
	groups, err := q.ListRequestApprovals(ctx, gen.ListRequestApprovalsParams{DatabaseID: row.Migration.DatabaseID, RequestID: latest.ID})
	if err != nil {
		return apigen.MigrationApprovals{}, err
	}
	out := apigen.MigrationApprovals{
		Request:   latest.dto(),
		Groups:    make([]apigen.MigrationApproval, len(groups)),
		CanDecide: decidable(*latest) && len(undecided(groups, p.UserID)) > 0,
	}
	for i, g := range groups {
		decision := apigen.MigrationApprovalDecisionPending
		if g.Approved.Valid {
			decision = apigen.MigrationApprovalDecisionRejected
			if g.Approved.Bool {
				decision = apigen.MigrationApprovalDecisionApproved
			}
		}
		out.Groups[i] = apigen.MigrationApproval{
			GroupId: g.GroupID, GroupName: g.GroupName, Decision: decision, Comment: g.Comment,
			LeaderName: textPtr(text(g.LeaderName)), DecidedByName: textPtr(text(g.DecidedByName)), DecidedAt: timePtr(g.DecidedAt),
		}
	}
	return out, nil
}

func (s *Service) Decide(ctx context.Context, p authz.Principal, id, requestID int64, approve bool, comment string) (apigen.Migration, error) {
	err := s.inMigration(ctx, p, authz.ActionMigrationRead, id, func(q *gen.Queries, _ pgx.Tx, row gen.GetMigrationRow, st state) error {
		latest := st.latest(id)
		if latest == nil || latest.ID != requestID {
			return conflict("request ini bukan lagi request terakhir; muat ulang halaman")
		}
		params := gen.ListRequestApprovalsParams{DatabaseID: row.Migration.DatabaseID, RequestID: requestID}
		groups, err := q.ListRequestApprovals(ctx, params)
		if err != nil {
			return err
		}
		if !leads(groups, p.UserID) {
			return apperr.Wrap(apperr.CodeForbidden, "hanya leader group yang punya akses ke database ini yang boleh memutuskan", nil)
		}
		mine := undecided(groups, p.UserID)
		if len(mine) == 0 || !decidable(*latest) {
			return conflict("keputusan untuk request ini sudah tercatat atau request sudah selesai")
		}
		for _, groupID := range mine {
			if err := q.InsertApproval(ctx, gen.InsertApprovalParams{
				RequestID: requestID, GroupID: groupID, Approved: approve, DecidedBy: p.UserID, Comment: comment,
			}); err != nil {
				return err
			}
		}
		status := latest.Status
		switch {
		case !approve && status != gen.RequestStatusPartial:
			status = gen.RequestStatusRejected
		case approve && status == gen.RequestStatusPendingReview:
			if groups, err = q.ListRequestApprovals(ctx, params); err != nil {
				return err
			}
			if approved(groups) {
				status = gen.RequestStatusApproved
			}
		}
		if status == latest.Status {
			return nil
		}
		return q.SetMigrationRequestStatus(ctx, gen.SetMigrationRequestStatusParams{ID: requestID, Status: status})
	})
	if err != nil {
		return apigen.Migration{}, err
	}
	s.record(ctx, p, "migration.decide", id, map[string]any{"requestId": requestID, "approved": approve})
	return s.Get(ctx, p, id)
}
