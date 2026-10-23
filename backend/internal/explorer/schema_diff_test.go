package explorer_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/explorer"
)

func postJSON(t *testing.T, srvURL, path, user string, body any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, srvURL+path, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(res.Body)
	require.NoError(t, err)
	return res.StatusCode, buf.String()
}

func TestSchemaDiffAgainstSnapshot(t *testing.T) {
	f := setup(t)
	base := "/api/databases/" + strconv.FormatInt(f.liveID, 10)

	code, body := post(t, f.srv, base+"/snapshots", "42")
	require.Equal(t, http.StatusCreated, code, "body: %s", body)
	var snap explorer.Snapshot
	require.NoError(t, json.Unmarshal([]byte(body), &snap))

	_, err := f.target.Exec(t.Context(),
		`ALTER TABLE app.books ADD COLUMN subtitle text`)
	require.NoError(t, err)

	code, body = postJSON(t, f.srv.URL, base+"/schema-diff", "42", map[string]any{
		"against":   map[string]any{"type": "snapshot", "id": snap.ID},
		"direction": "toTarget",
	})
	require.Equal(t, http.StatusOK, code, "body: %s", body)
	var res explorer.SchemaDiffResult
	require.NoError(t, json.Unmarshal([]byte(body), &res), "body: %s", body)
	require.NotEmpty(t, res.Changes)
	require.Contains(t, res.SQL, "DROP COLUMN")
	require.Contains(t, res.SQL, "subtitle")
	found := false
	for _, c := range res.Changes {
		if c.Kind == "drop_column" && c.Name == "subtitle" && c.Destructive {
			found = true
		}
	}
	require.True(t, found, "changes: %+v", res.Changes)

	code, body = postJSON(t, f.srv.URL, base+"/schema-diff", "42", map[string]any{
		"against":   map[string]any{"type": "database", "id": f.liveID},
		"direction": "toTarget",
	})
	require.Equal(t, http.StatusOK, code, "body: %s", body)
	require.NoError(t, json.Unmarshal([]byte(body), &res))
	require.Empty(t, res.Changes)
	require.Empty(t, res.SQL)
}

func TestSchemaDiffAccessAndValidation(t *testing.T) {
	f := setup(t)
	base := "/api/databases/" + strconv.FormatInt(f.liveID, 10)
	ok := map[string]any{
		"against":   map[string]any{"type": "database", "id": f.liveID},
		"direction": "toTarget",
	}

	code, _ := postJSON(t, f.srv.URL, base+"/schema-diff", "43", ok)
	require.Equal(t, http.StatusForbidden, code)

	code, body := postJSON(t, f.srv.URL, base+"/schema-diff", "42", map[string]any{
		"against":   map[string]any{"type": "database", "id": f.nograntID},
		"direction": "toTarget",
	})
	require.Equal(t, http.StatusForbidden, code, "body: %s", body)

	code, body = postJSON(t, f.srv.URL, base+"/schema-diff", "42", map[string]any{
		"against":   map[string]any{"type": "snapshot", "id": 999999},
		"direction": "toTarget",
	})
	require.Equal(t, http.StatusNotFound, code, "body: %s", body)

	code, body = postJSON(t, f.srv.URL, base+"/schema-diff", "42", map[string]any{
		"against":   map[string]any{"type": "database", "id": f.liveID},
		"direction": "sideways",
	})
	require.Equal(t, http.StatusUnprocessableEntity, code, "body: %s", body)
	require.Equal(t, "VALIDATION_FAILED", problemCode(t, body))

	code, _ = postJSON(t, f.srv.URL, base+"/schema-diff", "42", map[string]any{
		"against":   map[string]any{"type": "table", "id": 1},
		"direction": "toTarget",
	})
	require.Equal(t, http.StatusUnprocessableEntity, code)

	code, _ = postJSON(t, f.srv.URL, base+"/schema-diff", "42", map[string]any{
		"against":   map[string]any{"type": "database", "id": f.liveID},
		"direction": "toTarget",
		"extra":     true,
	})
	require.Equal(t, http.StatusUnprocessableEntity, code)

	code, _ = postJSON(t, f.srv.URL, base+"/schema-diff", "", ok)
	require.Equal(t, http.StatusUnauthorized, code)

	code, body = postJSON(t, f.srv.URL,
		"/api/databases/"+strconv.FormatInt(f.deadID, 10)+"/schema-diff", "42", ok)
	require.Equal(t, http.StatusServiceUnavailable, code, "body: %s", body)
	require.Equal(t, "DB_UNREACHABLE", problemCode(t, body))
	require.NotContains(t, body, "apa-saja")
}
