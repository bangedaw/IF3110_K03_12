package web_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplicationLogsNeverContainSecrets(t *testing.T) {
	e := newEnv(t, true)
	secrets := []string{"LEAK-login-pw", "LEAK-conn-pw", "LEAK-bad-csrf", "LEAK-cookie", "LEAK-query", "LEAK-user-pw", "LEAK-reset-pw", "LEAK-stored-pw"}
	deadForm := validConnectionForm("mati")
	deadForm.Set("host", "127.0.0.1")
	deadForm.Set("port", "1")
	deadForm.Set("password", "LEAK-conn-pw")
	deadForm.Set("id", "0")
	created := validConnectionForm("baru")
	created.Set("password", "LEAK-stored-pw")
	newUser := newUserForm("loguser", "LEAK-user-pw")
	userID := e.insertUser("target-reset", true)

	e.do(http.MethodPost, "/login", 0, loginForm("alice", "LEAK-login-pw", "/databases?x=LEAK-query"), []*http.Cookie{{Name: "dbmp_session", Value: "LEAK-cookie"}})
	e.do(http.MethodPost, "/login", 0, loginForm("hantu", "LEAK-login-pw", ""), nil)
	e.postNoCSRF("/admin/connections/new", adminID, url.Values{"_csrf": {"LEAK-bad-csrf"}, "password": {"LEAK-conn-pw"}})
	e.post("/admin/connections/test", adminID, deadForm)
	e.post("/admin/connections/new", adminID, created)
	e.post("/admin/users/new", adminID, newUser)
	e.post(fmt.Sprintf("/admin/users/%d/password", userID), adminID, url.Values{"password": {"LEAK-reset-pw"}, "passwordConfirm": {"LEAK-reset-pw"}})
	e.get("/databases?filter=LEAK-query", viewerID)
	e.get(dbPath(e.deadID, ""), viewerID)
	e.get(dbPath(e.deadID, "/schemas/app/tables/books?tab=data"), viewerID)
	e.post(dbPath(e.deadID, "/diff"), viewerID, diffForm("database", e.liveID))

	logs := e.logs.String()
	require.NotEmpty(t, logs, "request harus tercatat di log")
	assert.Contains(t, logs, "/databases")
	for _, secret := range append(secrets, "apa-saja", csrfFor(adminID), csrfFor(viewerID)) {
		assert.NotContains(t, logs, secret, "log tidak boleh memuat %q", secret)
	}
}
