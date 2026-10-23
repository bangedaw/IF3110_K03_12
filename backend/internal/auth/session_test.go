package auth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

var testSessionCfg = config.Session{
	IdleTimeout:      30 * time.Minute,
	AbsoluteTimeout:  8 * time.Hour,
	RotationInterval: 15 * time.Minute,
	GracePeriod:      30 * time.Second,
	TouchInterval:    60 * time.Second,
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestService(t *testing.T) (*Service, *pgxpool.Pool, *clock) {
	t.Helper()
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	clk := &clock{t: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)}
	svc := NewService(pool, testSessionCfg, false)
	svc.now = clk.now
	return svc, pool, clk
}

func createUser(t *testing.T, pool *pgxpool.Pool, username, password string, active bool) int64 {
	t.Helper()
	h, err := HashPassword(password)
	require.NoError(t, err)
	var id int64
	err = pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash, is_active) VALUES ($1, $2, $3) RETURNING id`,
		username, h, active).Scan(&id)
	require.NoError(t, err)
	return id
}

func login(t *testing.T, svc *Service, username, password string) Session {
	t.Helper()
	_, sess, err := svc.Login(context.Background(), LoginInput{Username: username, Password: password})
	require.NoError(t, err)
	require.NotEmpty(t, sess.NewToken)
	return sess
}

func TestLogin(t *testing.T) {
	svc, pool, _ := newTestService(t)
	ctx := context.Background()
	id := createUser(t, pool, "alice", "rahasia123", true)
	createUser(t, pool, "nonaktif", "rahasia123", false)

	user, sess, err := svc.Login(ctx, LoginInput{Username: "ALICE", Password: "rahasia123"})
	require.NoError(t, err)
	assert.Equal(t, id, user.ID)
	assert.Equal(t, id, sess.Principal.UserID)

	for _, in := range []LoginInput{
		{Username: "alice", Password: "salah"},
		{Username: "tidakada", Password: "rahasia123"},
		{Username: "nonaktif", Password: "rahasia123"},
	} {
		_, _, err := svc.Login(ctx, in)
		var ae *apperr.Error
		require.ErrorAs(t, err, &ae, in.Username)
		assert.Equal(t, errInvalidCredentials, ae, in.Username)
	}
}

func TestLoginAlwaysCreatesNewFamily(t *testing.T) {
	svc, pool, _ := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)

	a := login(t, svc, "alice", "rahasia123")
	b := login(t, svc, "alice", "rahasia123")
	assert.NotEqual(t, a.FamilyID, b.FamilyID)
	assert.NotEqual(t, a.NewToken, b.NewToken)
}

func TestAuthenticate(t *testing.T) {
	ctx := context.Background()

	t.Run("token valid", func(t *testing.T) {
		svc, pool, _ := newTestService(t)
		id := createUser(t, pool, "alice", "rahasia123", true)
		s := login(t, svc, "alice", "rahasia123")

		got, err := svc.Authenticate(ctx, s.NewToken)
		require.NoError(t, err)
		assert.Equal(t, id, got.Principal.UserID)
		assert.Empty(t, got.NewToken)
	})

	t.Run("token tidak dikenal", func(t *testing.T) {
		svc, _, _ := newTestService(t)
		_, err := svc.Authenticate(ctx, "bukan-token")
		assert.ErrorIs(t, err, apperr.ErrUnauthenticated)
	})

	t.Run("idle timeout", func(t *testing.T) {
		svc, pool, clk := newTestService(t)
		createUser(t, pool, "alice", "rahasia123", true)
		s := login(t, svc, "alice", "rahasia123")

		clk.advance(testSessionCfg.IdleTimeout + time.Second)
		_, err := svc.Authenticate(ctx, s.NewToken)
		assert.ErrorIs(t, err, apperr.New(apperr.CodeSessionExpired, ""))
	})

	t.Run("aktivitas memperpanjang idle", func(t *testing.T) {
		svc, pool, clk := newTestService(t)
		createUser(t, pool, "alice", "rahasia123", true)
		token := login(t, svc, "alice", "rahasia123").NewToken

		for range 3 {
			clk.advance(testSessionCfg.IdleTimeout - time.Minute)
			s, err := svc.Authenticate(ctx, token)
			require.NoError(t, err)
			if s.NewToken != "" {
				token = s.NewToken
			}
		}
	})

	t.Run("absolute expiration tidak diperpanjang rotasi", func(t *testing.T) {
		svc, pool, clk := newTestService(t)
		createUser(t, pool, "alice", "rahasia123", true)
		first := login(t, svc, "alice", "rahasia123")
		token := first.NewToken

		var err error
		for elapsed := time.Duration(0); elapsed < testSessionCfg.AbsoluteTimeout; elapsed += 20 * time.Minute {
			clk.advance(20 * time.Minute)
			var s Session
			s, err = svc.Authenticate(ctx, token)
			if err != nil {
				break
			}
			assert.Equal(t, first.ExpiresAt.Unix(), s.ExpiresAt.Unix())
			if s.NewToken != "" {
				token = s.NewToken
			}
		}
		assert.ErrorIs(t, err, apperr.New(apperr.CodeSessionExpired, ""))
		assert.False(t, clk.now().Before(first.ExpiresAt))
	})

	t.Run("rotasi periodik dan grace period", func(t *testing.T) {
		svc, pool, clk := newTestService(t)
		createUser(t, pool, "alice", "rahasia123", true)
		old := login(t, svc, "alice", "rahasia123").NewToken

		clk.advance(testSessionCfg.RotationInterval)
		rotated, err := svc.Authenticate(ctx, old)
		require.NoError(t, err)
		require.NotEmpty(t, rotated.NewToken)
		assert.NotEqual(t, old, rotated.NewToken)

		clk.advance(testSessionCfg.GracePeriod - time.Second)
		again, err := svc.Authenticate(ctx, old)
		require.NoError(t, err)
		assert.Empty(t, again.NewToken)

		clk.advance(2 * time.Second)
		_, err = svc.Authenticate(ctx, old)
		assert.ErrorIs(t, err, apperr.ErrUnauthenticated)

		fresh, err := svc.Authenticate(ctx, rotated.NewToken)
		require.NoError(t, err)
		assert.Empty(t, fresh.NewToken)
	})

	t.Run("rotasi karena privilege naik", func(t *testing.T) {
		svc, pool, _ := newTestService(t)
		id := createUser(t, pool, "alice", "rahasia123", true)
		s := login(t, svc, "alice", "rahasia123")

		_, err := pool.Exec(ctx, `UPDATE users SET is_platform_admin = TRUE WHERE id = $1`, id)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE session_families SET rotate_required = TRUE WHERE user_id = $1`, id)
		require.NoError(t, err)

		got, err := svc.Authenticate(ctx, s.NewToken)
		require.NoError(t, err)
		assert.NotEmpty(t, got.NewToken)
		assert.True(t, got.Principal.IsPlatformAdmin)

		next, err := svc.Authenticate(ctx, got.NewToken)
		require.NoError(t, err)
		assert.Empty(t, next.NewToken)
	})

	t.Run("rotasi paralel hanya menghasilkan satu token baru", func(t *testing.T) {
		svc, pool, clk := newTestService(t)
		createUser(t, pool, "alice", "rahasia123", true)
		old := login(t, svc, "alice", "rahasia123").NewToken
		clk.advance(testSessionCfg.RotationInterval)

		var wg sync.WaitGroup
		results := make([]Session, 5)
		errs := make([]error, 5)
		for i := range results {
			wg.Go(func() { results[i], errs[i] = svc.Authenticate(ctx, old) })
		}
		wg.Wait()

		issued := 0
		for i := range results {
			require.NoError(t, errs[i])
			if results[i].NewToken != "" {
				issued++
			}
		}
		assert.Equal(t, 1, issued)
	})

	t.Run("user dinonaktifkan", func(t *testing.T) {
		svc, pool, _ := newTestService(t)
		id := createUser(t, pool, "alice", "rahasia123", true)
		s := login(t, svc, "alice", "rahasia123")

		_, err := pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, id)
		require.NoError(t, err)
		_, err = svc.Authenticate(ctx, s.NewToken)
		assert.ErrorIs(t, err, apperr.ErrUnauthenticated)
	})
}

func TestLogoutRevokesOnlyThatFamily(t *testing.T) {
	svc, pool, _ := newTestService(t)
	ctx := context.Background()
	createUser(t, pool, "alice", "rahasia123", true)
	a := login(t, svc, "alice", "rahasia123")
	b := login(t, svc, "alice", "rahasia123")

	require.NoError(t, svc.Logout(ctx, a.FamilyID))

	_, err := svc.Authenticate(ctx, a.NewToken)
	assert.ErrorIs(t, err, apperr.ErrUnauthenticated)
	_, err = svc.Authenticate(ctx, b.NewToken)
	assert.NoError(t, err)
}

func TestRevokeUserSessions(t *testing.T) {
	svc, pool, _ := newTestService(t)
	ctx := context.Background()
	id := createUser(t, pool, "alice", "rahasia123", true)
	createUser(t, pool, "bob", "rahasia123", true)
	a := login(t, svc, "alice", "rahasia123")
	b := login(t, svc, "alice", "rahasia123")
	other := login(t, svc, "bob", "rahasia123")

	require.NoError(t, svc.RevokeUserSessions(ctx, id))

	for _, tok := range []string{a.NewToken, b.NewToken} {
		_, err := svc.Authenticate(ctx, tok)
		assert.ErrorIs(t, err, apperr.ErrUnauthenticated)
	}
	_, err := svc.Authenticate(ctx, other.NewToken)
	assert.NoError(t, err)
}

func TestSessionTokenStoredOnlyAsHash(t *testing.T) {
	svc, pool, _ := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)
	s := login(t, svc, "alice", "rahasia123")

	var hashed, raw int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE token_hash = $1), count(*) FILTER (WHERE token_hash = convert_to($2, 'UTF8'))
		 FROM session_tokens`,
		hashToken(s.NewToken), s.NewToken).Scan(&hashed, &raw)
	require.NoError(t, err)
	assert.Equal(t, 1, hashed)
	assert.Equal(t, 0, raw)
}
