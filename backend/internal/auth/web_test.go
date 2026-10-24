package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
)

func authRequest(cookie *http.Cookie) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodGet, "/databases", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return httptest.NewRecorder(), req
}

func TestWebAuthenticateRejectsMissingCookie(t *testing.T) {
	svc, _, _ := newTestService(t)

	w, r := authRequest(nil)
	_, err := svc.WebAuthenticate(w, r)

	require.ErrorIs(t, err, apperr.ErrUnauthenticated)
}

func TestWebAuthenticateReturnsIdentityAndCSRF(t *testing.T) {
	svc, pool, _ := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)
	sess := login(t, svc, "alice", "rahasia123")

	w, r := authRequest(&http.Cookie{Name: CookieName, Value: sess.NewToken})
	id, err := svc.WebAuthenticate(w, r)

	require.NoError(t, err)
	assert.Equal(t, "alice", id.Username)
	assert.Equal(t, sess.Principal.UserID, id.Principal.UserID)
	assert.Equal(t, svc.csrfToken(sess.FamilyID), id.CSRF)
	assert.True(t, svc.validCSRF(id.FamilyID, id.CSRF))
}

func TestWebAuthenticateClearsCookieOnInvalidToken(t *testing.T) {
	svc, _, _ := newTestService(t)

	w, r := authRequest(&http.Cookie{Name: CookieName, Value: "token-palsu"})
	_, err := svc.WebAuthenticate(w, r)

	require.Error(t, err)
	cleared := sessionCookie(t, w)
	require.NotNil(t, cleared)
	assert.Equal(t, -1, cleared.MaxAge)
}

func TestWebAuthenticateRotatesCookieWhenDue(t *testing.T) {
	svc, pool, clk := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)
	sess := login(t, svc, "alice", "rahasia123")
	clk.advance(testSessionCfg.RotationInterval + time.Minute)

	w, r := authRequest(&http.Cookie{Name: CookieName, Value: sess.NewToken})
	_, err := svc.WebAuthenticate(w, r)

	require.NoError(t, err)
	rotated := sessionCookie(t, w)
	require.NotNil(t, rotated)
	assert.NotEqual(t, sess.NewToken, rotated.Value)
}

func TestWebLogoutRevokesSession(t *testing.T) {
	svc, pool, _ := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)
	sess := login(t, svc, "alice", "rahasia123")
	cookie := &http.Cookie{Name: CookieName, Value: sess.NewToken}

	w, r := authRequest(cookie)
	id, err := svc.WebAuthenticate(w, r)
	require.NoError(t, err)

	w, r = authRequest(cookie)
	require.NoError(t, svc.WebLogout(w, r, id))
	assert.Equal(t, -1, sessionCookie(t, w).MaxAge)

	w, r = authRequest(cookie)
	_, err = svc.WebAuthenticate(w, r)
	require.Error(t, err)
}

func TestWebLoginSetsSessionCookie(t *testing.T) {
	svc, pool, _ := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)

	w, r := authRequest(nil)
	err := svc.WebLogin(w, r, "  alice ", "rahasia123")

	require.NoError(t, err)
	cookie := sessionCookie(t, w)
	require.NotNil(t, cookie)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	w2, r2 := authRequest(cookie)
	id, err := svc.WebAuthenticate(w2, r2)
	require.NoError(t, err)
	assert.Equal(t, "alice", id.Username)
}

func TestWebLoginRejectsBadCredentialsWithoutCookie(t *testing.T) {
	svc, pool, _ := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)
	createUser(t, pool, "nonaktif", "rahasia123", false)

	cases := map[string][2]string{
		"password salah": {"alice", "salah"},
		"user tidak ada": {"hantu", "rahasia123"},
		"user nonaktif":  {"nonaktif", "rahasia123"},
	}
	var messages []string
	for name, c := range cases {
		w, r := authRequest(nil)
		err := svc.WebLogin(w, r, c[0], c[1])
		require.ErrorIs(t, err, apperr.ErrUnauthenticated, name)
		assert.Nil(t, sessionCookie(t, w), name)
		messages = append(messages, err.Error())
	}
	assert.Len(t, messages, 3)
	assert.Equal(t, messages[0], messages[1])
	assert.Equal(t, messages[1], messages[2])
}

func TestWebLoginValidatesRequiredFields(t *testing.T) {
	svc, _, _ := newTestService(t)

	w, r := authRequest(nil)
	err := svc.WebLogin(w, r, "   ", "")

	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperr.CodeValidation, ae.Code)
	assert.Contains(t, ae.Fields, "username")
	assert.Contains(t, ae.Fields, "password")
}

func TestWebLoginRecordsAuditEntries(t *testing.T) {
	svc, pool, _ := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)

	w, r := authRequest(nil)
	require.Error(t, svc.WebLogin(w, r, "alice", "salah"))
	w, r = authRequest(nil)
	require.NoError(t, svc.WebLogin(w, r, "alice", "rahasia123"))

	rows, err := pool.Query(r.Context(), `SELECT outcome, metadata::text FROM audit_logs WHERE action = 'auth.login' ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var outcomes, metadata []string
	for rows.Next() {
		var outcome, meta string
		require.NoError(t, rows.Scan(&outcome, &meta))
		outcomes = append(outcomes, outcome)
		metadata = append(metadata, meta)
	}
	assert.Equal(t, []string{"failure", "success"}, outcomes)
	for _, m := range metadata {
		assert.NotContains(t, m, "rahasia123")
		assert.NotContains(t, m, "salah")
	}
}
