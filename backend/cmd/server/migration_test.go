package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T {
	t.Helper()
	require.Equal(t, status, rec.Code, rec.Body.String())
	var v T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	return v
}

func TestMigrationAPIFromDraftToQueue(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	live := testutil.NewPostgresTarget(t).Config().ConnConfig
	const pw = "rahasia123"
	insertUser(t, pool, "admin", pw, true)
	aliceID := insertUser(t, pool, "alice", pw, false)
	bobID := insertUser(t, pool, "bob", pw, false)
	insertUser(t, pool, "mallory", pw, false)
	cfg := config.Config{
		SecretKey: "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8=",
		CSRFKey:   "test-csrf-key",
		Session: config.Session{
			IdleTimeout: 30 * time.Minute, AbsoluteTimeout: 8 * time.Hour,
			RotationInterval: 15 * time.Minute, GracePeriod: 30 * time.Second, TouchInterval: time.Minute,
		},
	}
	h, cleanup, err := newHandler(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)), fstest.MapFS{"index.html": {Data: []byte("x")}})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	admin := loginAs(t, h, "admin", pw)
	dbID := createdID(t, admin.do(http.MethodPost, "/api/admin/databases", connectionBody("live", live.Host, int(live.Port), live.Database, live.User, live.Password)))
	for name, leader := range map[string]struct {
		id    int64
		level string
	}{"editors": {aliceID, "edit"}, "viewers": {bobID, "view"}} {
		groupID := createdID(t, admin.do(http.MethodPost, "/api/admin/groups", fmt.Sprintf(`{"name":%q,"leaderId":%d}`, name, leader.id)))
		rec := admin.do(http.MethodPut, fmt.Sprintf("/api/admin/groups/%d/grants/%d", groupID, dbID), fmt.Sprintf(`{"level":%q}`, leader.level))
		require.Less(t, rec.Code, 300, rec.Body.String())
	}
	alice, bob, mallory := loginAs(t, h, "alice", pw), loginAs(t, h, "bob", pw), loginAs(t, h, "mallory", pw)
	list := fmt.Sprintf("/api/databases/%d/migrations", dbID)
	definition := `{"title":"buku","description":"","upSql":"CREATE TABLE app.t (id int)","downSql":"DROP TABLE app.t","irreversible":false,"irreversibleReason":""}`

	assert.Equal(t, http.StatusUnauthorized, (&client{t: t, h: h}).do(http.MethodGet, list, "").Code)
	assert.Equal(t, http.StatusForbidden, mallory.do(http.MethodGet, list, "").Code)
	assert.Equal(t, http.StatusForbidden, admin.do(http.MethodPost, list, definition).Code, "Platform Admin tidak otomatis punya akses data")
	assert.Equal(t, http.StatusForbidden, bob.do(http.MethodPost, list, definition).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, alice.do(http.MethodPost, list, `{"title":""}`).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, alice.do(http.MethodPost, list, `{"tidakDikenal":1}`).Code)
	m := decodeBody[apigen.Migration](t, alice.do(http.MethodPost, list, definition), http.StatusCreated)
	path := fmt.Sprintf("/api/migrations/%d", m.Id)

	assert.Equal(t, http.StatusNotFound, alice.do(http.MethodGet, "/api/migrations/abc", "").Code)
	assert.Equal(t, http.StatusNotFound, alice.do(http.MethodGet, path+"/approvals", "").Code, "belum ada request")
	assert.Equal(t, http.StatusConflict, alice.do(http.MethodPut, path, `{"title":"x","upSql":"SELECT 1","downSql":"SELECT 1","irreversible":false,"irreversibleReason":"","revision":9}`).Code)
	assert.Len(t, decodeBody[[]apigen.Migration](t, bob.do(http.MethodGet, list, ""), http.StatusOK), 1)
	plan := decodeBody[[]apigen.MigrationPlan](t, alice.do(http.MethodPost, path+"/explain", `{"analyze":false}`), http.StatusOK)
	assert.Len(t, plan, 1)

	m = decodeBody[apigen.Migration](t, alice.do(http.MethodPost, path+"/submit", ""), http.StatusCreated)
	body := fmt.Sprintf(`{"requestId":%d,"decision":"approved"}`, m.Request.Id)
	assert.Equal(t, http.StatusUnprocessableEntity, bob.do(http.MethodPost, path+"/approvals", `{"requestId":1,"decision":"mungkin"}`).Code)
	decodeBody[apigen.Migration](t, alice.do(http.MethodPost, path+"/approvals", body), http.StatusOK)
	approvals := decodeBody[apigen.MigrationApprovals](t, bob.do(http.MethodGet, path+"/approvals", ""), http.StatusOK)
	assert.True(t, approvals.CanDecide)
	m = decodeBody[apigen.Migration](t, bob.do(http.MethodPost, path+"/approvals", body), http.StatusOK)
	assert.Equal(t, apigen.MigrationStatusApproved, m.Status)

	execute := fmt.Sprintf(`{"requestId":%d}`, m.Request.Id)
	assert.Equal(t, http.StatusForbidden, bob.do(http.MethodPost, path+"/execute", execute).Code)
	job := decodeBody[apigen.MigrationJob](t, alice.do(http.MethodPost, path+"/execute", execute), http.StatusAccepted)
	status := decodeBody[apigen.Job](t, bob.do(http.MethodGet, fmt.Sprintf("/api/databases/%d/jobs/%d", dbID, job.JobId), ""), http.StatusOK)
	assert.Equal(t, apigen.JobStatusPending, status.Status)
	assert.Equal(t, http.StatusConflict, alice.do(http.MethodPost, path+"/rollback", "").Code)
	assert.Equal(t, http.StatusConflict, alice.do(http.MethodDelete, path, "").Code)
	history := fmt.Sprintf("/api/databases/%d/migration-history?hideRollback=true", dbID)
	assert.Empty(t, decodeBody[[]apigen.MigrationExecution](t, bob.do(http.MethodGet, history, ""), http.StatusOK))
	assert.Equal(t, http.StatusUnprocessableEntity, bob.do(http.MethodGet, history+"&migrationId=x", "").Code)

	var audited int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE resource_type = 'migration'`).Scan(&audited))
	assert.Equal(t, 5, audited, "create, request, dua keputusan, dan execute")
}
