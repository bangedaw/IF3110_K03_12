package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

var errInvalidCredentials = apperr.New(apperr.CodeUnauthenticated, "username atau password salah")

type Service struct {
	pool    *pgxpool.Pool
	q       *gen.Queries
	audit   *audit.Logger
	cfg     config.Session
	csrfKey []byte
	secure  bool
	now     func() time.Time
}

func NewService(pool *pgxpool.Pool, cfg config.Session, csrfKey string, secureCookie bool) *Service {
	return &Service{pool: pool, q: gen.New(pool), audit: audit.New(pool), cfg: cfg, csrfKey: []byte(csrfKey), secure: secureCookie, now: time.Now}
}

type Session struct {
	Principal authz.Principal
	FamilyID  uuid.UUID
	NewToken  string
	ExpiresAt time.Time
}

type LoginInput struct {
	Username  string
	Password  string
	UserAgent string
	IP        *netip.Addr
}

func (s *Service) Login(ctx context.Context, in LoginInput) (gen.User, Session, error) {
	user, err := s.q.GetUserByUsername(ctx, in.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = VerifyPassword(dummyHash(), in.Password)
		return gen.User{}, Session{}, errInvalidCredentials
	}
	if err != nil {
		return gen.User{}, Session{}, err
	}
	ok, err := VerifyPassword(user.PasswordHash, in.Password)
	if err != nil {
		return gen.User{}, Session{}, err
	}
	if !ok || !user.IsActive {
		return gen.User{}, Session{}, errInvalidCredentials
	}

	now := s.now()
	familyID := uuid.New()
	expires := now.Add(s.cfg.AbsoluteTimeout)
	token, hash, err := newToken()
	if err != nil {
		return gen.User{}, Session{}, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.CreateSessionFamily(ctx, gen.CreateSessionFamilyParams{
			ID:                pgUUID(familyID),
			UserID:            user.ID,
			Now:               ts(now),
			AbsoluteExpiresAt: ts(expires),
			UserAgent:         pgtype.Text{String: in.UserAgent, Valid: in.UserAgent != ""},
			Ip:                in.IP,
		}); err != nil {
			return err
		}
		return q.CreateSessionToken(ctx, gen.CreateSessionTokenParams{TokenHash: hash, FamilyID: pgUUID(familyID), Now: ts(now)})
	})
	if err != nil {
		return gen.User{}, Session{}, err
	}
	return user, Session{
		Principal: authz.Principal{UserID: user.ID, IsPlatformAdmin: user.IsPlatformAdmin},
		FamilyID:  familyID,
		NewToken:  token,
		ExpiresAt: expires,
	}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	hash := hashToken(token)
	row, err := s.q.GetSessionByTokenHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, apperr.ErrUnauthenticated
	}
	if err != nil {
		return Session{}, err
	}

	now := s.now()
	familyID := uuid.UUID(row.FamilyID.Bytes)
	switch {
	case row.RevokedAt.Valid:
		return Session{}, apperr.ErrUnauthenticated
	case !now.Before(row.AbsoluteExpiresAt.Time):
		return Session{}, apperr.New(apperr.CodeSessionExpired, "session berakhir, silakan login kembali")
	case now.Sub(row.LastSeenAt.Time) > s.cfg.IdleTimeout:
		return Session{}, apperr.New(apperr.CodeSessionExpired, "session berakhir karena tidak aktif")
	case !row.IsActive:
		if err := s.q.RevokeSessionFamily(ctx, gen.RevokeSessionFamilyParams{ID: row.FamilyID, Now: ts(now)}); err != nil {
			return Session{}, err
		}
		return Session{}, apperr.ErrUnauthenticated
	case row.SupersededAt.Valid && now.Sub(row.SupersededAt.Time) > s.cfg.GracePeriod:
		return Session{}, apperr.ErrUnauthenticated
	}

	sess := Session{
		Principal: authz.Principal{UserID: row.UserID, IsPlatformAdmin: row.IsPlatformAdmin},
		FamilyID:  familyID,
		ExpiresAt: row.AbsoluteExpiresAt.Time,
	}
	if row.SupersededAt.Valid {
		return sess, nil
	}

	if row.RotateRequired || now.Sub(row.LastRotatedAt.Time) >= s.cfg.RotationInterval {
		token, err := s.rotate(ctx, hash, row.FamilyID, now)
		if err != nil {
			return Session{}, err
		}
		sess.NewToken = token
		return sess, nil
	}

	if now.Sub(row.LastSeenAt.Time) >= s.cfg.TouchInterval {
		if err := s.q.TouchSessionFamily(ctx, gen.TouchSessionFamilyParams{ID: row.FamilyID, Now: ts(now)}); err != nil {
			return Session{}, err
		}
	}
	return sess, nil
}

func (s *Service) rotate(ctx context.Context, oldHash []byte, familyID pgtype.UUID, now time.Time) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	rotated := false
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		n, err := q.SupersedeSessionToken(ctx, gen.SupersedeSessionTokenParams{TokenHash: oldHash, Now: ts(now)})
		if err != nil || n == 0 {
			return err
		}
		if err := q.CreateSessionToken(ctx, gen.CreateSessionTokenParams{TokenHash: hash, FamilyID: familyID, Now: ts(now)}); err != nil {
			return err
		}
		rotated = true
		return q.MarkSessionRotated(ctx, gen.MarkSessionRotatedParams{ID: familyID, Now: ts(now)})
	})
	if err != nil || !rotated {
		return "", err
	}
	return token, nil
}

func (s *Service) Logout(ctx context.Context, familyID uuid.UUID) error {
	return s.q.RevokeSessionFamily(ctx, gen.RevokeSessionFamilyParams{ID: pgUUID(familyID), Now: ts(s.now())})
}

func (s *Service) RevokeUserSessions(ctx context.Context, userID int64) error {
	return s.q.RevokeUserSessions(ctx, gen.RevokeUserSessionsParams{UserID: userID, Now: ts(s.now())})
}

func newToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
