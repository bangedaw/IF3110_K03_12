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
