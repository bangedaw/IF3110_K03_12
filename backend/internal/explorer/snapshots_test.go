package explorer_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/explorer"
)

func post(t *testing.T, srv *httptest.Server, path, user string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, nil)
	require.NoError(t, err)
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, string(raw)
}

func TestCreateAndListSnapshots(t *testing.T) {
	f := setup(t)
	base := "/api/databases/" + strconv.FormatInt(f.liveID, 10)

	code, body := post(t, f.srv, base+"/snapshots", "42")
	require.Equal(t, http.StatusCreated, code, "body: %s", body)
	var snap explorer.Snapshot
	require.NoError(t, json.Unmarshal([]byte(body), &snap), "body: %s", body)
	require.Positive(t, snap.ID)
	require.Equal(t, f.liveID, snap.DatabaseID)
	require.NotNil(t, snap.TakenBy)
	require.EqualValues(t, 42, *snap.TakenBy)
	require.NotNil(t, snap.Model)
	require.NotEmpty(t, snap.Model.Schemas)
	require.Equal(t, "app", snap.Model.Schemas[0].Name)

	var list []explorer.Snapshot
	getJSON(t, f.srv, base+"/snapshots", "42", &list)
	require.NotEmpty(t, list)
	require.Equal(t, snap.ID, list[0].ID)

	var got explorer.Snapshot
	getJSON(t, f.srv, base+"/snapshots/"+strconv.FormatInt(snap.ID, 10), "42", &got)
	require.Equal(t, snap.ID, got.ID)
	books, ok := got.Model.Find("app", "books")
	require.True(t, ok)
	require.Len(t, books.Columns, 6)
}

func TestSnapshotAccess(t *testing.T) {
	f := setup(t)
	base := "/api/databases/" + strconv.FormatInt(f.liveID, 10)

	code, _ := get(t, f.srv, base+"/snapshots", "43")
	require.Equal(t, http.StatusForbidden, code)
	code, _ = post(t, f.srv, base+"/snapshots", "43")
	require.Equal(t, http.StatusForbidden, code)

	code, _ = get(t, f.srv, base+"/snapshots", "")
	require.Equal(t, http.StatusUnauthorized, code)

	code, _ = get(t, f.srv, base+"/snapshots/999999", "42")
	require.Equal(t, http.StatusNotFound, code)
}

func TestSnapshotNotFoundOnDeadTarget(t *testing.T) {
	f := setup(t)
	base := "/api/databases/" + strconv.FormatInt(f.deadID, 10)

	code, body := post(t, f.srv, base+"/snapshots", "42")
	require.Equal(t, http.StatusServiceUnavailable, code, "body: %s", body)
	require.Equal(t, "DB_UNREACHABLE", problemCode(t, body))
}
